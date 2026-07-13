package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mnexa-AI/e2a/internal/delivery"
	"github.com/jackc/pgx/v5"
)

// --- Delivery feedback (decision 9 / Slice 4b) ---
//
// These back internal/delivery's Consumer.Store and the send path + the
// /v1/account/suppressions endpoints. delivery is a stdlib-only leaf package,
// so identity importing it (for delivery.Status / delivery.Merge) adds no heavy
// deps — unlike senderidentity, no adapter is needed.

// CorrelateBySESMessageID finds the outbound message + owning user by the
// SES-assigned provider_message_id captured at send time. found=false when the
// id is unknown (expired message, or an event for a different deployment).
//
// subject rides along for the delivered/bounced/complained event payloads —
// it's a column on the very row this query already reads, so including it
// costs no extra query (contract freeze PR-2: `subject` on delivery events).
//
// The SNS notification carries the BARE SES id (e.g. 010f0193…-000000), but the
// send path stores it angle-bracketed and sometimes with an @region.amazonses.com
// suffix (parseMessageIDFromResponse) — same discrepancy LookupConversationID
// works around. Match all three stored shapes against the bare id: exact,
// <id>, and <id@…>. SES ids are [A-Za-z0-9-] so they carry no LIKE metacharacters.
func (s *Store) CorrelateBySESMessageID(ctx context.Context, sesMessageID string) (messageID, userID, agentID, subject string, found bool, err error) {
	if sesMessageID == "" {
		return "", "", "", "", false, nil
	}
	err = s.pool.QueryRow(ctx,
		`SELECT m.id, a.user_id, m.agent_id, COALESCE(m.subject, '')
		   FROM messages m
		   JOIN agent_identities a ON a.id = m.agent_id
		  WHERE m.direction = 'outbound'
		    AND ( m.provider_message_id = $1
		       OR m.provider_message_id = '<' || $1 || '>'
		       OR m.provider_message_id LIKE '<' || $1 || '@%' )
		  LIMIT 1`,
		sesMessageID,
	).Scan(&messageID, &userID, &agentID, &subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", "", false, err
	}
	return messageID, userID, agentID, subject, true, nil
}

// RecordDeliveryOutcome upserts one recipient's status monotonically (by the
// delivery precedence) and recomputes the message's rollup delivery_status as
// the worst status across its recipients. Runs in a tx with FOR UPDATE so
// concurrent SNS events can't race the merge. Idempotent: a duplicate or older
// event is a no-op for the status (detail still refreshes on an equal/higher).
func (s *Store) RecordDeliveryOutcome(ctx context.Context, messageID, address string, status delivery.Status, detail string) error {
	addr := NormalizeEmail(address)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Lock the message row to serialize ALL events for this message (every
	// recipient). SES fans out delivery + bounce/complaint for the same message
	// concurrently; without this, two events for different recipients (or two
	// first-events for an un-pre-populated recipient) race the rollup write and
	// the insert ON CONFLICT path, dropping a terminal status. The lock makes
	// the read-merge-write below strictly monotonic per message.
	var lockedID string
	err = tx.QueryRow(ctx, `SELECT id FROM messages WHERE id = $1 FOR UPDATE`, messageID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // message deleted between correlation and now
	}
	if err != nil {
		return err
	}

	var cur string
	err = tx.QueryRow(ctx,
		`SELECT status FROM message_recipients WHERE message_id = $1 AND address = $2`,
		messageID, addr,
	).Scan(&cur)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Recipient row not pre-populated (e.g. SES reports an address the send
		// path didn't record). Serialized by the message-row lock, so no insert
		// race.
		if _, err := tx.Exec(ctx,
			`INSERT INTO message_recipients (id, message_id, address, status, detail)
			 VALUES ($1, $2, $3, $4, $5)`,
			"rcpt_"+generateID(), messageID, addr, string(status), nullIfEmpty(detail),
		); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		merged := delivery.Merge(delivery.Status(cur), status)
		// Only write when the status actually advances — a duplicate/lower-rank
		// event must not regress the status NOR clobber the diagnostic detail
		// (a late `delivered` carrying a detail must not overwrite the bounce
		// reason).
		if merged != delivery.Status(cur) {
			if _, err := tx.Exec(ctx,
				`UPDATE message_recipients SET status = $3, detail = COALESCE($4, detail), updated_at = now()
				  WHERE message_id = $1 AND address = $2`,
				messageID, addr, string(merged), nullIfEmpty(detail),
			); err != nil {
				return err
			}
		}
	}

	// Recompute the rollup = worst recipient status by precedence. Few
	// recipients per message, so reduce in Go to keep the rank logic in one
	// place (delivery.Merge).
	rows, err := tx.Query(ctx, `SELECT status FROM message_recipients WHERE message_id = $1`, messageID)
	if err != nil {
		return err
	}
	var rollup delivery.Status
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			rows.Close()
			return err
		}
		rollup = delivery.Merge(rollup, delivery.Status(st))
	}
	rows.Close()
	if rollup != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE messages SET delivery_status = $2 WHERE id = $1`, messageID, string(rollup),
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// MarkMessageSent records that an outbound message was accepted by the relay:
// delivery_status='sent', the From identity actually used, and one
// message_recipients row per recipient (to/cc/bcc) at 'sent'. Called after a
// successful relay accept. Idempotent on the recipient rows.
func (s *Store) MarkMessageSent(ctx context.Context, messageID, sentAs string, to, cc, bcc []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE messages SET delivery_status = 'sent', sent_as = $2 WHERE id = $1`,
		messageID, nullIfEmpty(sentAs),
	); err != nil {
		return err
	}
	add := func(addrs []string, kind string) error {
		for _, a := range addrs {
			addr := NormalizeEmail(a)
			if addr == "" {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO message_recipients (id, message_id, address, kind, status)
				 VALUES ($1, $2, $3, $4, 'sent')
				 ON CONFLICT (message_id, address) DO NOTHING`,
				"rcpt_"+generateID(), messageID, addr, kind,
			); err != nil {
				return err
			}
		}
		return nil
	}
	if err := add(to, "to"); err != nil {
		return err
	}
	if err := add(cc, "cc"); err != nil {
		return err
	}
	if err := add(bcc, "bcc"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- Async outbound send (async-message-pipeline.md, slice C) ---

// OutboundSendPayload is the async worker's view of an accepted outbound message
// (the LoadOutboundForSend result). Recipients is the SMTP envelope (to+cc+bcc);
// Raw is the persisted Sent-folder bytes the worker submits via Sender.SubmitOnce.
type OutboundSendPayload struct {
	ID             string
	DeliveryStatus string
	EnvelopeFrom   string
	SentAs         string
	Recipients     []string
	Raw            []byte
	CreatedAt      time.Time // accept time — the outage-tail retry-horizon clock
}

// OutboundSentInfo carries the fields the async worker's MarkSent/MarkFailed
// adapters need to build the email.sent / email.failed event and meter usage,
// resolved from the row + its owning agent in one transaction.
type OutboundSentInfo struct {
	Message *Message
	UserID  string
	Domain  string
}

// SendOutcome is the current terminal-ish state of an async send, for wait=sent
// polling. DeliveryStatus is "" when the row is gone.
type SendOutcome struct {
	DeliveryStatus    string
	ProviderMessageID string
	SentAs            string
	DeliveryDetail    string
}

// GetSendOutcome reads the current delivery_status + provider id + sent_as + detail
// for an outbound message — the wait=sent poll. Returns a zero-value outcome (empty
// DeliveryStatus) if the row is gone, never an error for a missing row.
func (s *Store) GetSendOutcome(ctx context.Context, messageID string) (SendOutcome, error) {
	var o SendOutcome
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(delivery_status,''), COALESCE(provider_message_id,''),
		        COALESCE(sent_as,''), COALESCE(delivery_detail,'')
		   FROM messages WHERE id = $1 AND direction = 'outbound'`,
		messageID,
	).Scan(&o.DeliveryStatus, &o.ProviderMessageID, &o.SentAs, &o.DeliveryDetail)
	if errors.Is(err, pgx.ErrNoRows) {
		return SendOutcome{}, nil
	}
	if err != nil {
		return SendOutcome{}, err
	}
	return o, nil
}

// LoadOutboundForSend returns the payload the async send worker submits, or nil if
// the row is gone (agent-delete cascade / TTL) — the worker treats that as a
// no-op. Reads the envelope (to+cc+bcc) and the persisted wire bytes; does not
// touch message_recipients (those are written at MarkSent).
func (s *Store) LoadOutboundForSend(ctx context.Context, messageID string) (*OutboundSendPayload, error) {
	var (
		deliveryStatus string
		envelopeFrom   string
		sentAs         string
		to, cc, bcc    []string
		raw            []byte
		createdAt      time.Time
	)
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(delivery_status,''), COALESCE(envelope_from,''), COALESCE(sent_as,''),
		        to_recipients, cc, bcc, raw_message, created_at
		   FROM messages
		  WHERE id = $1 AND direction = 'outbound'`,
		messageID,
	).Scan(&deliveryStatus, &envelopeFrom, &sentAs, &to, &cc, &bcc, &raw, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	recipients := make([]string, 0, len(to)+len(cc)+len(bcc))
	recipients = append(recipients, to...)
	recipients = append(recipients, cc...)
	recipients = append(recipients, bcc...)
	return &OutboundSendPayload{
		ID:             messageID,
		DeliveryStatus: deliveryStatus,
		EnvelopeFrom:   envelopeFrom,
		SentAs:         sentAs,
		Recipients:     recipients,
		Raw:            raw,
		CreatedAt:      createdAt,
	}, nil
}

// MarkOutboundSentTx records, within the caller's transaction, that an accepted
// outbound message was submitted to the provider: delivery_status='sent',
// provider_message_id, and one message_recipients row per recipient at 'sent'
// (mirrors MarkMessageSent's recipient shape). sent_as is left as the accept-time
// value. Returns the row + owning user/domain for the caller to emit email.sent +
// meter usage, or nil if the row is gone (deleted between load and mark).
func (s *Store) MarkOutboundSentTx(ctx context.Context, tx pgx.Tx, messageID, providerMessageID string) (*OutboundSentInfo, error) {
	m := &Message{ID: messageID, Direction: "outbound", DeliveryStatus: "sent", ProviderMessageID: providerMessageID}
	err := tx.QueryRow(ctx,
		`UPDATE messages
		    SET delivery_status = 'sent', provider_message_id = $2
		  WHERE id = $1 AND direction = 'outbound'
		 RETURNING agent_id, subject, message_type, method, conversation_id, sender, to_recipients, cc, bcc`,
		messageID, providerMessageID,
	).Scan(&m.AgentID, &m.Subject, &m.Type, &m.Method, &m.ConversationID, &m.Sender, &m.ToRecipients, &m.CC, &m.BCC)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	add := func(addrs []string, kind string) error {
		for _, a := range addrs {
			addr := NormalizeEmail(a)
			if addr == "" {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO message_recipients (id, message_id, address, kind, status)
				 VALUES ($1, $2, $3, $4, 'sent')
				 ON CONFLICT (message_id, address) DO NOTHING`,
				"rcpt_"+generateID(), messageID, addr, kind,
			); err != nil {
				return err
			}
		}
		return nil
	}
	if err := add(m.ToRecipients, "to"); err != nil {
		return nil, err
	}
	if err := add(m.CC, "cc"); err != nil {
		return nil, err
	}
	if err := add(m.BCC, "bcc"); err != nil {
		return nil, err
	}
	info := &OutboundSentInfo{Message: m}
	if err := tx.QueryRow(ctx,
		`SELECT user_id, domain FROM agent_identities WHERE id = $1`, m.AgentID,
	).Scan(&info.UserID, &info.Domain); err != nil {
		return nil, err
	}
	return info, nil
}

// MarkOutboundFailedTx records, within the caller's transaction, a terminal
// outbound send failure: delivery_status='failed' + delivery_detail. Returns the
// row + owning user for the caller to emit email.failed, or nil if the row is gone.
func (s *Store) MarkOutboundFailedTx(ctx context.Context, tx pgx.Tx, messageID, detail string) (*OutboundSentInfo, error) {
	m := &Message{ID: messageID, Direction: "outbound", DeliveryStatus: "failed", DeliveryDetail: detail}
	err := tx.QueryRow(ctx,
		`UPDATE messages
		    SET delivery_status = 'failed', delivery_detail = $2
		  WHERE id = $1 AND direction = 'outbound'
		 RETURNING agent_id, subject, message_type, method, conversation_id, sender, to_recipients, cc, bcc`,
		messageID, nullIfEmpty(detail),
	).Scan(&m.AgentID, &m.Subject, &m.Type, &m.Method, &m.ConversationID, &m.Sender, &m.ToRecipients, &m.CC, &m.BCC)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := &OutboundSentInfo{Message: m}
	if err := tx.QueryRow(ctx,
		`SELECT user_id, domain FROM agent_identities WHERE id = $1`, m.AgentID,
	).Scan(&info.UserID, &info.Domain); err != nil {
		return nil, err
	}
	return info, nil
}

// --- Suppression list ---

// Suppression is one (user, address) entry on the per-tenant suppression list.
type Suppression struct {
	Address         string    `json:"address"`
	Reason          string    `json:"reason,omitempty"`
	Source          string    `json:"source"` // bounce | complaint | manual
	SourceMessageID string    `json:"source_message_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// AddSuppression idempotently inserts a (user, address) suppression. added is
// false when it already existed, so the caller fires domain.suppression_added
// at most once per address.
func (s *Store) AddSuppression(ctx context.Context, userID, address, reason, source, sourceMessageID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO suppressions (id, user_id, address, reason, source, source_message_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (user_id, address) DO NOTHING`,
		"supp_"+generateID(), userID, NormalizeEmail(address), reason, source, nullIfEmpty(sourceMessageID),
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SuppressedAddresses returns the subset of addrs that are suppressed for the
// user — the send-time enforcement read. Empty input → empty result.
func (s *Store) SuppressedAddresses(ctx context.Context, userID string, addrs []string) ([]string, error) {
	if len(addrs) == 0 {
		return nil, nil
	}
	norm := make([]string, 0, len(addrs))
	for _, a := range addrs {
		norm = append(norm, NormalizeEmail(a))
	}
	rows, err := s.pool.Query(ctx,
		`SELECT address FROM suppressions WHERE user_id = $1 AND address = ANY($2)`,
		userID, norm,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListSuppressions returns the user's suppression list, newest first.
// ListSuppressions returns one page of the user's suppressed addresses,
// newest first, keyset-paginated on (created_at, address). The caller passes
// limit (fetch limit+1 to detect a further page) and the after-key from the
// previous page's last row (zero afterCreatedAt = first page). (A-5: the
// suppression list auto-grows on every bounce/complaint, so it needs real
// pagination, not a single page.)
func (s *Store) ListSuppressions(ctx context.Context, userID string, limit int, afterCreatedAt time.Time, afterAddress string) ([]Suppression, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT address, reason, source, COALESCE(source_message_id, ''), created_at
	        FROM suppressions WHERE user_id = $1`
	args := []interface{}{userID}
	if !afterCreatedAt.IsZero() {
		i := len(args) + 1
		q += fmt.Sprintf(` AND (created_at < $%d OR (created_at = $%d AND address < $%d))`, i, i, i+1)
		args = append(args, afterCreatedAt, afterAddress)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC, address DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suppression
	for rows.Next() {
		var sp Suppression
		if err := rows.Scan(&sp.Address, &sp.Reason, &sp.Source, &sp.SourceMessageID, &sp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// RemoveSuppression deletes a (user, address) suppression. found=false when no
// such entry existed.
func (s *Store) RemoveSuppression(ctx context.Context, userID, address string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM suppressions WHERE user_id = $1 AND address = $2`,
		userID, NormalizeEmail(address),
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

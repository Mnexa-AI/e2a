package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LocalDeliveryTxHook runs after both sides of a providerless local delivery
// are visible in tx and before commit. Callers use it for durable outcome
// events and idempotency completion so none can diverge from the Sent/Inbox
// pair.
type LocalDeliveryTxHook func(ctx context.Context, tx pgx.Tx, outbound, inbound *Message, result SendResult) error

// GetEventEnvelope returns the exact durable event envelope for a message.
// WebSocket reconnect drain uses this instead of rebuilding an event whose
// timestamp or attachment metadata could differ under the same event id.
func (s *Store) GetEventEnvelope(ctx context.Context, messageID, eventType string) ([]byte, error) {
	var envelope []byte
	err := s.pool.QueryRow(ctx,
		`SELECT envelope FROM webhook_events WHERE message_id=$1 AND type=$2`,
		messageID, eventType,
	).Scan(&envelope)
	return envelope, err
}

// ApproveAndDeliverLocal atomically resolves a pending outbound review hold
// whose only recipient is a mailbox owned by this service. Unlike
// ApproveAndSend, compose must be a local, side-effect-free operation: the
// outbound update, recipient-side insert, events, and idempotency completion
// all commit or roll back together, so the SES-oriented send_attempts journal
// is neither needed nor appropriate.
func (s *Store) ApproveAndDeliverLocal(
	ctx context.Context,
	messageID, userID string,
	edits PendingApprovalEdit,
	compose func(msg *Message) (SendResult, error),
	beforeCommit LocalDeliveryTxHook,
) (*Message, error) {
	txCtx, cancel := context.WithTimeout(ctx, approvalTxTimeout)
	defer cancel()

	tx, err := s.pool.Begin(txCtx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(txCtx)
		}
	}()

	m, ownerUserID, err := loadPendingOutboundForLocalDelivery(txCtx, tx, messageID)
	if err != nil {
		return nil, err
	}
	if ownerUserID != userID {
		return nil, ErrMessageNotFound
	}

	editedByReviewer := edits.Apply(m)
	result, err := compose(m)
	if err != nil {
		return nil, err
	}
	if result.Method != "loopback" || len(result.To) != 1 || len(result.Raw) == 0 || result.ProviderMessageID == "" || result.Sender == "" {
		return nil, errors.New("identity: invalid local delivery result")
	}

	reviewerID := userID
	inbound, err := finalizeLocalDeliveryTx(txCtx, tx, m, result, MessageStatusSent, editedByReviewer, &reviewerID)
	if err != nil {
		return nil, err
	}

	if beforeCommit != nil {
		if err := beforeCommit(ctx, tx, m, inbound, result); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(txCtx); err != nil {
		return nil, err
	}
	committed = true
	return m, nil
}

// ExpireAndDeliverLocal is the TTL-worker counterpart of
// ApproveAndDeliverLocal. It requires an expired pending row, records the
// review_expired_approved lifecycle, and atomically creates the Inbox copy and
// outcome events without using the external-provider send journal.
func (s *Store) ExpireAndDeliverLocal(
	ctx context.Context,
	messageID string,
	compose func(msg *Message) (SendResult, error),
	beforeCommit LocalDeliveryTxHook,
) (*Message, error) {
	txCtx, cancel := context.WithTimeout(ctx, approvalTxTimeout)
	defer cancel()

	tx, err := s.pool.Begin(txCtx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(txCtx)
		}
	}()

	m, _, err := loadExpiredPendingOutboundForLocalDelivery(txCtx, tx, messageID)
	if err != nil {
		return nil, err
	}

	result, err := compose(m)
	if err != nil {
		return nil, err
	}
	if result.Method != "loopback" || len(result.To) != 1 || len(result.Raw) == 0 || result.ProviderMessageID == "" || result.Sender == "" {
		return nil, errors.New("identity: invalid local delivery result")
	}

	inbound, err := finalizeLocalDeliveryTx(txCtx, tx, m, result, MessageStatusReviewExpiredApproved, false, nil)
	if err != nil {
		return nil, err
	}
	if beforeCommit != nil {
		if err := beforeCommit(ctx, tx, m, inbound, result); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(txCtx); err != nil {
		return nil, err
	}
	committed = true
	return m, nil
}

func finalizeLocalDeliveryTx(
	ctx context.Context,
	tx pgx.Tx,
	m *Message,
	result SendResult,
	targetStatus string,
	editedByReviewer bool,
	reviewedByUserID *string,
) (*Message, error) {
	_, err := tx.Exec(ctx,
		`UPDATE messages
		    SET status                = $2,
		        delivery_status       = 'sent',
		        provider_message_id   = $3,
		        method                = $4,
		        to_recipients         = $5,
		        cc                    = $6,
		        bcc                   = $7,
		        recipient             = $8,
		        subject               = $9,
		        edited                = $10,
		        reviewed_at           = now(),
		        reviewed_by_user_id   = $11,
		        raw_message           = $12::bytea,
		        sent_as               = 'own_address',
		        body_text             = NULL,
		        body_html             = NULL,
		        attachments_json      = NULL
		  WHERE id = $1`,
		m.ID,
		targetStatus,
		result.ProviderMessageID,
		result.Method,
		result.To,
		result.CC,
		result.BCC,
		firstOr(result.To, ""),
		m.Subject,
		editedByReviewer || m.Edited,
		reviewedByUserID,
		result.Raw,
	)
	if err != nil {
		return nil, err
	}

	inbound, err := createInboundMessage(
		ctx, tx, "", m.AgentID, result.Sender, m.AgentID,
		result.ProviderMessageID, m.Subject, m.ConversationID, "unread",
		result.Raw, nil, nil, false, "", result.To, result.CC, m.ReplyTo,
		InboundScreening{}, &InboundAuth{HeaderFrom: m.AgentID},
	)
	if err != nil {
		return nil, fmt.Errorf("local delivery inbound row: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE messages SET method='loopback' WHERE id=$1`, inbound.ID); err != nil {
		return nil, fmt.Errorf("local delivery inbound method: %w", err)
	}
	inbound.Method = "loopback"

	m.Status = targetStatus
	m.DeliveryStatus = "sent"
	m.ProviderMessageID = result.ProviderMessageID
	m.Method = result.Method
	m.ToRecipients = result.To
	m.CC = result.CC
	m.BCC = result.BCC
	m.Recipient = firstOr(result.To, "")
	m.Edited = editedByReviewer || m.Edited
	m.RawMessage = result.Raw
	m.SentAs = "own_address"
	m.BodyText = ""
	m.BodyHTML = ""
	m.AttachmentsJSON = nil
	now := time.Now()
	m.ReviewedAt = &now
	m.ReviewedByUserID = reviewedByUserID
	return inbound, nil
}

func loadExpiredPendingOutboundForLocalDelivery(ctx context.Context, tx pgx.Tx, messageID string) (*Message, string, error) {
	return scanPendingOutboundForLocalDelivery(tx.QueryRow(ctx, localDeliverySelect+
		` AND m.approval_expires_at < now()
		  FOR NO KEY UPDATE OF m SKIP LOCKED`, messageID), ErrNotPendingApproval)
}

func loadPendingOutboundForLocalDelivery(ctx context.Context, tx pgx.Tx, messageID string) (*Message, string, error) {
	return scanPendingOutboundForLocalDelivery(tx.QueryRow(ctx, localDeliverySelect+
		` FOR NO KEY UPDATE OF m`, messageID), ErrMessageNotFound)
}

const localDeliverySelect = `SELECT m.id, m.agent_id, m.direction, m.sender, m.recipient, m.subject,
		m.email_message_id, m.method, m.message_type,
		m.conversation_id, m.created_at, m.expires_at,
		m.to_recipients, m.cc, m.bcc, m.reply_to,
		m.status, m.approval_expires_at, m.edited,
		m.body_text, m.body_html, m.attachments_json,
		a.user_id
	 FROM messages m
	 JOIN agent_identities a ON a.id = m.agent_id
	WHERE m.id = $1 AND m.direction = 'outbound'
	  AND a.deleted_at IS NULL`

func scanPendingOutboundForLocalDelivery(row pgx.Row, noRowError error) (*Message, string, error) {
	var (
		m                  Message
		ownerUserID        string
		bodyText, bodyHTML *string
		attachments        []byte
		method, msgType    *string
		approvalExpires    *time.Time
	)
	err := row.Scan(
		&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.Subject,
		&m.EmailMessageID, &method, &msgType,
		&m.ConversationID, &m.CreatedAt, &m.ExpiresAt,
		&m.ToRecipients, &m.CC, &m.BCC, &m.ReplyTo,
		&m.Status, &approvalExpires, &m.Edited,
		&bodyText, &bodyHTML, &attachments,
		&ownerUserID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", noRowError
		}
		return nil, "", err
	}
	if m.Status != MessageStatusPendingReview {
		return nil, "", ErrNotPendingApproval
	}
	if method != nil {
		m.Method = *method
	}
	if msgType != nil {
		m.Type = *msgType
	}
	if approvalExpires != nil {
		m.ApprovalExpiresAt = approvalExpires
	}
	if bodyText != nil {
		m.BodyText = *bodyText
	}
	if bodyHTML != nil {
		m.BodyHTML = *bodyHTML
	}
	if len(attachments) > 0 {
		m.AttachmentsJSON = json.RawMessage(attachments)
	}
	return &m, ownerUserID, nil
}

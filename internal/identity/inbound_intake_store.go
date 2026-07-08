package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- Inbound intake (queue-first inbound pipeline, Layer 1) ---
//
// inbound_intake is the durable landing pad the SMTP session writes before 250 (see
// inbound-message-pipeline-river.md). The internal/inboundprocess River worker reads
// a row by id, parses/screens/persists a messages row, and marks the intake
// processed. These methods are the store surface for both sides; the worker's Store
// adapter (in internal/agent) bridges onto them.

// IntakeStatus values (mirrors migration 056's CHECK).
const (
	IntakeStatusAccepted  = "accepted"
	IntakeStatusProcessed = "processed"
	IntakeStatusFailed    = "failed"
)

// NewInboundIntakeID mints an intake row id.
func NewInboundIntakeID() string {
	return "intk_" + generateID()
}

// InboundIntake is the worker's view of an accepted inbound message — the raw MIME
// plus the connection facts (envelope + remote IP) it needs to run SPF/DKIM and
// screening, which cannot be recomputed outside the SMTP session.
type InboundIntake struct {
	ID           string
	Recipient    string
	EnvelopeFrom string
	RemoteIP     string
	Raw          []byte
	MessageID    string // sender's RFC 5322 Message-ID
	ContentHash  string
	Status       string
	CreatedAt    time.Time
}

// InsertInboundIntakeTx writes an accepted intake row inside the accept-tx. It
// returns inserted=false (no error) when the row is a duplicate — the dedup unique
// index (recipient, message_id, content_hash) suppressed it — so the caller knows
// NOT to enqueue a second job and to still answer 250 (idempotent accept).
func (s *Store) InsertInboundIntakeTx(ctx context.Context, tx pgx.Tx, id, recipient, envelopeFrom, remoteIP, messageID, contentHash string, raw []byte) (inserted bool, err error) {
	var returnedID string
	err = tx.QueryRow(ctx,
		`INSERT INTO inbound_intake (id, recipient, envelope_from, remote_ip, raw_message, message_id, content_hash, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'accepted')
		 ON CONFLICT (recipient, message_id, content_hash) DO NOTHING
		 RETURNING id`,
		id, recipient, envelopeFrom, remoteIP, raw, messageID, contentHash,
	).Scan(&returnedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // duplicate — dedup index suppressed the insert
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// StampInboundIntakeJobIDTx records the River job id on the intake row, in the same
// accept-tx as the insert + enqueue (so a committed accepted row always has its job).
func (s *Store) StampInboundIntakeJobIDTx(ctx context.Context, tx pgx.Tx, intakeID string, jobID int64) error {
	_, err := tx.Exec(ctx, `UPDATE inbound_intake SET process_job_id = $2 WHERE id = $1`, intakeID, jobID)
	return err
}

// LoadInboundIntake reads an intake row for the worker. Returns (nil, nil) when the
// row is gone (pruned) so the worker treats it as a no-op.
func (s *Store) LoadInboundIntake(ctx context.Context, intakeID string) (*InboundIntake, error) {
	var it InboundIntake
	err := s.pool.QueryRow(ctx,
		`SELECT id, recipient, envelope_from, remote_ip, raw_message, message_id, content_hash, status, created_at
		   FROM inbound_intake WHERE id = $1`,
		intakeID,
	).Scan(&it.ID, &it.Recipient, &it.EnvelopeFrom, &it.RemoteIP, &it.Raw, &it.MessageID, &it.ContentHash, &it.Status, &it.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

// MarkInboundIntakeProcessedTx flips the intake to 'processed' and links the created
// messages row, in the worker's terminal tx (same tx as the messages insert + outbox
// publish). This is the worker's idempotency gate: a re-drive that finds 'processed'
// no-ops instead of re-creating the message.
func (s *Store) MarkInboundIntakeProcessedTx(ctx context.Context, tx pgx.Tx, intakeID, messageFK string) error {
	// Guard on status='accepted' (defense-in-depth): the worker's Work() already
	// no-ops a non-accepted row before processing, but scoping the flip here means a
	// second concurrent processor of the same intake commits nothing rather than
	// re-linking a different messages row.
	tag, err := tx.Exec(ctx,
		`UPDATE inbound_intake SET status = 'processed', message_fk = $2, processed_at = now()
		  WHERE id = $1 AND status = 'accepted'`,
		intakeID, messageFK)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrIntakeAlreadyProcessed
	}
	return nil
}

// ErrIntakeAlreadyProcessed signals that the intake was not in 'accepted' state when
// the worker tried to flip it — another attempt already processed it. The caller
// rolls back its persist tx (no duplicate message/event) and treats it as done.
var ErrIntakeAlreadyProcessed = errors.New("inbound intake already processed")

// ErrRecipientGone signals that the recipient no longer resolves to an agent (deleted
// between accept and processing). It is NOT a transient error — the async worker
// marks the intake terminally (so it doesn't linger 'accepted' forever) and the sync
// path skips the recipient. Distinct sentinel so callers don't retry.
var ErrRecipientGone = errors.New("recipient no longer resolves to an agent")

// MarkInboundIntakeFailed marks an intake terminally failed (unparseable body /
// exhausted retries) with a diagnostic detail. Own transaction — a terminal record
// for ops visibility; the message is dropped (we already 250'd).
func (s *Store) MarkInboundIntakeFailed(ctx context.Context, intakeID, detail string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE inbound_intake SET status = 'failed', detail = $2, processed_at = now() WHERE id = $1`,
		intakeID, detail)
	return err
}

// PruneProcessedIntake deletes processed intake rows whose terminal time is older
// than olderThan. Safe because the raw MIME also lives in messages.raw_message once
// processed; failed rows are deliberately retained for inspection. Returns the count
// pruned. Called by the inbound retention periodic (QueueMaintenance).
func (s *Store) PruneProcessedIntake(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM inbound_intake WHERE status = 'processed' AND processed_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

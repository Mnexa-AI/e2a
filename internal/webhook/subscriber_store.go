package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SubscriberDelivery is one row in webhook_subscriber_deliveries.
// Distinct from the legacy Delivery struct (which is keyed by
// message_id and tracks legacy single-URL delivery state).
type SubscriberDelivery struct {
	ID             string
	WebhookID      string
	EventType      string
	EventPayload   []byte // pre-marshalled envelope bytes; POSTed verbatim
	MessageID      *string
	Status         string // pending | delivered | failed
	Attempts       int
	MaxAttempts    int
	LastError      string
	LastStatusCode *int
	LastAttemptAt  *time.Time
	NextRetryAt    time.Time
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// SubscriberStore manages webhook_subscriber_deliveries. Parallel to
// the legacy DeliveryStore (which manages webhook_deliveries).
type SubscriberStore struct {
	pool *pgxpool.Pool
}

func NewSubscriberStore(pool *pgxpool.Pool) *SubscriberStore {
	return &SubscriberStore{pool: pool}
}

// GetPending pulls up to limit rows whose next_retry_at is in the
// past, ordered by next_retry_at ASC (oldest-due first). Caller is
// responsible for processing them and updating status — no row-level
// lease here; the worker's per-webhook inflight cap is what prevents
// double-processing.
func (s *SubscriberStore) GetPending(ctx context.Context, limit int) ([]SubscriberDelivery, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, webhook_id, event_type, event_payload, message_id,
		        status, attempts, max_attempts, last_error,
		        last_status_code, last_attempt_at, next_retry_at,
		        created_at, expires_at
		 FROM webhook_subscriber_deliveries
		 WHERE status = 'pending' AND next_retry_at <= now()
		 ORDER BY next_retry_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubscriberDelivery
	for rows.Next() {
		var d SubscriberDelivery
		if err := rows.Scan(
			&d.ID, &d.WebhookID, &d.EventType, &d.EventPayload, &d.MessageID,
			&d.Status, &d.Attempts, &d.MaxAttempts, &d.LastError,
			&d.LastStatusCode, &d.LastAttemptAt, &d.NextRetryAt,
			&d.CreatedAt, &d.ExpiresAt,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkDelivered transitions a row to status='delivered' and stamps
// last_attempt_at + last_status_code. Also bumps webhooks.last_delivered_at
// in the same transaction so list views show the freshest activity.
func (s *SubscriberStore) MarkDelivered(ctx context.Context, deliveryID string, statusCode int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var webhookID string
	if err := tx.QueryRow(ctx,
		`UPDATE webhook_subscriber_deliveries
		 SET status = 'delivered',
		     last_attempt_at = now(),
		     last_status_code = $2,
		     attempts = attempts + 1
		 WHERE id = $1
		 RETURNING webhook_id`,
		deliveryID, statusCode,
	).Scan(&webhookID); err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE webhooks SET last_delivered_at = now() WHERE id = $1`,
		webhookID,
	); err != nil {
		return fmt.Errorf("bump last_delivered_at: %w", err)
	}
	return tx.Commit(ctx)
}

// RecordAttemptFailure records a failed attempt, sets the next retry
// time, and decides whether to keep the row pending (more attempts
// remain) or transition to 'failed' (exhausted).
//
// statusCode is 0 when the failure was a connection error / timeout
// (no HTTP response received).
func (s *SubscriberStore) RecordAttemptFailure(ctx context.Context, deliveryID, errMsg string, statusCode int) error {
	// First, decide: is this attempt the final one?
	var attempts, maxAttempts int
	err := s.pool.QueryRow(ctx,
		`SELECT attempts, max_attempts FROM webhook_subscriber_deliveries WHERE id = $1`,
		deliveryID,
	).Scan(&attempts, &maxAttempts)
	if err != nil {
		return err
	}
	newAttempts := attempts + 1

	if newAttempts >= maxAttempts {
		_, err = s.pool.Exec(ctx,
			`UPDATE webhook_subscriber_deliveries
			 SET status = 'failed',
			     attempts = $2,
			     last_attempt_at = now(),
			     last_error = $3,
			     last_status_code = $4
			 WHERE id = $1`,
			deliveryID, newAttempts, errMsg, statusCode,
		)
		return err
	}

	nextRetry, ok := nextRetryAt(newAttempts)
	if !ok {
		// Defensive: nextRetryAt only fails if attempts exceeds the
		// backoff slice. The branch above should have caught it.
		nextRetry = time.Now().Add(1 * time.Hour)
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE webhook_subscriber_deliveries
		 SET attempts = $2,
		     last_attempt_at = now(),
		     last_error = $3,
		     last_status_code = $4,
		     next_retry_at = $5
		 WHERE id = $1`,
		deliveryID, newAttempts, errMsg, statusCode, nextRetry,
	)
	return err
}

// generateDeliveryID returns a prefixed id of the form wdl_<32-hex>.
// 16 bytes of entropy is more than enough — the row is short-lived
// (30-day expiry), and the prefix follows the rest of the e2a id
// scheme so logs and dashboards can spot a delivery id at a glance.
func generateDeliveryID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("webhook: crypto/rand failed: %v", err))
	}
	return "wdl_" + hex.EncodeToString(b)
}

// InsertPendingForTest creates a single delivery row tied to the given
// webhook + event type with the supplied envelope bytes. The retry
// worker picks it up on the next tick. Used by the
// POST /api/v1/webhooks/{id}/test endpoint to schedule a one-off
// delivery without going through the publisher's filter-matching path.
func (s *SubscriberStore) InsertPendingForTest(ctx context.Context, webhookID, eventType string, envelope []byte) (string, error) {
	id := generateDeliveryID()
	_, err := s.pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries
		     (id, webhook_id, event_type, event_payload, status, next_retry_at)
		 VALUES ($1, $2, $3, $4, 'pending', now())`,
		id, webhookID, eventType, envelope,
	)
	if err != nil {
		return "", fmt.Errorf("insert test delivery: %w", err)
	}
	return id, nil
}

// ListDeliveriesByWebhook returns up to `limit` delivery rows for the
// webhook, most-recent first. When status is non-empty, restricts to
// that status (pending|delivered|failed). Limit is bounded by the
// caller; this method does not enforce a cap.
func (s *SubscriberStore) ListDeliveriesByWebhook(ctx context.Context, webhookID, status string, limit int) ([]SubscriberDelivery, error) {
	var (
		rowsErr error
		out     []SubscriberDelivery
	)
	q := `SELECT id, webhook_id, event_type, event_payload, message_id,
	             status, attempts, max_attempts, last_error,
	             last_status_code, last_attempt_at, next_retry_at,
	             created_at, expires_at
	      FROM webhook_subscriber_deliveries
	      WHERE webhook_id = $1`
	args := []interface{}{webhookID}
	if status != "" {
		q += ` AND status = $2`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", len(args)+1)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d SubscriberDelivery
		if err := rows.Scan(
			&d.ID, &d.WebhookID, &d.EventType, &d.EventPayload, &d.MessageID,
			&d.Status, &d.Attempts, &d.MaxAttempts, &d.LastError,
			&d.LastStatusCode, &d.LastAttemptAt, &d.NextRetryAt,
			&d.CreatedAt, &d.ExpiresAt,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	rowsErr = rows.Err()
	return out, rowsErr
}

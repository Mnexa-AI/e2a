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

// GetPending leases up to `limit` rows whose next_retry_at is in the
// past. The lease is implemented as a single CTE that selects-for-update
// the candidate rows with SKIP LOCKED and pushes their next_retry_at
// forward by LeaseDuration, so concurrent workers (in-process OR across
// replicas) never return the same row. Mirrors the legacy
// DeliveryStore.GetPendingDeliveries pattern.
//
// The lease window is what prevents double-POST in multi-replica
// deployments. If a worker crashes mid-attempt, the row reappears once
// the lease expires; the caller writing MarkDelivered /
// RecordAttemptFailure inside the lease window overwrites next_retry_at
// to the real schedule (or 'failed' status) before the lease expires.
func (s *SubscriberStore) GetPending(ctx context.Context, limit int) ([]SubscriberDelivery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`WITH candidates AS (
		    SELECT id FROM webhook_subscriber_deliveries
		    WHERE status = 'pending' AND next_retry_at <= now()
		    ORDER BY next_retry_at ASC
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED
		 )
		 UPDATE webhook_subscriber_deliveries d
		 SET next_retry_at = now() + ($2 * interval '1 second')
		 FROM candidates c
		 WHERE d.id = c.id
		 RETURNING d.id, d.webhook_id, d.event_type, d.event_payload, d.message_id,
		           d.status, d.attempts, d.max_attempts, d.last_error,
		           d.last_status_code, d.last_attempt_at, d.next_retry_at,
		           d.created_at, d.expires_at`,
		limit, int(LeaseDuration.Seconds()),
	)
	if err != nil {
		return nil, err
	}

	var out []SubscriberDelivery
	for rows.Next() {
		var d SubscriberDelivery
		if err := rows.Scan(
			&d.ID, &d.WebhookID, &d.EventType, &d.EventPayload, &d.MessageID,
			&d.Status, &d.Attempts, &d.MaxAttempts, &d.LastError,
			&d.LastStatusCode, &d.LastAttemptAt, &d.NextRetryAt,
			&d.CreatedAt, &d.ExpiresAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
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
//
// SELECT and UPDATE run inside a single transaction with FOR UPDATE so
// two workers can't race on the same row (which could happen if the
// GetPending lease expires while an attempt is in flight). Without the
// row lock, the read-then-write pattern under-counts attempts on
// concurrent failure recording.
func (s *SubscriberStore) RecordAttemptFailure(ctx context.Context, deliveryID, errMsg string, statusCode int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var attempts, maxAttempts int
	if err := tx.QueryRow(ctx,
		`SELECT attempts, max_attempts FROM webhook_subscriber_deliveries
		 WHERE id = $1 FOR UPDATE`,
		deliveryID,
	).Scan(&attempts, &maxAttempts); err != nil {
		return err
	}
	newAttempts := attempts + 1

	if newAttempts >= maxAttempts {
		if _, err := tx.Exec(ctx,
			`UPDATE webhook_subscriber_deliveries
			 SET status = 'failed',
			     attempts = $2,
			     last_attempt_at = now(),
			     last_error = $3,
			     last_status_code = $4
			 WHERE id = $1`,
			deliveryID, newAttempts, errMsg, statusCode,
		); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	nextRetry, ok := nextRetryAt(newAttempts)
	if !ok {
		// Defensive: nextRetryAt only fails if attempts exceeds the
		// backoff slice. The branch above should have caught it.
		nextRetry = time.Now().Add(1 * time.Hour)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE webhook_subscriber_deliveries
		 SET attempts = $2,
		     last_attempt_at = now(),
		     last_error = $3,
		     last_status_code = $4,
		     next_retry_at = $5
		 WHERE id = $1`,
		deliveryID, newAttempts, errMsg, statusCode, nextRetry,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// generateDeliveryID returns a prefixed id of the form whd_<32-hex>.
// 16 bytes of entropy is more than enough — the row is short-lived
// (30-day expiry), and the prefix follows the rest of the e2a id
// scheme so logs and dashboards can spot a delivery id at a glance.
// Matches the publisher's inline generator at webhookpub/publisher.go
// so customers see one consistent prefix regardless of which path
// created the row (publisher fan-out vs. /test endpoint).
func generateDeliveryID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("webhook: crypto/rand failed: %v", err))
	}
	return "whd_" + hex.EncodeToString(b)
}

// InsertPendingForTest creates a single delivery row tied to the given
// webhook + event type with the supplied envelope bytes. The retry
// worker picks it up on the next tick. Used by the
// POST /v1/webhooks/{id}/test endpoint to schedule a one-off
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

// BumpNextRetry pushes the row's next_retry_at out by `after` so the
// row doesn't reappear in GetPending on every tick. Used by the worker
// when it skips a row without attempting delivery (e.g. webhook is
// disabled, waiting for re-enable). Status stays 'pending' so the row
// resumes processing once the deferred time arrives.
func (s *SubscriberStore) BumpNextRetry(ctx context.Context, deliveryID string, after time.Duration) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE webhook_subscriber_deliveries
		 SET next_retry_at = now() + ($2 * interval '1 second')
		 WHERE id = $1`,
		deliveryID, int(after.Seconds()),
	)
	return err
}

// DeleteExpiredSubscriberDeliveries removes rows whose expires_at has
// passed. Migration 025 sets a 30-day TTL on every row; without this
// janitor the table grows monotonically and query plans degrade.
// Mirrors DeliveryStore.DeleteExpiredDeliveries for the legacy table.
func (s *SubscriberStore) DeleteExpiredSubscriberDeliveries(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM webhook_subscriber_deliveries WHERE expires_at <= now()`,
	)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
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

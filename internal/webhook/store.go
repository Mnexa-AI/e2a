package webhook

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const DeliveryTTL = 48 * time.Hour

type Delivery struct {
	AgentID       string     `json:"agent_id"`
	MessageID     string     `json:"message_id"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"max_attempts"`
	LastError     string     `json:"last_error"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	NextRetryAt   time.Time  `json:"next_retry_at"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
}

type DeliveryStore struct {
	pool *pgxpool.Pool
}

func NewDeliveryStore(pool *pgxpool.Pool) *DeliveryStore {
	return &DeliveryStore{pool: pool}
}

func (s *DeliveryStore) CreateDelivery(ctx context.Context, messageID string, lastError string) (*Delivery, error) {
	now := time.Now()

	d := &Delivery{
		MessageID:   messageID,
		Status:      "pending",
		Attempts:    0,
		MaxAttempts: 2,
		LastError:   lastError,
		NextRetryAt: now,
		CreatedAt:   now,
		ExpiresAt:   now.Add(DeliveryTTL),
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO webhook_deliveries (message_id, status, attempts, max_attempts, last_error, next_retry_at, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		d.MessageID, d.Status, d.Attempts, d.MaxAttempts, d.LastError, d.NextRetryAt, d.CreatedAt, d.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// LeaseDuration is how long a leased delivery is hidden from other workers.
// On a clean delivery the row's next_retry_at is overwritten by the success
// path (MarkDelivered) or a real backoff (RecordFailure). The lease only
// matters as a recovery mechanism when a worker dies mid-delivery — after
// LeaseDuration the row becomes eligible again and another worker picks it
// up. Long enough that a legitimate slow webhook won't be double-fired,
// short enough that a crashed worker doesn't strand its rows for hours.
const LeaseDuration = 5 * time.Minute

// GetPendingDeliveries atomically claims up to `limit` due deliveries.
// Each returned row's next_retry_at is pushed by LeaseDuration so other
// workers (in this process or a different replica) won't grab the same
// row. The standard `WHERE status='pending' AND next_retry_at <= now()`
// filter then naturally excludes leased rows.
//
// This must run inside a transaction: `FOR UPDATE SKIP LOCKED` only
// holds the row lock for the lifetime of the surrounding transaction.
// pool.Query (autocommit) would release the lock as soon as the SELECT
// completed, leaving a window where two callers could each return the
// same row.
func (s *DeliveryStore) GetPendingDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Single CTE: select+lock the candidate rows, then update their
	// next_retry_at in the same statement so the lease is committed
	// atomically with the read. RETURNING gives us the same shape the
	// caller previously got from the plain SELECT.
	rows, err := tx.Query(ctx,
		`WITH candidates AS (
		    SELECT wd.message_id
		    FROM webhook_deliveries wd
		    WHERE wd.status = 'pending' AND wd.next_retry_at <= now() AND wd.expires_at > now()
		    ORDER BY wd.next_retry_at
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED
		 )
		 UPDATE webhook_deliveries wd
		 SET next_retry_at = now() + ($2 * interval '1 second')
		 FROM candidates c
		 JOIN messages m ON m.id = c.message_id
		 WHERE wd.message_id = c.message_id
		 RETURNING m.agent_id, wd.message_id, wd.status, wd.attempts, wd.max_attempts,
		           wd.last_error, wd.last_attempt_at, wd.next_retry_at, wd.created_at, wd.expires_at`,
		limit, int(LeaseDuration.Seconds()),
	)
	if err != nil {
		return nil, err
	}

	var deliveries []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.AgentID, &d.MessageID, &d.Status, &d.Attempts, &d.MaxAttempts, &d.LastError, &d.LastAttemptAt, &d.NextRetryAt, &d.CreatedAt, &d.ExpiresAt); err != nil {
			rows.Close()
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return deliveries, nil
}

func (s *DeliveryStore) MarkDelivered(ctx context.Context, messageID string) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx,
		`UPDATE webhook_deliveries SET status = 'delivered', last_error = '', last_attempt_at = $2, attempts = attempts + 1 WHERE message_id = $1`,
		messageID, now,
	)
	return err
}

func (s *DeliveryStore) MarkAttemptFailed(ctx context.Context, messageID, errMsg string, nextRetry time.Time) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx,
		`UPDATE webhook_deliveries SET attempts = attempts + 1, last_error = $2, last_attempt_at = $3, next_retry_at = $4 WHERE message_id = $1`,
		messageID, errMsg, now, nextRetry,
	)
	return err
}

func (s *DeliveryStore) MarkFailed(ctx context.Context, messageID, errMsg string) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx,
		`UPDATE webhook_deliveries SET status = 'failed', last_error = $2, last_attempt_at = $3, attempts = attempts + 1 WHERE message_id = $1`,
		messageID, errMsg, now,
	)
	return err
}

func (s *DeliveryStore) DeleteExpiredDeliveries(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhook_deliveries WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

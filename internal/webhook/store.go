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

func (s *DeliveryStore) GetPendingDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m.agent_id, wd.message_id, wd.status, wd.attempts, wd.max_attempts, wd.last_error, wd.last_attempt_at, wd.next_retry_at, wd.created_at, wd.expires_at
		 FROM webhook_deliveries wd
		 JOIN messages m ON m.id = wd.message_id
		 WHERE wd.status = 'pending' AND wd.next_retry_at <= now() AND wd.expires_at > now()
		 ORDER BY wd.next_retry_at
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.AgentID, &d.MessageID, &d.Status, &d.Attempts, &d.MaxAttempts, &d.LastError, &d.LastAttemptAt, &d.NextRetryAt, &d.CreatedAt, &d.ExpiresAt); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
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

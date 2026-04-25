package usage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type UsageEvent struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	AgentID   string    `json:"agent_id"`
	Domain    string    `json:"domain"`
	Direction string    `json:"direction"`
	EventType string    `json:"event_type"`
	CreatedAt time.Time `json:"created_at"`
}

type UsageSummary struct {
	UserID        string `json:"user_id"`
	BucketDate    string `json:"bucket_date"`
	InboundCount  int    `json:"inbound_count"`
	OutboundCount int    `json:"outbound_count"`
	TotalCount    int    `json:"total_count"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) RecordUsageEvent(ctx context.Context, event *UsageEvent) error {
	if event.ID == "" {
		event.ID = generateBillingID()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if event.EventType == "" {
		event.EventType = "message"
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO usage_events (id, user_id, agent_id, domain, direction, event_type, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		event.ID, event.UserID, event.AgentID, event.Domain, event.Direction, event.EventType, event.CreatedAt,
	)
	return err
}

func (s *Store) GetUsageSummary(ctx context.Context, userID, bucketDate string) (*UsageSummary, error) {
	sum := &UsageSummary{}
	var bucketTime time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, bucket_date, inbound_count, outbound_count, total_count
		 FROM usage_summaries WHERE user_id = $1 AND bucket_date = $2`, userID, bucketDate,
	).Scan(&sum.UserID, &bucketTime, &sum.InboundCount, &sum.OutboundCount, &sum.TotalCount)
	if err != nil {
		return nil, err
	}
	sum.BucketDate = bucketTime.Format("2006-01-02")
	return sum, nil
}

func (s *Store) IncrementUsageSummary(ctx context.Context, userID, bucketDate, direction string) error {
	inbound, outbound := 0, 0
	if direction == "inbound" {
		inbound = 1
	} else {
		outbound = 1
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO usage_summaries (user_id, bucket_date, inbound_count, outbound_count, total_count)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (user_id, bucket_date) DO UPDATE SET
		   inbound_count = usage_summaries.inbound_count + $3,
		   outbound_count = usage_summaries.outbound_count + $4,
		   total_count = usage_summaries.total_count + $5`,
		userID, bucketDate, inbound, outbound, inbound+outbound,
	)
	return err
}

func (s *Store) CountAgentsByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_identities WHERE user_id = $1`, userID,
	).Scan(&count)
	return count, err
}

func generateBillingID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure means the OS RNG is broken — propagating
		// would force every caller to handle an error that effectively
		// can't happen on a healthy system. Panic so the failure is
		// visible immediately rather than silently producing colliding
		// all-zero IDs.
		panic(fmt.Sprintf("billing: crypto/rand failed: %v", err))
	}
	return fmt.Sprintf("ue_%s", hex.EncodeToString(b))
}

// CurrentDate returns today's date as a string in YYYY-MM-DD format.
func CurrentDate() string {
	return time.Now().UTC().Format("2006-01-02")
}

package usage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

// GetAccountClass returns the account class for a user. A missing user (no row)
// resolves to ClassStandard so the metering gate fails toward metering — a
// real customer must never be silently exempted from billing because of a
// transient lookup miss. The PK lookup on users is cheap; account class is read
// once per metered message.
func (s *Store) GetAccountClass(ctx context.Context, userID string) (AccountClass, error) {
	var class string
	err := s.pool.QueryRow(ctx,
		`SELECT account_class FROM users WHERE id = $1`, userID,
	).Scan(&class)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ClassStandard, nil
		}
		return ClassStandard, err
	}
	return AccountClass(class), nil
}

// CountAgentsByUser returns the number of ACTIVE agents owned by the user —
// active meaning the agent's domain row still exists. The INNER JOIN on
// domains mirrors identity.Store.ListAgentsByUser EXACTLY (same
// `agent_identities a JOIN domains d ON a.domain = d.domain WHERE a.user_id`
// predicate), so this count is guaranteed to equal the length of the
// /v1/agents list. Without the join an orphaned agent (one whose domain row
// is gone) would inflate the count above the list length, silently consuming
// a plan slot and letting usage.agents exceed max_agents while the agent is
// invisible to the user. Both consumers — the account usage view
// (usage.Agents) and the max_agents cap in limits.DBEnforcer.CheckAgentCreate
// — therefore count only agents the user can actually see and manage, so an
// orphaned agent neither shows up as usage nor blocks creating a new one.
func (s *Store) CountAgentsByUser(ctx context.Context, userID string) (int, error) {
	// deleted_at IS NULL mirrors ListAgentsByUser's trash exclusion
	// (migration 063): a soft-deleted agent is invisible to the user, so it
	// must neither show up as usage nor consume a max_agents slot — the user
	// can always create a replacement while the old inbox sits in the trash.
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*)
		   FROM agent_identities a
		   JOIN domains d ON a.domain = d.domain
		  WHERE a.user_id = $1 AND a.deleted_at IS NULL`, userID,
	).Scan(&count)
	return count, err
}

// CountDomainsByUser returns the number of domains owned by the user.
// Used by the limits enforcer to check max_domains caps. Counts every
// row in domains regardless of verification status; an unverified
// domain still consumes a slot until the user deletes it.
func (s *Store) CountDomainsByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM domains WHERE user_id = $1`, userID,
	).Scan(&count)
	return count, err
}

// MessagesThisMonth returns the user's inbound+outbound message count
// for the current UTC calendar month, summed from usage_summaries.
// Returns 0 with no error if the user has no rows yet. The reference is
// time.Now().UTC() so server clocks crossing midnight UTC roll the
// counter consistently with the daily bucket_date written by
// IncrementUsageSummary.
func (s *Store) MessagesThisMonth(ctx context.Context, userID string) (int, error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(total_count), 0)
		   FROM usage_summaries
		  WHERE user_id = $1 AND bucket_date >= $2`,
		userID, monthStart,
	).Scan(&count)
	return count, err
}

// GetStorageBytes returns the user's current materialized storage bytes
// from account_usage. Returns 0 with no error if the user has no row
// yet — the trigger in migration 016 lazily creates the row on first
// message insert, so a pre-message user legitimately has 0 storage.
func (s *Store) GetStorageBytes(ctx context.Context, userID string) (int64, error) {
	var bytes int64
	err := s.pool.QueryRow(ctx,
		`SELECT storage_bytes FROM account_usage WHERE user_id = $1`, userID,
	).Scan(&bytes)
	if err != nil {
		// No row yet → 0 bytes. The trigger creates rows lazily on
		// first message insert, so a pre-message user legitimately has
		// 0 storage and should not see a synthetic error on first
		// dashboard load.
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return bytes, nil
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

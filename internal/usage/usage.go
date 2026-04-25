package usage

import (
	"context"
	"log"
)

// UsageTracker records usage events. Always allows the action (no quota enforcement).
type UsageTracker interface {
	// RecordAndCheck records a usage event. Always returns true.
	RecordAndCheck(ctx context.Context, userID, agentID, domain, direction string) (allowed bool, err error)
}

// LiveUsageTracker is the real implementation backed by the billing store.
type LiveUsageTracker struct {
	store *Store
}

func NewUsageTracker(store *Store) *LiveUsageTracker {
	return &LiveUsageTracker{store: store}
}

func (t *LiveUsageTracker) RecordAndCheck(ctx context.Context, userID, agentID, domain, direction string) (bool, error) {
	// Record the event
	event := &UsageEvent{
		UserID:    userID,
		AgentID:   agentID,
		Domain:    domain,
		Direction: direction,
	}
	if err := t.store.RecordUsageEvent(ctx, event); err != nil {
		log.Printf("[billing] failed to record usage event: %v", err)
		// Don't block on recording failure
	}

	bucketDate := CurrentDate()
	if err := t.store.IncrementUsageSummary(ctx, userID, bucketDate, direction); err != nil {
		log.Printf("[billing] failed to increment usage summary: %v", err)
	}

	return true, nil
}

// NoopUsageTracker always allows everything. Used when billing is disabled.
type NoopUsageTracker struct{}

func NewNoopUsageTracker() *NoopUsageTracker {
	return &NoopUsageTracker{}
}

func (t *NoopUsageTracker) RecordAndCheck(ctx context.Context, userID, agentID, domain, direction string) (bool, error) {
	return true, nil
}

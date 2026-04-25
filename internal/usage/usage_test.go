package usage_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// -- NoopUsageTracker tests (no DB needed) --

func TestNoopUsageTracker_AlwaysAllows(t *testing.T) {
	tracker := usage.NewNoopUsageTracker()
	ctx := context.Background()

	allowed, err := tracker.RecordAndCheck(ctx, "user1", "agent1", "test.com", "outbound")
	if err != nil || !allowed {
		t.Errorf("RecordAndCheck: allowed=%v, err=%v", allowed, err)
	}
}

// -- Store tests (DB required) --

func TestUsageSummaryIncrementAndGet(t *testing.T) {
	pool := testutil.TestDB(t)
	store := usage.NewStore(pool)
	idStore := identity.NewStore(pool)

	ctx := context.Background()
	bucketDate := "2026-03-29"

	// Create a real user to satisfy FK constraint
	user, err := idStore.CreateOrGetUser(ctx, "billing-test@example.com", "Billing Test", "google-billing-test")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// Increment inbound
	if err := store.IncrementUsageSummary(ctx, user.ID, bucketDate, "inbound"); err != nil {
		t.Fatalf("IncrementUsageSummary inbound: %v", err)
	}
	// Increment outbound twice
	for i := 0; i < 2; i++ {
		if err := store.IncrementUsageSummary(ctx, user.ID, bucketDate, "outbound"); err != nil {
			t.Fatalf("IncrementUsageSummary outbound: %v", err)
		}
	}

	sum, err := store.GetUsageSummary(ctx, user.ID, bucketDate)
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}
	if sum.InboundCount != 1 {
		t.Errorf("InboundCount = %d, want 1", sum.InboundCount)
	}
	if sum.OutboundCount != 2 {
		t.Errorf("OutboundCount = %d, want 2", sum.OutboundCount)
	}
	if sum.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", sum.TotalCount)
	}
}

func TestLiveUsageTracker_AlwaysAllows(t *testing.T) {
	pool := testutil.TestDB(t)
	store := usage.NewStore(pool)
	idStore := identity.NewStore(pool)

	tracker := usage.NewUsageTracker(store)
	ctx := context.Background()

	// Create a real user to satisfy FK constraint
	user, err := idStore.CreateOrGetUser(ctx, "billing-tracker@example.com", "Billing Tracker", "google-billing-tracker")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// Inbound always allowed
	allowed, err := tracker.RecordAndCheck(ctx, user.ID, "agent1", "test.com", "inbound")
	if err != nil || !allowed {
		t.Errorf("inbound should always be allowed: allowed=%v, err=%v", allowed, err)
	}

	// Outbound always allowed (no quota enforcement)
	allowed, err = tracker.RecordAndCheck(ctx, user.ID, "agent1", "test.com", "outbound")
	if err != nil || !allowed {
		t.Errorf("outbound should always be allowed: allowed=%v, err=%v", allowed, err)
	}
}

func TestCurrentDate(t *testing.T) {
	date := usage.CurrentDate()
	if len(date) != 10 { // "2026-03-29" format
		t.Errorf("CurrentDate() = %q, expected YYYY-MM-DD format", date)
	}
	if date[4] != '-' || date[7] != '-' {
		t.Errorf("CurrentDate() = %q, missing dash separators", date)
	}
}

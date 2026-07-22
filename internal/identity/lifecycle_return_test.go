package identity_test

import (
	"context"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
	"github.com/tokencanopy/e2a/internal/testutil"
)

func TestCreatePendingReturnsExactPersistedHoldTransition(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	user, err := store.CreateOrGetUser(ctx, "lifecycle-return@example.test", "Owner", "lifecycle-return")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "example.test", user.ID); err != nil {
		t.Fatal(err)
	}
	agent, err := store.CreateAgent(ctx, "bot@example.test", "example.test", "", "", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID, []string{"alice@example.net"}, nil, nil, "review me", "body", "", nil, "send", "", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.LifecycleTransitions) != 1 || msg.LifecycleTransitions[0].ReasonCode != messagelifecycle.ReasonReviewHoldCreated {
		t.Fatalf("returned lifecycle = %+v, want review hold", msg.LifecycleTransitions)
	}
	var persistedID string
	if err := pool.QueryRow(ctx, `SELECT id FROM message_lifecycle_transitions WHERE message_id=$1 AND reason_code=$2`, msg.ID, messagelifecycle.ReasonReviewHoldCreated).Scan(&persistedID); err != nil {
		t.Fatal(err)
	}
	if msg.LifecycleTransitions[0].ID != persistedID {
		t.Fatalf("returned transition id = %q, persisted = %q", msg.LifecycleTransitions[0].ID, persistedID)
	}
}

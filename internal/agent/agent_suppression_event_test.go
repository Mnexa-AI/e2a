package agent_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/eventpayload"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

type suppressionEventOutbox struct {
	tx pgx.Tx
	e  webhookpub.Event
}

func TestAgentSuppressionAddedHookEmitsOnceForIdempotentInsert(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "suppression-event@example.test", "Owner", "suppression-event")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "suppression-event.example.test", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyDomain(ctx, "suppression-event.example.test", user.ID); err != nil {
		t.Fatal(err)
	}
	ag, err := store.CreateAgent(ctx, "sender@suppression-event.example.test", "suppression-event.example.test", "", "", "local", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	hook := agent.AgentSuppressionAddedHook(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	for i := 0; i < 2; i++ {
		if _, _, err := store.AddAgentSuppression(ctx, user.ID, ag.ID, "person@example.test", "", "manual", hook); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE user_id=$1 AND type=$2`, user.ID, webhookpub.EventAgentSuppressionAdded).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("agent.suppression_added events = %d, want 1", count)
	}
}

func (o *suppressionEventOutbox) Enabled() bool { return true }
func (o *suppressionEventOutbox) PublishTx(_ context.Context, tx pgx.Tx, e webhookpub.Event) error {
	o.tx, o.e = tx, e
	return nil
}
func (o *suppressionEventOutbox) PublishBestEffortTx(context.Context, pgx.Tx, webhookpub.Event) bool {
	return false
}
func (o *suppressionEventOutbox) DeleteExpiredWebhookEvents(context.Context) (int, error) {
	return 0, nil
}
func (o *suppressionEventOutbox) SetFanOutEnqueuer(webhookpub.FanOutEnqueuer) {}

func TestAgentSuppressionAddedHookPublishesExactScopedPayload(t *testing.T) {
	outbox := &suppressionEventOutbox{}
	hook := agent.AgentSuppressionAddedHook(outbox)
	scope := identity.AgentSuppressionHookScope{UserID: "user_1", AgentID: "sender@agents.test", Address: "person@example.test", Source: "unsubscribe"}
	if err := hook(context.Background(), nil, scope); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if outbox.e.Type != webhookpub.EventAgentSuppressionAdded || outbox.e.UserID != scope.UserID || outbox.e.AgentID != scope.AgentID {
		t.Fatalf("event routing = %+v", outbox.e)
	}
	want := eventpayload.AgentSuppressionAddedData{AgentEmail: scope.AgentID, Address: scope.Address, Source: scope.Source}
	if !reflect.DeepEqual(outbox.e.Data, want) {
		t.Fatalf("event data = %#v, want %#v", outbox.e.Data, want)
	}
}

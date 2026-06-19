package webhookpub_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedWebhookFixture creates the full chain (user, domain, agent,
// webhook) needed to test slice-2 fan-out from a webhook_events row
// into webhook_subscriber_deliveries.
func seedWebhookFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *identity.Store, slug string) (userID, agentEmail, webhookID string) {
	t.Helper()
	userID = "u_" + slug
	domain := slug + ".example.com"
	agentEmail = "agent@" + domain

	_, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, name, google_subject, created_at)
		 VALUES ($1, $2, 'Worker Test', $1, now())
		 ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@example.com")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO domains (domain, user_id, verified, verification_token, created_at)
		 VALUES ($1, $2, true, 'tkn', now())
		 ON CONFLICT (domain) DO NOTHING`,
		domain, userID); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "Worker Test Agent", "https://test.example.com/wh", "cloud", userID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	created, err := store.CreateWebhook(ctx, userID,
		"https://test.example.com/webhook", "test",
		[]string{webhookpub.EventEmailReceived},
		identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	return userID, agentEmail, created.ID
}

func TestOutboxWorker_Integration_FanOutDeliveries(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	userID, _, webhookID := seedWebhookFixture(t, ctx, pool, store, "wkr_fanout")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_subscriber_deliveries WHERE webhook_id = $1`, webhookID)
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM webhooks WHERE id = $1`, webhookID)
		_, _ = pool.Exec(ctx, `DELETE FROM agent_identities WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM domains WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// Seed a pending webhook_events row.
	eventID := webhookpub.DeterministicEventID("msg_worker_1", webhookpub.EventEmailReceived)
	envelope := webhookpub.Envelope{
		Type:      webhookpub.EventEmailReceived,
		ID:        eventID,
		CreatedAt: time.Now().UTC(),
		Data:      map[string]any{"hello": "world"},
	}
	envBytes, _ := json.Marshal(envelope)
	_, err := pool.Exec(ctx,
		`INSERT INTO webhook_events (id, user_id, type, envelope, status)
		 VALUES ($1, $2, $3, $4, 'pending')`,
		eventID, userID, webhookpub.EventEmailReceived, envBytes)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}

	worker := webhookpub.NewOutboxWorker(pool, store)
	worker.Tick(ctx)

	// Verify the outbox row transitioned to 'processed' AND a
	// subscriber delivery row was created.
	var status string
	var matchedIDs []string
	if err := pool.QueryRow(ctx,
		`SELECT status, matched_webhook_ids FROM webhook_events WHERE id = $1`, eventID,
	).Scan(&status, &matchedIDs); err != nil {
		t.Fatalf("read event status: %v", err)
	}
	if status != "processed" {
		t.Errorf("status = %s, want processed", status)
	}
	if len(matchedIDs) != 1 || matchedIDs[0] != webhookID {
		t.Errorf("matched_webhook_ids = %v, want [%s]", matchedIDs, webhookID)
	}

	var deliveryCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_subscriber_deliveries WHERE event_id = $1 AND webhook_id = $2`,
		eventID, webhookID,
	).Scan(&deliveryCount); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveryCount != 1 {
		t.Errorf("delivery count = %d, want 1", deliveryCount)
	}
}

func TestOutboxWorker_Integration_NoMatchTransition(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	userID := "u_wkr_nomatch"
	_, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, name, google_subject, created_at)
		 VALUES ($1, $2, 'NM', $1, now())
		 ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@example.com")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// Seed an event with no enabled webhooks for this user.
	eventID := webhookpub.DeterministicEventID("msg_nomatch", webhookpub.EventEmailReceived)
	_, err = pool.Exec(ctx,
		`INSERT INTO webhook_events (id, user_id, type, envelope, status)
		 VALUES ($1, $2, $3, '{}'::jsonb, 'pending')`,
		eventID, userID, webhookpub.EventEmailReceived)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}

	worker := webhookpub.NewOutboxWorker(pool, store)
	worker.Tick(ctx)

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM webhook_events WHERE id = $1`, eventID,
	).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "no_match" {
		t.Errorf("status = %s, want no_match", status)
	}

	var deliveryCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_subscriber_deliveries WHERE event_id = $1`, eventID,
	).Scan(&deliveryCount); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveryCount != 0 {
		t.Errorf("delivery count = %d, want 0 (no_match)", deliveryCount)
	}
}

func TestOutboxWorker_Integration_LeaseSkipsAlreadyProcessed(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	userID, _, webhookID := seedWebhookFixture(t, ctx, pool, store, "wkr_lease")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_subscriber_deliveries WHERE webhook_id = $1`, webhookID)
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM webhooks WHERE id = $1`, webhookID)
		_, _ = pool.Exec(ctx, `DELETE FROM agent_identities WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM domains WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	eventID := webhookpub.DeterministicEventID("msg_lease", webhookpub.EventEmailReceived)
	_, err := pool.Exec(ctx,
		`INSERT INTO webhook_events (id, user_id, type, envelope, status)
		 VALUES ($1, $2, $3, '{}'::jsonb, 'pending')`,
		eventID, userID, webhookpub.EventEmailReceived)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}

	worker := webhookpub.NewOutboxWorker(pool, store)
	worker.Tick(ctx)
	// A second Tick should leave the row alone — the partial index on
	// pending rows means the worker doesn't even see processed rows.
	worker.Tick(ctx)

	var deliveryCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_subscriber_deliveries WHERE event_id = $1`, eventID,
	).Scan(&deliveryCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if deliveryCount != 1 {
		t.Errorf("delivery count after two Ticks = %d, want 1 (idempotent)", deliveryCount)
	}
}

func TestOutboxWorker_Integration_ConcurrentTickDeduplicates(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	userID, _, webhookID := seedWebhookFixture(t, ctx, pool, store, "wkr_concurrent")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_subscriber_deliveries WHERE webhook_id = $1`, webhookID)
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM webhooks WHERE id = $1`, webhookID)
		_, _ = pool.Exec(ctx, `DELETE FROM agent_identities WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM domains WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	eventID := webhookpub.DeterministicEventID("msg_concurrent", webhookpub.EventEmailReceived)
	_, err := pool.Exec(ctx,
		`INSERT INTO webhook_events (id, user_id, type, envelope, status)
		 VALUES ($1, $2, $3, '{}'::jsonb, 'pending')`,
		eventID, userID, webhookpub.EventEmailReceived)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}

	// Simulate two concurrent worker replicas. FOR UPDATE SKIP LOCKED
	// means one wins the lease and the other sees no rows.
	workerA := webhookpub.NewOutboxWorker(pool, store)
	workerB := webhookpub.NewOutboxWorker(pool, store)
	done := make(chan struct{}, 2)
	go func() { workerA.Tick(ctx); done <- struct{}{} }()
	go func() { workerB.Tick(ctx); done <- struct{}{} }()
	<-done
	<-done

	// Exactly one delivery row for this (event, webhook).
	var deliveryCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_subscriber_deliveries WHERE event_id = $1 AND webhook_id = $2`,
		eventID, webhookID,
	).Scan(&deliveryCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if deliveryCount != 1 {
		t.Errorf("concurrent Tick produced %d deliveries; want exactly 1 (lease + partial unique should dedupe)", deliveryCount)
	}
}

// poolBeginFunc is used only as a structural hint to consumers — the
// worker's internal helper isn't exported.
// fixture helpers only — no compile guards needed

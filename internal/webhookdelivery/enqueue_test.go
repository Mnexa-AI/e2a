package webhookdelivery_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/jobs"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookdelivery"
)

// TestEnqueueDelivery_StampsRiverJob is the stranding-fix regression test: the
// /test webhook endpoint and the event-redelivery API insert a
// webhook_subscriber_deliveries row directly (bypassing the outbox drain, which
// enqueues in-tx). Under River-as-sole-engine, that row only ever delivers if
// EnqueueDelivery enqueues a webhook_deliver job and stamps job_id. This test
// stands up a REAL River client and asserts, for a /test-style row and a
// redelivery-style row, that (a) a river_job of kind=webhook_deliver is created
// carrying the delivery id, and (b) the delivery row's job_id is stamped.
func TestEnqueueDelivery_StampsRiverJob(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("jobs.Migrate: %v", err)
	}

	store := identity.NewStore(pool)
	user, err := store.CreateOrGetUser(ctx, "owner-enq@example.com", "Owner", "google-enq")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	wh, err := store.CreateWebhook(ctx, user.ID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	sub := webhook.NewSubscriberStore(pool)

	// Build the Jobs integration on a real shared River client so EnqueueDelivery
	// inserts an actual river_job row.
	j := webhookdelivery.NewJobs(sub, fakeDeliverer{}, fakeWebhooks{wh: wh})
	client, err := jobs.New(pool, jobs.Config{}, j)
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	j.SetEnqueuer(client)

	assertEnqueued := func(t *testing.T, deliveryID string) {
		t.Helper()
		if err := j.EnqueueDelivery(ctx, pool, deliveryID); err != nil {
			t.Fatalf("EnqueueDelivery(%s): %v", deliveryID, err)
		}
		// (a) a webhook_deliver river_job carrying this delivery id exists.
		var jobCount int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM river_job WHERE kind = 'webhook_deliver' AND args->>'delivery_id' = $1`,
			deliveryID).Scan(&jobCount); err != nil {
			t.Fatalf("count river_job: %v", err)
		}
		if jobCount != 1 {
			t.Errorf("river_job kind=webhook_deliver for %s = %d, want 1", deliveryID, jobCount)
		}
		// (b) the delivery row's job_id is stamped.
		var jobID *int64
		if err := pool.QueryRow(ctx,
			`SELECT job_id FROM webhook_subscriber_deliveries WHERE id = $1`, deliveryID).Scan(&jobID); err != nil {
			t.Fatalf("read job_id: %v", err)
		}
		if jobID == nil {
			t.Errorf("delivery %s has no job_id after EnqueueDelivery", deliveryID)
		}
		// Idempotent: a re-enqueue (crash/retry) doesn't create a second job.
		if err := j.EnqueueDelivery(ctx, pool, deliveryID); err != nil {
			t.Fatalf("EnqueueDelivery re-run: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM river_job WHERE kind = 'webhook_deliver' AND args->>'delivery_id' = $1`,
			deliveryID).Scan(&jobCount); err != nil {
			t.Fatalf("count river_job (re-run): %v", err)
		}
		if jobCount != 1 {
			t.Errorf("after re-enqueue: river_job for %s = %d, want 1 (idempotent)", deliveryID, jobCount)
		}
	}

	// /test webhook endpoint path: InsertPendingForTest → EnqueueDelivery.
	t.Run("test_endpoint", func(t *testing.T) {
		id, err := sub.InsertPendingForTest(ctx, wh.ID, "email.received", []byte(`{"type":"email.received"}`))
		if err != nil {
			t.Fatalf("InsertPendingForTest: %v", err)
		}
		assertEnqueued(t, id)
	})

	// Redelivery API path: agent.InsertReplayDelivery → EnqueueDelivery.
	// (event_id is a logical link, no FK — see migration 028 — so a synthetic id
	// is fine here.)
	t.Run("redelivery", func(t *testing.T) {
		id, err := agent.InsertReplayDelivery(ctx, pool, "evt_enq_replay", wh.ID, "email.received", nil, []byte(`{"type":"email.received"}`))
		if err != nil {
			t.Fatalf("InsertReplayDelivery: %v", err)
		}
		assertEnqueued(t, id)
	})
}

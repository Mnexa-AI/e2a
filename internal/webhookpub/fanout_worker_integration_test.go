package webhookpub_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// fakeFanOutEnq is a jobs.Enqueuer that records the FanOutArgs it was asked to enqueue
// and hands back monotonic job ids — no real River client needed. Mirrors the
// inboundprocess reconcile fake.
type fakeFanOutEnq struct {
	mu   sync.Mutex
	n    int64
	args []webhookpub.FanOutArgs
}

func (f *fakeFanOutEnq) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return f.record(args)
}

func (f *fakeFanOutEnq) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return f.record(args)
}

func (f *fakeFanOutEnq) record(args river.JobArgs) (*rivertype.JobInsertResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if fa, ok := args.(webhookpub.FanOutArgs); ok {
		f.args = append(f.args, fa)
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: f.n}}, nil
}

// seedPendingEvent inserts a pending webhook_events row of the given type and returns
// its id. Mirrors the worker_integration_test seeding.
func seedPendingEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, msgKey, eventType string) string {
	t.Helper()
	eventID := webhookpub.DeterministicEventID(msgKey, eventType)
	env, _ := json.Marshal(webhookpub.Envelope{Type: eventType, ID: eventID, CreatedAt: time.Now().UTC()})
	if _, err := pool.Exec(ctx,
		`INSERT INTO webhook_events (id, user_id, type, envelope, status) VALUES ($1, $2, $3, $4, 'pending')`,
		eventID, userID, eventType, env); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return eventID
}

// cleanupWebhookFixture tears down the chain seeded by seedWebhookFixture plus any
// events/deliveries the fan-out tests created.
func cleanupWebhookFixture(ctx context.Context, pool *pgxpool.Pool, userID, webhookID string) {
	_, _ = pool.Exec(ctx, `DELETE FROM webhook_subscriber_deliveries WHERE webhook_id = $1`, webhookID)
	_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM webhooks WHERE id = $1`, webhookID)
	_, _ = pool.Exec(ctx, `DELETE FROM agent_identities WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM domains WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
}

// TestFanOutWorker_Integration_FansOutAndEnqueues drives the River FanOutWorker over a
// pending event: it inserts the Layer 2 delivery row, enqueues its delivery job in the
// same tx (job_id stamped), and marks the event 'processed'. A second Work on the now-
// processed event is a no-op (idempotent re-run) — no duplicate enqueue.
func TestFanOutWorker_Integration_FansOutAndEnqueues(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	userID, _, webhookID := seedWebhookFixture(t, ctx, pool, store, "fo_enq")
	t.Cleanup(func() { cleanupWebhookFixture(ctx, pool, userID, webhookID) })

	eventID := seedPendingEvent(t, ctx, pool, userID, "msg_fo_1", webhookpub.EventEmailReceived)

	enq := &fakeDeliveryEnqueuer{}
	w := webhookpub.NewFanOutWorker(pool, store, enq, nil)
	if err := w.Work(ctx, &river.Job[webhookpub.FanOutArgs]{Args: webhookpub.FanOutArgs{EventID: eventID}}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var deliveryID string
	var jobID *int64
	if err := pool.QueryRow(ctx,
		`SELECT id, job_id FROM webhook_subscriber_deliveries WHERE event_id = $1 AND webhook_id = $2`,
		eventID, webhookID,
	).Scan(&deliveryID, &jobID); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if len(enq.ids) != 1 || enq.ids[0] != deliveryID {
		t.Fatalf("enqueued ids = %v, want [%s]", enq.ids, deliveryID)
	}
	if jobID == nil || *jobID != 1 {
		t.Errorf("job_id = %v, want 1 (stamped from the enqueue)", jobID)
	}

	var status string
	var matched []string
	if err := pool.QueryRow(ctx,
		`SELECT status, matched_webhook_ids FROM webhook_events WHERE id = $1`, eventID,
	).Scan(&status, &matched); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if status != "processed" {
		t.Errorf("status = %q, want processed", status)
	}
	if len(matched) != 1 || matched[0] != webhookID {
		t.Errorf("matched_webhook_ids = %v, want [%s]", matched, webhookID)
	}

	// Idempotent re-run: the event is 'processed' now, so Work is a no-op — no second
	// enqueue, status unchanged.
	if err := w.Work(ctx, &river.Job[webhookpub.FanOutArgs]{Args: webhookpub.FanOutArgs{EventID: eventID}}); err != nil {
		t.Fatalf("Work (re-run): %v", err)
	}
	if len(enq.ids) != 1 {
		t.Errorf("after re-run, enqueue calls = %d, want 1 (idempotent)", len(enq.ids))
	}
}

// TestFanOutWorker_Integration_NoMatch: an event whose type no enabled webhook
// subscribes to transitions to 'no_match' with zero deliveries and zero enqueues.
func TestFanOutWorker_Integration_NoMatch(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	// The fixture's webhook subscribes to email.received only.
	userID, _, webhookID := seedWebhookFixture(t, ctx, pool, store, "fo_nomatch")
	t.Cleanup(func() { cleanupWebhookFixture(ctx, pool, userID, webhookID) })

	eventID := seedPendingEvent(t, ctx, pool, userID, "msg_fo_nm", webhookpub.EventEmailSent)

	enq := &fakeDeliveryEnqueuer{}
	w := webhookpub.NewFanOutWorker(pool, store, enq, nil)
	if err := w.Work(ctx, &river.Job[webhookpub.FanOutArgs]{Args: webhookpub.FanOutArgs{EventID: eventID}}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM webhook_events WHERE id = $1`, eventID).Scan(&status); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if status != "no_match" {
		t.Errorf("status = %q, want no_match", status)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM webhook_subscriber_deliveries WHERE event_id = $1`, eventID).Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if n != 0 {
		t.Errorf("deliveries = %d, want 0", n)
	}
	if len(enq.ids) != 0 {
		t.Errorf("enqueue calls = %d, want 0", len(enq.ids))
	}
}

// TestFanOutWorker_Integration_EventGoneReturnsNil: a job for an event that no longer
// exists (30d GC before fan-out) returns nil — nothing to do, not an error to retry.
func TestFanOutWorker_Integration_EventGoneReturnsNil(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	w := webhookpub.NewFanOutWorker(pool, store, &fakeDeliveryEnqueuer{}, nil)
	err := w.Work(ctx, &river.Job[webhookpub.FanOutArgs]{Args: webhookpub.FanOutArgs{EventID: "evt_does_not_exist"}})
	if err != nil {
		t.Errorf("Work on missing event = %v, want nil", err)
	}
}

// TestFanOutJobs_Integration_ReconcilePending: a pending event with no fan-out job is
// re-enqueued and its fanout_job_id stamped; a re-run does not double-enqueue it.
func TestFanOutJobs_Integration_ReconcilePending(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	userID, _, webhookID := seedWebhookFixture(t, ctx, pool, store, "fo_recon")
	t.Cleanup(func() { cleanupWebhookFixture(ctx, pool, userID, webhookID) })

	eventID := seedPendingEvent(t, ctx, pool, userID, "msg_fo_rc", webhookpub.EventEmailReceived)

	enq := &fakeFanOutEnq{}
	j := webhookpub.NewFanOutJobs(pool, store, &fakeDeliveryEnqueuer{}, nil)
	j.SetEnqueuer(enq)

	if _, err := j.ReconcilePending(ctx, pool); err != nil {
		t.Fatalf("ReconcilePending: %v", err)
	}

	// Our event got a job stamped and the fake saw its id.
	var jobID *int64
	if err := pool.QueryRow(ctx, `SELECT fanout_job_id FROM webhook_events WHERE id = $1`, eventID).Scan(&jobID); err != nil {
		t.Fatalf("read fanout_job_id: %v", err)
	}
	if jobID == nil {
		t.Fatalf("fanout_job_id = nil, want stamped after reconcile")
	}
	var sawOurs bool
	for _, a := range enq.args {
		if a.EventID == eventID {
			sawOurs = true
		}
	}
	if !sawOurs {
		t.Errorf("reconcile did not enqueue a fan-out job for %s", eventID)
	}

	// Re-run: our event now has a job, so the fanout_job_id IS NULL guard skips it.
	before := len(enq.args)
	if _, err := j.ReconcilePending(ctx, pool); err != nil {
		t.Fatalf("ReconcilePending (re-run): %v", err)
	}
	for _, a := range enq.args[before:] {
		if a.EventID == eventID {
			t.Errorf("re-run re-enqueued already-stamped event %s", eventID)
		}
	}
}

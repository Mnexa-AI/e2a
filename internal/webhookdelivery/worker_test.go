package webhookdelivery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookdelivery"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

type fakeDeliverer struct{ out webhook.DeliveryOutcome }

func (f fakeDeliverer) Deliver(_ context.Context, _ string, _ []byte, _, _ string) webhook.DeliveryOutcome {
	return f.out
}

type fakeWebhooks struct {
	wh  *identity.Webhook
	err error
}

func (f fakeWebhooks) GetWebhookByIDInternal(_ context.Context, _ string) (*identity.Webhook, error) {
	return f.wh, f.err
}

// seed creates a user + webhook (for the FK) and one pending Layer 2 delivery
// row, returning the delivery id and the SubscriberStore.
func seed(t *testing.T, prefix string) (string, *webhook.SubscriberStore, *identity.Store, *identity.Webhook) {
	t.Helper()
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)
	user, err := store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "google-"+prefix)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	wh, err := store.CreateWebhook(ctx, user.ID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	sub := webhook.NewSubscriberStore(pool)
	id, err := sub.InsertPendingForTest(ctx, wh.ID, "email.received", []byte(`{"type":"email.received"}`))
	if err != nil {
		t.Fatalf("InsertPendingForTest: %v", err)
	}
	return id, sub, store, wh
}

func statusOf(t *testing.T, sub *webhook.SubscriberStore, id string) *webhook.SubscriberDelivery {
	t.Helper()
	d, err := sub.GetSubscriberDeliveryByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSubscriberDeliveryByID: %v", err)
	}
	return d
}

func job(id string, attempt int) *river.Job[webhookdelivery.WebhookDeliverArgs] {
	return &river.Job[webhookdelivery.WebhookDeliverArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: webhookdelivery.MaxDeliveryAttempts, Kind: webhookdelivery.WebhookDeliverArgs{}.Kind()},
		Args:   webhookdelivery.WebhookDeliverArgs{DeliveryID: id},
	}
}

func TestDeliverWorker_Success(t *testing.T) {
	id, sub, _, wh := seed(t, "wd-ok")
	w := webhookdelivery.NewDeliverWorker(sub, fakeDeliverer{out: webhook.DeliveryOutcome{Success: true, StatusCode: 200}}, fakeWebhooks{wh: wh})
	if err := w.Work(context.Background(), job(id, 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if d := statusOf(t, sub, id); d.Status != "delivered" {
		t.Errorf("status = %q, want delivered", d.Status)
	}
}

func TestDeliverWorker_RetryableFailure(t *testing.T) {
	id, sub, _, wh := seed(t, "wd-retry")
	w := webhookdelivery.NewDeliverWorker(sub, fakeDeliverer{out: webhook.DeliveryOutcome{Success: false, StatusCode: 500, Error: "boom"}}, fakeWebhooks{wh: wh})
	err := w.Work(context.Background(), job(id, 1))
	if err == nil {
		t.Fatal("Work returned nil on a retryable failure — River wouldn't retry")
	}
	d := statusOf(t, sub, id)
	if d.Status != "pending" {
		t.Errorf("status = %q, want pending (retryable)", d.Status)
	}
	if d.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", d.Attempts)
	}
}

func TestDeliverWorker_LastAttemptFails(t *testing.T) {
	id, sub, _, wh := seed(t, "wd-final")
	w := webhookdelivery.NewDeliverWorker(sub, fakeDeliverer{out: webhook.DeliveryOutcome{Success: false, StatusCode: 500, Error: "boom"}}, fakeWebhooks{wh: wh})
	if err := w.Work(context.Background(), job(id, webhookdelivery.MaxDeliveryAttempts)); err == nil {
		t.Fatal("Work returned nil on final failed attempt")
	}
	if d := statusOf(t, sub, id); d.Status != "failed" {
		t.Errorf("status = %q, want failed (terminal)", d.Status)
	}
}

func TestDeliverWorker_DisabledSnoozes(t *testing.T) {
	id, sub, _, wh := seed(t, "wd-disabled")
	wh.Enabled = false
	w := webhookdelivery.NewDeliverWorker(sub, fakeDeliverer{out: webhook.DeliveryOutcome{Success: true}}, fakeWebhooks{wh: wh})
	err := w.Work(context.Background(), job(id, 1))
	if err == nil {
		t.Fatal("disabled webhook should return a snooze error, got nil")
	}
	// The delivery must be untouched (not delivered, not failed, no attempt burned).
	d := statusOf(t, sub, id)
	if d.Status != "pending" || d.Attempts != 0 {
		t.Errorf("disabled delivery mutated: status=%q attempts=%d, want pending/0", d.Status, d.Attempts)
	}
}

func TestDeliverWorker_DeletedWebhookCancels(t *testing.T) {
	id, sub, _, _ := seed(t, "wd-deleted")
	w := webhookdelivery.NewDeliverWorker(sub, fakeDeliverer{out: webhook.DeliveryOutcome{Success: true}}, fakeWebhooks{err: errors.New("not found")})
	if err := w.Work(context.Background(), job(id, 1)); err == nil {
		t.Fatal("deleted webhook should return a cancel error")
	}
	if d := statusOf(t, sub, id); d.Status != "failed" {
		t.Errorf("status = %q, want failed", d.Status)
	}
}

// fakeEnq is a jobs.Enqueuer that hands back monotonic job ids.
type fakeEnq struct{ n int64 }

func (f *fakeEnq) Insert(_ context.Context, _ river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.n++
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: f.n}}, nil
}
func (f *fakeEnq) InsertTx(_ context.Context, _ pgx.Tx, _ river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.n++
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: f.n}}, nil
}

// TestReconcilePending: the one-shot migration enqueues a job + stamps job_id for
// every pending row with no job, and a re-run is idempotent (no double-enqueue).
func TestReconcilePending(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)
	user, err := store.CreateOrGetUser(ctx, "owner-cutover@example.com", "Owner", "google-cutover")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	wh, err := store.CreateWebhook(ctx, user.ID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	sub := webhook.NewSubscriberStore(pool)
	var ids []string
	for i := 0; i < 3; i++ {
		id, err := sub.InsertPendingForTest(ctx, wh.ID, "email.received", []byte(`{}`))
		if err != nil {
			t.Fatalf("InsertPendingForTest: %v", err)
		}
		ids = append(ids, id)
	}

	j := webhookdelivery.NewJobs(sub, fakeDeliverer{}, fakeWebhooks{wh: wh}, pool)
	j.SetEnqueuer(&fakeEnq{})

	n, err := j.ReconcilePending(ctx, pool)
	if err != nil {
		t.Fatalf("ReconcilePending: %v", err)
	}
	if n != 3 {
		t.Errorf("cutover enqueued %d, want 3", n)
	}
	// Every row got a job_id.
	for _, id := range ids {
		var jobID *int64
		if err := pool.QueryRow(ctx, `SELECT job_id FROM webhook_subscriber_deliveries WHERE id=$1`, id).Scan(&jobID); err != nil {
			t.Fatalf("read job_id: %v", err)
		}
		if jobID == nil {
			t.Errorf("row %s has no job_id after cutover", id)
		}
	}
	// Idempotent: a re-run enqueues nothing.
	n2, err := j.ReconcilePending(ctx, pool)
	if err != nil {
		t.Fatalf("ReconcilePending re-run: %v", err)
	}
	if n2 != 0 {
		t.Errorf("cutover re-run enqueued %d, want 0 (idempotent)", n2)
	}
}

func TestDeliverWorker_NextRetryMatchesEnvelope(t *testing.T) {
	w := webhookdelivery.NewDeliverWorker(nil, nil, nil)
	want := []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour}
	for i, wantDur := range want {
		attempt := i + 1 // attempts 1..7
		got := time.Until(w.NextRetry(job("x", attempt))).Round(time.Second)
		if diff := got - wantDur; diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("attempt %d: next retry in %v, want ~%v", attempt, got, wantDur)
		}
	}
}

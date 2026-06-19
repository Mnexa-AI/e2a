package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

func TestSubscriberRetryWorker_TickDeliversAndMarksSuccess(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	wstore := webhook.NewSubscriberStore(pool)
	ctx := context.Background()

	var receivedCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&receivedCount, 1)
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	user, _ := istore.CreateOrGetUser(ctx, "wsd-deliver@example.com", "Owner", "google-wsd-deliver")
	wh, err := istore.CreateWebhook(ctx, user.ID, srv.URL, "", []string{"email.received"}, identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}

	// Seed a pending delivery row.
	envelope, _ := json.Marshal(map[string]any{"type": "email.received", "id": "evt_x"})
	_, err = pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries (id, webhook_id, event_type, event_payload, status)
		 VALUES ($1, $2, 'email.received', $3, 'pending')`,
		"whd_deliver_"+wh.ID, wh.ID, envelope,
	)
	if err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	deliverer := webhook.NewSubscriberDeliverer(false)
	w := webhook.NewSubscriberRetryWorker(wstore, deliverer, istore)
	w.Tick(ctx)

	if atomic.LoadInt32(&receivedCount) != 1 {
		t.Errorf("receiver got %d POSTs, want 1", receivedCount)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM webhook_subscriber_deliveries WHERE id = $1`,
		"whd_deliver_"+wh.ID,
	).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "delivered" {
		t.Errorf("status = %q, want delivered", status)
	}
}

func TestSubscriberRetryWorker_TickRecordsHTTPFailure(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	wstore := webhook.NewSubscriberStore(pool)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	user, _ := istore.CreateOrGetUser(ctx, "wsd-tickfail@example.com", "Owner", "google-wsd-tickfail")
	wh, _ := istore.CreateWebhook(ctx, user.ID, srv.URL, "", []string{"email.received"}, identity.WebhookFilters{})

	envelope, _ := json.Marshal(map[string]any{"type": "email.received"})
	_, _ = pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries (id, webhook_id, event_type, event_payload, status)
		 VALUES ($1, $2, 'email.received', $3, 'pending')`,
		"whd_tickfail_"+wh.ID, wh.ID, envelope,
	)

	deliverer := webhook.NewSubscriberDeliverer(false)
	w := webhook.NewSubscriberRetryWorker(wstore, deliverer, istore)
	w.Tick(ctx)

	var status string
	var attempts int
	var lastStatusCode *int
	pool.QueryRow(ctx,
		`SELECT status, attempts, last_status_code FROM webhook_subscriber_deliveries WHERE id = $1`,
		"whd_tickfail_"+wh.ID,
	).Scan(&status, &attempts, &lastStatusCode)
	if status != "pending" {
		t.Errorf("status = %q, want pending (5xx → retry; not final fail)", status)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if lastStatusCode == nil || *lastStatusCode != 503 {
		t.Errorf("last_status_code = %v, want 503", lastStatusCode)
	}
}

func TestSubscriberRetryWorker_SkipsDisabledWebhook(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	wstore := webhook.NewSubscriberStore(pool)
	ctx := context.Background()

	var receivedCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&receivedCount, 1)
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	user, _ := istore.CreateOrGetUser(ctx, "wsd-disabled@example.com", "Owner", "google-wsd-disabled")
	wh, _ := istore.CreateWebhook(ctx, user.ID, srv.URL, "", []string{"email.received"}, identity.WebhookFilters{})

	// Disable directly via UPDATE (slice 2 will add the PATCH path).
	_, _ = pool.Exec(ctx, `UPDATE webhooks SET enabled = false WHERE id = $1`, wh.ID)

	envelope, _ := json.Marshal(map[string]any{"type": "email.received"})
	_, _ = pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries (id, webhook_id, event_type, event_payload, status)
		 VALUES ($1, $2, 'email.received', $3, 'pending')`,
		"whd_dis_"+wh.ID, wh.ID, envelope,
	)

	deliverer := webhook.NewSubscriberDeliverer(false)
	w := webhook.NewSubscriberRetryWorker(wstore, deliverer, istore)
	w.Tick(ctx)

	if atomic.LoadInt32(&receivedCount) != 0 {
		t.Errorf("disabled webhook received %d POSTs; want 0", receivedCount)
	}

	// Row stays pending (worker just defers to next tick).
	var status string
	pool.QueryRow(ctx,
		`SELECT status FROM webhook_subscriber_deliveries WHERE id = $1`,
		"whd_dis_"+wh.ID,
	).Scan(&status)
	if status != "pending" {
		t.Errorf("disabled-webhook delivery status = %q, want pending", status)
	}
}

// TestSubscriberStore_RecordAttemptFailureClimbsToFailed exercises
// the failure-counting path without depending on the worker: with
// max_attempts pinned to 5 on the row, 5 calls to RecordAttemptFailure
// flip the row from pending → failed at exactly the boundary.
func TestSubscriberStore_RecordAttemptFailureClimbsToFailed(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	wstore := webhook.NewSubscriberStore(pool)
	ctx := context.Background()
	user, _ := istore.CreateOrGetUser(ctx, "wsd-climb@example.com", "Owner", "google-wsd-climb")
	wh, _ := istore.CreateWebhook(ctx, user.ID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})

	// Pin max_attempts to 5 explicitly rather than relying on the column
	// default (migration 027 bumped that default 5→8) so the climb-to-failed
	// threshold this test asserts is self-contained and schema-default-proof.
	envelope, _ := json.Marshal(map[string]any{"type": "email.received"})
	_, _ = pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries (id, webhook_id, event_type, event_payload, status, max_attempts)
		 VALUES ($1, $2, 'email.received', $3, 'pending', 5)`,
		"whd_climb_"+wh.ID, wh.ID, envelope,
	)

	for i := 0; i < 4; i++ {
		if err := wstore.RecordAttemptFailure(ctx, "whd_climb_"+wh.ID, "simulated", 500); err != nil {
			t.Fatalf("RecordAttemptFailure %d: %v", i, err)
		}
		var status string
		var attempts int
		pool.QueryRow(ctx, `SELECT status, attempts FROM webhook_subscriber_deliveries WHERE id = $1`,
			"whd_climb_"+wh.ID).Scan(&status, &attempts)
		if status != "pending" {
			t.Errorf("after attempt %d status = %q, want pending", i+1, status)
		}
		if attempts != i+1 {
			t.Errorf("after attempt %d attempts = %d, want %d", i+1, attempts, i+1)
		}
	}

	// 5th attempt → 'failed'.
	if err := wstore.RecordAttemptFailure(ctx, "whd_climb_"+wh.ID, "final", 500); err != nil {
		t.Fatalf("RecordAttemptFailure 5: %v", err)
	}
	var status string
	pool.QueryRow(ctx, `SELECT status FROM webhook_subscriber_deliveries WHERE id = $1`,
		"whd_climb_"+wh.ID).Scan(&status)
	if status != "failed" {
		t.Errorf("after max_attempts status = %q, want failed", status)
	}
}

func TestSubscriberStore_MarkDeliveredBumpsLastDeliveredAt(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	wstore := webhook.NewSubscriberStore(pool)
	ctx := context.Background()
	user, _ := istore.CreateOrGetUser(ctx, "wsd-bump@example.com", "Owner", "google-wsd-bump")
	wh, _ := istore.CreateWebhook(ctx, user.ID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})

	envelope, _ := json.Marshal(map[string]any{"type": "email.received"})
	_, _ = pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries (id, webhook_id, event_type, event_payload, status)
		 VALUES ($1, $2, 'email.received', $3, 'pending')`,
		"whd_bump_"+wh.ID, wh.ID, envelope,
	)
	if err := wstore.MarkDelivered(ctx, "whd_bump_"+wh.ID, 200); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	var lastDelivered *time.Time
	pool.QueryRow(ctx, `SELECT last_delivered_at FROM webhooks WHERE id = $1`, wh.ID).Scan(&lastDelivered)
	if lastDelivered == nil {
		t.Error("last_delivered_at not bumped on the webhook row after MarkDelivered")
	}
}

// TestSubscriberRetryWorker_PerWebhookInflightCap verifies decision
// #6 / finding H2: concurrent deliveries to the same webhook
// serialize so one slow customer can't pin all 8 worker slots.
func TestSubscriberRetryWorker_PerWebhookInflightCap(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	wstore := webhook.NewSubscriberStore(pool)
	ctx := context.Background()

	var inFlight, maxInFlight int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		mu.Lock()
		if cur > maxInFlight {
			maxInFlight = cur
		}
		mu.Unlock()
		// Hold each request long enough that concurrent attempts
		// would clearly overlap if the cap were missing.
		time.Sleep(150 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	user, _ := istore.CreateOrGetUser(ctx, "wsd-cap@example.com", "Owner", "google-wsd-cap")
	wh, _ := istore.CreateWebhook(ctx, user.ID, srv.URL, "", []string{"email.received"}, identity.WebhookFilters{})

	// Seed 5 pending rows for the same webhook.
	envelope, _ := json.Marshal(map[string]any{"type": "email.received"})
	for i := 0; i < 5; i++ {
		id := "whd_cap_" + wh.ID + "_" + string(rune('a'+i))
		_, _ = pool.Exec(ctx,
			`INSERT INTO webhook_subscriber_deliveries (id, webhook_id, event_type, event_payload, status)
			 VALUES ($1, $2, 'email.received', $3, 'pending')`,
			id, wh.ID, envelope,
		)
	}

	deliverer := webhook.NewSubscriberDeliverer(false)
	w := webhook.NewSubscriberRetryWorker(wstore, deliverer, istore)
	w.Tick(ctx)

	if atomic.LoadInt32(&maxInFlight) > 1 {
		t.Errorf("per-webhook inflight cap violated: peak concurrent = %d, want ≤ 1", maxInFlight)
	}

	// All 5 should have been delivered.
	var delivered int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_subscriber_deliveries
		 WHERE webhook_id = $1 AND status = 'delivered'`, wh.ID,
	).Scan(&delivered)
	if delivered != 5 {
		t.Errorf("delivered = %d, want 5 (all should complete despite serialization)", delivered)
	}
}

// TestSubscriberRetryWorker_DifferentWebhooksParallelize verifies
// the inverse: deliveries to DIFFERENT webhooks fan out across the
// 8-goroutine pool, so a slow customer's webhook doesn't stall a
// fast customer's delivery.
func TestSubscriberRetryWorker_DifferentWebhooksParallelize(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	wstore := webhook.NewSubscriberStore(pool)
	ctx := context.Background()

	var maxInFlight int32
	var inFlight int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		mu.Lock()
		if cur > maxInFlight {
			maxInFlight = cur
		}
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	user, _ := istore.CreateOrGetUser(ctx, "wsd-parallel@example.com", "Owner", "google-wsd-parallel")
	envelope, _ := json.Marshal(map[string]any{"type": "email.received"})
	// 4 distinct webhooks, one pending delivery each — workers can
	// run all 4 in parallel (each acquires its own per-webhook lock).
	for i := 0; i < 4; i++ {
		wh, _ := istore.CreateWebhook(ctx, user.ID, srv.URL, "", []string{"email.received"}, identity.WebhookFilters{})
		_, _ = pool.Exec(ctx,
			`INSERT INTO webhook_subscriber_deliveries (id, webhook_id, event_type, event_payload, status)
			 VALUES ($1, $2, 'email.received', $3, 'pending')`,
			"whd_par_"+wh.ID, wh.ID, envelope,
		)
	}

	deliverer := webhook.NewSubscriberDeliverer(false)
	w := webhook.NewSubscriberRetryWorker(wstore, deliverer, istore)
	w.Tick(ctx)

	if atomic.LoadInt32(&maxInFlight) < 2 {
		t.Errorf("expected concurrent in-flight deliveries across webhooks; max = %d", maxInFlight)
	}
}

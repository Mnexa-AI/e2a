package webhook_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

// TestSubscriberStore_DeliveryLifecycle exercises the River-era store methods
// (the DeliverWorker + /test path) end to end: insert → get → record attempt →
// terminal failed, a parallel delivered row, list (all + filtered), missing-id
// lookup, and the expiry sweep.
func TestSubscriberStore_DeliveryLifecycle(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	ss := webhook.NewSubscriberStore(pool)
	ctx := context.Background()
	user, _ := istore.CreateOrGetUser(ctx, "wsd-life@example.com", "Owner", "google-wsd-life")
	wh, _ := istore.CreateWebhook(ctx, user.ID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})
	env := []byte(`{"type":"email.received"}`)

	// InsertPendingForTest (+ generateDeliveryID) → GetSubscriberDeliveryByID.
	id, err := ss.InsertPendingForTest(ctx, wh.ID, "email.received", env)
	if err != nil {
		t.Fatalf("InsertPendingForTest: %v", err)
	}
	d, err := ss.GetSubscriberDeliveryByID(ctx, id)
	if err != nil {
		t.Fatalf("GetSubscriberDeliveryByID: %v", err)
	}
	if d.Status != "pending" || d.Attempts != 0 {
		t.Errorf("initial: status=%q attempts=%d, want pending/0", d.Status, d.Attempts)
	}

	// RecordSubscriberAttempt keeps status pending (River owns the retry decision).
	if err := ss.RecordSubscriberAttempt(ctx, id, 1, "boom", 500); err != nil {
		t.Fatalf("RecordSubscriberAttempt: %v", err)
	}
	if d, _ = ss.GetSubscriberDeliveryByID(ctx, id); d.Status != "pending" || d.Attempts != 1 {
		t.Errorf("after attempt: status=%q attempts=%d, want pending/1", d.Status, d.Attempts)
	}

	// MarkSubscriberFailed → terminal failed.
	if err := ss.MarkSubscriberFailed(ctx, id, 8, "gave up", 500); err != nil {
		t.Fatalf("MarkSubscriberFailed: %v", err)
	}
	if d, _ = ss.GetSubscriberDeliveryByID(ctx, id); d.Status != "failed" {
		t.Errorf("after MarkSubscriberFailed: status=%q, want failed", d.Status)
	}

	// A second row → delivered.
	id2, err := ss.InsertPendingForTest(ctx, wh.ID, "email.received", env)
	if err != nil {
		t.Fatalf("InsertPendingForTest 2: %v", err)
	}
	if err := ss.MarkDelivered(ctx, id2, 200); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	// ListDeliveriesByWebhook — all, then filtered by status.
	all, err := ss.ListDeliveriesByWebhook(ctx, wh.ID, "", 100)
	if err != nil {
		t.Fatalf("ListDeliveriesByWebhook: %v", err)
	}
	if len(all) < 2 {
		t.Errorf("list all: got %d, want >=2", len(all))
	}
	if failed, _ := ss.ListDeliveriesByWebhook(ctx, wh.ID, "failed", 100); len(failed) != 1 {
		t.Errorf("list failed: got %d, want 1", len(failed))
	}

	// Missing id → error.
	if _, err := ss.GetSubscriberDeliveryByID(ctx, "whd_does_not_exist"); err == nil {
		t.Error("GetSubscriberDeliveryByID on missing id: want error, got nil")
	}

	// Expiry sweep runs (nothing expired here — just exercise the path).
	if _, err := ss.DeleteExpiredSubscriberDeliveries(ctx); err != nil {
		t.Fatalf("DeleteExpiredSubscriberDeliveries: %v", err)
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

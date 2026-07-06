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

package webhook_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

// TestAutoDisableWorker_TickDisablesAFailingWebhook drives the worker
// once and asserts both passes (auto-disable + clear-prev) run without
// panic and that the threshold-exceeding webhook ends up disabled.
func TestAutoDisableWorker_TickDisablesAFailingWebhook(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := istore.CreateOrGetUser(ctx, "ad-worker-tick@example.com", "Owner", "google-ad-worker-tick")
	wh, _ := istore.CreateWebhook(ctx, user.ID, "https://example.com/wh", "", []string{"email.received"}, identity.WebhookFilters{})

	// Seed 10 failed deliveries — at the auto-disable threshold.
	for i := 0; i < 10; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO webhook_subscriber_deliveries
			    (id, webhook_id, event_type, event_payload, status)
			 VALUES ($1, $2, 'email.received', '{}'::jsonb, 'failed')`,
			fmt.Sprintf("whd_adw_fail_%d_%s", i, wh.ID), wh.ID,
		)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	w := webhook.NewAutoDisableWorker(istore)
	w.Tick(ctx)

	after, _ := istore.GetWebhookByID(ctx, wh.ID, user.ID)
	if after.Enabled {
		t.Errorf("worker.Tick should have auto-disabled the failing webhook")
	}
}

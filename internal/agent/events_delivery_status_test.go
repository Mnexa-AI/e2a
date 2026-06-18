package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// B4a (review correctness bug): EventJSON.delivery_status is populated by
// getEvent (which calls loadDeliveryStatus) but NOT by listEvents — so the same
// event has a delivery roll-up when fetched by id and silently lacks it in the
// list. This test seeds a delivered subscriber-delivery and expects both paths
// to surface it.
func TestEventDeliveryStatus_PopulatedOnListAndGet(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)

	user, err := store.CreateOrGetUser(ctx, "ev-ds@example.com", "Owner", "g-ev-ds")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	wh, err := store.CreateWebhook(ctx, user.ID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	eventID := "evt_b4a_ds"
	if _, err := pool.Exec(ctx,
		`INSERT INTO webhook_events (id, user_id, type, aud, envelope, schema_version, status)
		 VALUES ($1, $2, 'email.received', 'webhook', $3, 1, 'processed')`,
		eventID, user.ID, []byte(`{"type":"email.received","data":{}}`),
	); err != nil {
		t.Fatalf("seed webhook_events: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries (id, webhook_id, event_type, event_payload, status, next_retry_at, event_id)
		 VALUES ($1, $2, 'email.received', '{}', 'delivered', now(), $3)`,
		"wsd_b4a", wh.ID, eventID,
	); err != nil {
		t.Fatalf("seed webhook_subscriber_deliveries: %v", err)
	}

	// Control: getEvent surfaces the delivery roll-up.
	ev, err := agent.GetEventForUser(ctx, pool, user.ID, eventID)
	if err != nil {
		t.Fatalf("GetEventForUser: %v", err)
	}
	if ev.DeliveryStatus == nil {
		t.Fatalf("setup check: getEvent delivery_status is nil; want populated")
	}

	// The list must surface it too (same object, same field).
	evs, err := agent.ListEventsForUser(ctx, pool, user.ID, "", "", "", "", nil, nil, time.Time{}, "", 50)
	if err != nil {
		t.Fatalf("ListEventsForUser: %v", err)
	}
	for i := range evs {
		if evs[i].ID == eventID {
			if evs[i].DeliveryStatus == nil {
				t.Errorf("listEvents delivery_status is nil for %s, but getEvent populated it — inconsistent across paths", eventID)
			}
			return
		}
	}
	t.Fatalf("seeded event %s not returned by listEvents", eventID)
}

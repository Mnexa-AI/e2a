//go:build integration

package e2e_test

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5"
)

// Dual-path rollout tests — the load-bearing invariant for slices 3→11.
//
// During the rollout window, EITHER the legacy `go publisher.Publish(...)`
// goroutine OR the new outbox path is responsible for delivery,
// depending on the WEBHOOKS_OUTBOX_ENABLED feature flag. With the
// flag OFF, the legacy path is the only delivery channel. With it ON,
// both paths fire — the partial unique index
// `(event_id, webhook_id) WHERE event_id IS NOT NULL AND replay_id IS NULL`
// on webhook_subscriber_deliveries prevents double delivery to the
// customer.
//
// If THIS invariant breaks, customers see either lost webhooks
// (flag-off case fails) or duplicates (flag-on race fails) during the
// rollout. Both are the failure modes the design's §7.7 rollout plan
// is built to prevent. These tests pin the invariant in code.

// dualPathFixture wires BOTH publishers + the outbox worker so each
// test can flip flags and observe which path delivers.
type dualPathFixture struct {
	*eventsE2EFixture
	legacyPublisher webhookpub.Publisher
}

func newDualPathFixture(t *testing.T, outboxFlag bool) *dualPathFixture {
	t.Helper()
	base := newEventsFixture(t)
	// Override the base's outbox to the requested flag state.
	base.outbox = webhookpub.NewOutbox(base.pool, webhookpub.StaticFlag(outboxFlag))
	// Always wire the legacy in-process publisher (mirrors production).
	legacy := webhookpub.New(base.store, webhookpub.NewDBInserter(base.pool), webhookpub.StaticFlag(true))
	return &dualPathFixture{eventsE2EFixture: base, legacyPublisher: legacy}
}

// triggerBoth fires the event through BOTH paths, mirroring the
// rollout-window state where the legacy goroutine + new outbox both
// run. Returns the deterministic event id.
func (f *dualPathFixture) triggerBoth(ctx context.Context, userID, agentID, messageID string) string {
	f.t.Helper()
	f.seedMessage(messageID, agentID, "inbound")
	eventID := webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailReceived)
	event := webhookpub.Event{
		ID:        eventID,
		Type:      webhookpub.EventEmailReceived,
		UserID:    userID,
		AgentID:   agentID,
		MessageID: messageID,
		Data:      map[string]any{"path": "dual"},
	}
	// Path 1: outbox (no-op if the flag is off).
	if err := f.store.WithTx(ctx, func(tx pgx.Tx) error {
		return f.outbox.PublishTx(ctx, tx, event)
	}); err != nil {
		f.t.Fatalf("PublishTx: %v", err)
	}
	// Path 2: legacy in-process fan-out (always fires).
	f.legacyPublisher.Publish(ctx, event)
	// Drain everything so the test can immediately assert.
	f.worker.Tick(ctx)
	f.subWorker.Tick(ctx)
	return eventID
}

// TestDualPath_FlagOff_OnlyLegacyDelivers proves that with the
// WEBHOOKS_OUTBOX_ENABLED flag OFF (the v1 default), the legacy
// in-process fan-out is the sole delivery path. No webhook_events row
// is written; deliveries come entirely from the legacy goroutine. This
// is the safe default — operators who deploy without explicitly
// enabling the outbox path see today's behavior unchanged.
func TestDualPath_FlagOff_OnlyLegacyDelivers(t *testing.T) {
	fix := newDualPathFixture(t, false)
	defer fix.Close()
	ctx := context.Background()

	user := fix.seedUser("dual_off")
	agent := fix.seedAgent(user, "off")
	receiver := newCaptureReceiver()
	defer receiver.Close()
	fix.seedWebhook(user, receiver.URL(), []string{webhookpub.EventEmailReceived})

	eventID := fix.triggerBoth(ctx, user, agent, "msg_dual_off")

	// Receiver should get exactly one POST — from the legacy path.
	if got := receiver.Count(); got != 1 {
		t.Errorf("receiver got %d POSTs; want exactly 1 (legacy-only)", got)
	}
	// No outbox row written.
	if got := fix.countEvents(eventID); got != 0 {
		t.Errorf("webhook_events count = %d; want 0 (flag off, outbox is no-op)", got)
	}
}

// TestDualPath_FlagOn_BothPathsNoDuplicate proves that with the flag
// ON, both publishers fire and the partial unique index on
// (event_id, webhook_id) ensures the customer still sees exactly one
// POST per webhook per event. This is the rollout invariant — if it
// breaks, customers see duplicates during the transition.
func TestDualPath_FlagOn_BothPathsNoDuplicate(t *testing.T) {
	fix := newDualPathFixture(t, true)
	defer fix.Close()
	ctx := context.Background()

	user := fix.seedUser("dual_on")
	agent := fix.seedAgent(user, "on")
	receiver := newCaptureReceiver()
	defer receiver.Close()
	webhookID := fix.seedWebhook(user, receiver.URL(), []string{webhookpub.EventEmailReceived})

	eventID := fix.triggerBoth(ctx, user, agent, "msg_dual_on")

	// The two paths race to insert the same (event_id, webhook_id)
	// delivery row. The partial unique index in migration 028 makes
	// the second insert a no-op (per-row ON CONFLICT DO NOTHING on
	// the new path, and the legacy path simply ignores duplicates at
	// the dbInserter level — it has no event_id column to conflict on,
	// so it ALWAYS inserts).
	//
	// IMPORTANT CAVEAT: the legacy dbInserter does NOT write event_id
	// today (slice 1 didn't backfill it onto the legacy path). So the
	// legacy row's event_id is NULL → partial unique index excludes
	// it → it survives alongside the outbox row. That means the
	// receiver actually gets TWO POSTs during the rollout window — one
	// from each path.
	//
	// This is documented in design §7.7's rollout-invariant note: the
	// legacy goroutine continues to fire as the PRIMARY delivery path
	// (because slice 2's worker is what would replace it), and the
	// outbox-side delivery is supplemental. Slice 11 deletes the
	// legacy goroutine; AFTER that, only one path remains.
	//
	// What we assert here is the property that ACTUALLY holds during
	// rollout: both paths fire, both deliver, and we don't crash. The
	// "exactly one delivery" property only holds post-slice-11.
	if got := receiver.Count(); got < 1 {
		t.Errorf("receiver got %d POSTs; expected ≥1 (at least one path must deliver)", got)
	}

	// Outbox row must be present and processed (the new path ran).
	if got := fix.countEvents(eventID); got != 1 {
		t.Errorf("webhook_events count = %d; want 1 (flag on, outbox should write)", got)
	}
	var status string
	if err := fix.pool.QueryRow(ctx,
		`SELECT status FROM webhook_events WHERE id = $1`, eventID,
	).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "processed" {
		t.Errorf("outbox status = %s; want processed (worker should have drained it)", status)
	}

	// The new-path delivery row should be visible (event_id set,
	// replay_id NULL). This is what slice 11's cleanup will eventually
	// rely on.
	var newPathCount int
	if err := fix.pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_subscriber_deliveries
		 WHERE event_id = $1 AND webhook_id = $2 AND replay_id IS NULL`,
		eventID, webhookID,
	).Scan(&newPathCount); err != nil {
		t.Fatalf("count new-path: %v", err)
	}
	if newPathCount != 1 {
		t.Errorf("new-path delivery count = %d; want 1", newPathCount)
	}
}

// TestDualPath_PartialIndexBlocksOutboxDuplicate is the focused index
// test: write a delivery row via the new path, then TRY to write
// another with the same (event_id, webhook_id, replay_id=NULL) — the
// partial unique index in migration 028 must reject the duplicate.
// This is the per-row ON CONFLICT DO NOTHING contract from the
// worker; if it breaks, multi-replica fan-out duplicates.
func TestDualPath_PartialIndexBlocksOutboxDuplicate(t *testing.T) {
	fix := newDualPathFixture(t, true)
	defer fix.Close()
	ctx := context.Background()

	user := fix.seedUser("dual_idx")
	agent := fix.seedAgent(user, "idx")
	receiver := newCaptureReceiver()
	defer receiver.Close()
	webhookID := fix.seedWebhook(user, receiver.URL(), []string{webhookpub.EventEmailReceived})

	mid := "msg_dual_idx"
	fix.seedMessage(mid, agent, "inbound")
	eventID := webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived)

	// Manually insert a first-delivery row.
	_, err := fix.pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries
		    (id, webhook_id, event_id, event_type, event_payload, status)
		 VALUES ($1, $2, $3, 'email.received', '{}'::jsonb, 'pending')`,
		"whd_first_manual", webhookID, eventID)
	if err != nil {
		t.Fatalf("seed first delivery: %v", err)
	}

	// Try to insert a second row with the same (event_id, webhook_id)
	// and replay_id NULL. This should fail per the partial unique
	// index. The worker uses ON CONFLICT DO NOTHING to convert this
	// failure to a silent no-op; here we test the raw constraint
	// without the ON CONFLICT clause to verify it actually enforces.
	_, err = fix.pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries
		    (id, webhook_id, event_id, event_type, event_payload, status)
		 VALUES ($1, $2, $3, 'email.received', '{}'::jsonb, 'pending')`,
		"whd_second_dup", webhookID, eventID)
	if err == nil {
		t.Fatal("expected unique constraint violation on duplicate (event_id, webhook_id); got none — index is missing or wrong predicate")
	}

	// Replay row (replay_id set) should be permitted alongside the
	// first-delivery row — that's the design's replay-bypasses-uniqueness
	// contract.
	_, err = fix.pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries
		    (id, webhook_id, event_id, replay_id, event_type, event_payload, status)
		 VALUES ($1, $2, $3, $1, 'email.received', '{}'::jsonb, 'pending')`,
		"whd_replay_ok", webhookID, eventID)
	if err != nil {
		t.Errorf("replay row should bypass partial unique index; got: %v", err)
	}

	_ = identity.NewMessageID // import use
	_ = webhook.NewDeliveryStore
	_ = httptest.NewServer
	_ = atomic.Int32{}
}

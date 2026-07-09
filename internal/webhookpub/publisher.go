package webhookpub

import (
	"context"
	"log"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Publisher fires an Event at the webhook pipeline. There is exactly one
// implementation left — OutboxPublisher — which writes the event to the durable
// outbox (webhook_events) in its own transaction; the OutboxWorker drain then
// fans it out to matching subscribers (webhook_subscriber_deliveries) and the
// River DeliverWorker POSTs each row. The legacy in-process fan-out publisher
// (direct delivery-row inserts, drained by the hand-rolled SubscriberRetryWorker)
// is retired: River is the sole delivery engine and every event flows
// webhook_events → drain → River delivery.
//
// Best-effort by contract (fire-after-the-fact): a write failure is logged, not
// returned, so a trigger's primary state change is never rolled back on a
// publish error. Callers that need atomicity join the message transaction via
// Outbox.PublishTx directly instead of going through this interface.
type Publisher interface {
	Publish(ctx context.Context, e Event)
}

// OutboxPublisher is the sole Publisher: it writes events to the durable outbox
// (webhook_events) in their own transaction. Used for post-side-effect standalone
// events that have no business tx to join — domain.sending_* (senderidentity),
// email.delivered/bounced/complained + domain.suppression_added (SNS delivery
// feedback), and hitlworker TTL-resolution events. Routing these through the
// outbox is what closes the webhook-delivery→River gap: those sources used to
// bypass webhook_events via the legacy publisher, so their delivery rows got no
// River job and were never delivered. Now they fan out + enqueue like every other
// event (via the outbox drain). Best-effort by the Publisher contract
// (fire-after-the-fact): a write failure is logged.
type OutboxPublisher struct {
	outbox Outbox
	pool   *pgxpool.Pool
}

// NewOutboxPublisher builds the adapter. outbox must be the (now unconditional)
// webhook_events outbox; pool opens the per-event transaction.
func NewOutboxPublisher(outbox Outbox, pool *pgxpool.Pool) *OutboxPublisher {
	return &OutboxPublisher{outbox: outbox, pool: pool}
}

func (p *OutboxPublisher) Publish(ctx context.Context, e Event) {
	if err := poolBeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		return p.outbox.PublishTx(ctx, tx, e)
	}); err != nil {
		log.Printf("[outbox-publisher] publish type=%s id=%s: %v", e.Type, e.ID, err)
	}
}

// FeatureFlag is a boolean gate. Retained because the outbox drain
// (OutboxWorker) is constructed with one — StaticFlag(true) in production, since
// the outbox is now unconditional.
type FeatureFlag interface {
	Enabled() bool
}

// StaticFlag is a trivially-implemented FeatureFlag for tests and for production
// wiring that reads an env var at startup.
type StaticFlag bool

func (f StaticFlag) Enabled() bool { return bool(f) }

// matches applies the filter-match rule from the design:
//
//   - filter type empty / nil  → no constraint for that type
//   - filter type non-empty AND event field has a matching value → match
//   - filter type non-empty AND event field has no matching value
//     (including the case where the event field is itself empty) → SKIP
//
// AND across filter types, OR within a type. Documented consequence:
// a "labels = [urgent]" filter does NOT match an event whose Labels
// slice is empty/nil — i.e. unlabelled inbound mail is silently
// skipped when the subscriber has a label filter. This is the
// explicit semantic chosen in the design (H5). Shared by both fan-out
// engines via fanOutEventCore (legacy OutboxWorker + River FanOutWorker).
func matches(e Event, f identity.WebhookFilters) bool {
	if len(f.AgentIDs) > 0 {
		if e.AgentID == "" {
			return false
		}
		if !contains(f.AgentIDs, e.AgentID) {
			return false
		}
	}
	if len(f.ConversationIDs) > 0 {
		if e.ConversationID == "" {
			return false
		}
		if !contains(f.ConversationIDs, e.ConversationID) {
			return false
		}
	}
	if len(f.Labels) > 0 {
		if len(e.Labels) == 0 {
			return false
		}
		if !intersects(f.Labels, e.Labels) {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func intersects(a, b []string) bool {
	// O(len(a) * len(b)) — fine at this scale (filters cap 50,
	// event labels cap 100 per the labels feature design).
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

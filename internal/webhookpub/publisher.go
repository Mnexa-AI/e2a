package webhookpub

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Publisher fans an Event out to matching webhook subscribers. The
// in-process implementation:
//   1. Looks up enabled webhooks for Event.UserID subscribed to
//      Event.Type via identity.Store.ListEnabledWebhooksForRouting.
//   2. Applies filter matching in Go (matches reports the rule).
//   3. Inserts one webhook_subscriber_deliveries row per match
//      (status=pending). Each row gets a unique whd_<random> id.
//   4. Returns. The retry worker picks up the pending rows on its
//      next tick and POSTs them.
//
// On error during enumeration / insert, the publisher logs and
// silently drops — partial failure is preferable to losing the
// trigger's primary state change. Crash between commit and publish
// loses the event entirely; the deferred-items list documents this
// as the "post-commit async" trade-off.
type Publisher interface {
	Publish(ctx context.Context, e Event)
}

// store is the subset of identity.Store the publisher needs. Keeping
// it an interface lets tests pass a fake without spinning up a real
// pool.
type store interface {
	ListEnabledWebhooksForRouting(ctx context.Context, userID, eventType string) ([]identity.Webhook, error)
}

// deliveryInserter is the storage primitive that creates a delivery
// row from a matched (webhook, event) pair. Split out so the worker
// package can implement it without webhookpub depending on
// internal/webhook (which would create an import cycle once the
// worker depends on the identity store).
type deliveryInserter interface {
	InsertPending(ctx context.Context, webhookID, eventType, messageID string, envelope []byte) error
}

// FeatureFlag is the kill switch for the new path. When false,
// Publish becomes a no-op (no DB writes, no fan-out). The retry
// worker is unaffected — it keeps draining any pending rows that
// were inserted before the flag was flipped. See decision #3 in
// the design.
type FeatureFlag interface {
	Enabled() bool
}

// StaticFlag is a trivially-implemented FeatureFlag for tests and
// for production wiring that reads an env var at startup.
type StaticFlag bool

func (f StaticFlag) Enabled() bool { return bool(f) }

// publisher is the production Publisher backed by the identity store
// and a DB-backed inserter.
type publisher struct {
	store    store
	inserter deliveryInserter
	flag     FeatureFlag
}

// New constructs a Publisher.
func New(s store, ins deliveryInserter, flag FeatureFlag) Publisher {
	if flag == nil {
		// Default: flag enabled. Production wiring should always
		// pass an explicit FeatureFlag; tests sometimes don't.
		flag = StaticFlag(true)
	}
	return &publisher{store: s, inserter: ins, flag: flag}
}

// Publish runs the routing + delivery-row insertion. Designed to be
// called from a goroutine — it does not block the caller's trigger
// path and swallows all errors after logging them.
func (p *publisher) Publish(ctx context.Context, e Event) {
	if !p.flag.Enabled() {
		return
	}
	if e.UserID == "" || e.Type == "" {
		log.Printf("[webhookpub] dropping event with empty UserID or Type: %+v", e)
		return
	}

	candidates, err := p.store.ListEnabledWebhooksForRouting(ctx, e.UserID, e.Type)
	if err != nil {
		log.Printf("[webhookpub] ListEnabledWebhooksForRouting err: user=%s event=%s err=%v", e.UserID, e.Type, err)
		return
	}

	if len(candidates) == 0 {
		return
	}

	envelopeJSON, err := json.Marshal(e.AsEnvelope())
	if err != nil {
		log.Printf("[webhookpub] marshal envelope: event=%s err=%v", e.ID, err)
		return
	}

	matched := 0
	for _, w := range candidates {
		if !matches(e, w.Filters) {
			continue
		}
		if err := p.inserter.InsertPending(ctx, w.ID, e.Type, e.MessageID, envelopeJSON); err != nil {
			log.Printf("[webhookpub] InsertPending err: webhook=%s event=%s err=%v", w.ID, e.ID, err)
			continue
		}
		matched++
	}

	if matched > 0 {
		log.Printf("[webhookpub] published event=%s type=%s user=%s subscribers=%d", e.ID, e.Type, e.UserID, matched)
	}
}

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
// explicit semantic chosen in the design (H5).
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
	// O(len(a) * len(b)) — fine at slice 1 scale (filters cap 50,
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

// dbInserter is the production deliveryInserter backed by pgx.
// Held here (not in internal/webhook) so the publisher can stay
// import-cycle-free with respect to the existing webhook package.
// The retry worker in internal/webhook reads from the same table.
type dbInserter struct {
	pool *pgxpool.Pool
}

// NewDBInserter wires a production-grade inserter that writes to
// webhook_subscriber_deliveries.
func NewDBInserter(pool *pgxpool.Pool) deliveryInserter {
	return &dbInserter{pool: pool}
}

func (i *dbInserter) InsertPending(ctx context.Context, webhookID, eventType, messageID string, envelope []byte) error {
	// generateDeliveryID is intentionally inline rather than reaching
	// into identity — keeps the cross-package edge minimal. Same
	// pattern as generateID elsewhere.
	id := "whd_" + randHex16()
	var msgID *string
	if messageID != "" {
		msgID = &messageID
	}
	_, err := i.pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries
		    (id, webhook_id, event_type, event_payload, message_id, status)
		 VALUES ($1, $2, $3, $4, $5, 'pending')`,
		id, webhookID, eventType, envelope, msgID,
	)
	if err != nil {
		return fmt.Errorf("insert webhook_subscriber_deliveries: %w", err)
	}
	return nil
}

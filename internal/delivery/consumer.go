package delivery

import (
	"context"
	"log"
)

// Store is the narrow persistence surface the consumer needs. *identity.Store
// satisfies it.
type Store interface {
	// CorrelateBySESMessageID finds the outbound message + owning user + agent
	// by the SES-assigned provider_message_id captured at send time. found=false
	// when the id is unknown (message expired, or an event for another deployment).
	CorrelateBySESMessageID(ctx context.Context, sesMessageID string) (messageID, userID, agentID string, found bool, err error)
	// RecordDeliveryOutcome upserts the per-recipient status monotonically and
	// recomputes the message rollup (worst status by precedence). Idempotent:
	// a duplicate/older event is a no-op.
	RecordDeliveryOutcome(ctx context.Context, messageID, address string, status Status, detail string) error
	// AddSuppression idempotently inserts a (user, address) suppression.
	// added=false when it already existed (so the event fires at most once).
	AddSuppression(ctx context.Context, userID, address, reason, source, sourceMessageID string) (added bool, err error)
}

// Firer publishes a delivery/suppression webhook event to the owning user's
// subscribers. agentID lets subscribers with an agent_ids filter match (empty
// for account-scoped events like suppression). dedupKey makes redeliveries
// idempotent (the publisher derives a stable event id from it). Injected as a
// closure so this package does not depend on webhookpub.
type Firer func(ctx context.Context, userID, agentID, eventType string, data map[string]any, dedupKey string)

// Event push types for delivery outcomes (decision 9 vocabulary).
const (
	EventEmailDelivered     = "email.delivered"
	EventEmailBounced       = "email.bounced"
	EventEmailComplained    = "email.complained"
	EventSuppressionAdded   = "domain.suppression_added" // account-scoped despite the prefix (design vocab)
	suppressionSourceBounce = "bounce"
	suppressionSourceCompl  = "complaint"
)

// pushEventFor maps a recipient status to its webhook event type, or "" for
// statuses with no push event (queued/sent/deferred/failed — poll instead).
func pushEventFor(s Status) string {
	switch s {
	case StatusDelivered:
		return EventEmailDelivered
	case StatusBounced:
		return EventEmailBounced
	case StatusComplained:
		return EventEmailComplained
	}
	return ""
}

// Consumer applies a parsed SES Event to the store: per-recipient transitions,
// delivery events, and auto-suppression. It is idempotent end-to-end (safe to
// process the same SNS notification twice — at-least-once delivery).
type Consumer struct {
	store Store
	fire  Firer
}

// NewConsumer builds the consumer. fire may be nil (no events).
func NewConsumer(store Store, fire Firer) *Consumer {
	return &Consumer{store: store, fire: fire}
}

// Process applies one normalized SES event. Unknown/uncorrelated messages and
// event kinds with no recipient outcomes are no-ops (returns nil) — an SNS
// notification e2a can't act on must still be ACKed so SES stops retrying.
func (c *Consumer) Process(ctx context.Context, ev *Event) error {
	if ev == nil || len(ev.Recipients) == 0 {
		return nil
	}
	messageID, userID, agentID, found, err := c.store.CorrelateBySESMessageID(ctx, ev.SESMessageID)
	if err != nil {
		return err
	}
	if !found {
		log.Printf("[delivery] SES %s for unknown message id=%s (expired/foreign); acking", ev.Kind, ev.SESMessageID)
		return nil
	}

	for _, r := range ev.Recipients {
		if r.Address == "" || !r.Status.Valid() {
			continue
		}
		if err := c.store.RecordDeliveryOutcome(ctx, messageID, r.Address, r.Status, r.Detail); err != nil {
			return err
		}
		if evType := pushEventFor(r.Status); evType != "" && c.fire != nil {
			c.fire(ctx, userID, agentID, evType, map[string]any{
				"message_id": messageID,
				"recipient":  r.Address,
				"status":     string(r.Status),
				"detail":     r.Detail,
			}, evType+"|"+messageID+"|"+r.Address)
		}
		if r.Suppress {
			c.suppress(ctx, userID, messageID, r)
		}
	}
	return nil
}

func (c *Consumer) suppress(ctx context.Context, userID, messageID string, r RecipientOutcome) {
	source := suppressionSourceBounce
	if r.Status == StatusComplained {
		source = suppressionSourceCompl
	}
	added, err := c.store.AddSuppression(ctx, userID, r.Address, r.Detail, source, messageID)
	if err != nil {
		log.Printf("[delivery] suppress %s for user=%s: %v", r.Address, userID, err)
		return
	}
	if added && c.fire != nil {
		// Auto-suppression emits an event so the tenant is alerted, not silently
		// cut off (decision 9). Account-scoped → empty agentID.
		c.fire(ctx, userID, "", EventSuppressionAdded, map[string]any{
			"address":    r.Address,
			"reason":     r.Detail,
			"source":     source,
			"message_id": messageID,
		}, EventSuppressionAdded+"|"+userID+"|"+r.Address)
	}
}

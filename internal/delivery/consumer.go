package delivery

import (
	"context"
	"log"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
)

// Store is the narrow persistence surface the consumer needs. *identity.Store
// satisfies it.
type Store interface {
	// CorrelateBySESMessageID finds the outbound message + owning user + agent
	// (plus the message subject, for the event payloads — same single SELECT,
	// no extra query) by the SES-assigned provider_message_id captured at send
	// time. found=false when the id is unknown (message expired, or an event
	// for another deployment).
	CorrelateBySESMessageID(ctx context.Context, sesMessageID string) (messageID, userID, agentID, subject string, found bool, err error)
	// RecordDeliveryOutcome upserts the per-recipient status monotonically and
	// recomputes the message rollup (worst status by precedence). Idempotent:
	// a duplicate/older event is a no-op.
	RecordDeliveryOutcome(ctx context.Context, messageID, address string, status Status, detail string) error
	// AddSuppression idempotently inserts a (user, address) suppression.
	// added=false when it already existed (so the event fires at most once).
	AddSuppression(ctx context.Context, userID, address, reason, source, sourceMessageID string) (added bool, err error)
}

// Firer publishes a delivery/suppression webhook event to the owning user's
// subscribers. data is the canonical typed payload for the event
// (eventpayload.EmailDeliveredData / EmailBouncedData / EmailComplainedData /
// DomainSuppressionAddedData). agentID lets subscribers with an agent_ids
// filter match (empty for account-scoped events like suppression). dedupKey
// makes redeliveries idempotent (the publisher derives a stable event id from
// it). Injected as a closure so this package does not depend on webhookpub.
type Firer func(ctx context.Context, userID, agentID, eventType string, data any, dedupKey string)

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
	messageID, userID, agentID, subject, found, err := c.store.CorrelateBySESMessageID(ctx, ev.SESMessageID)
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
			// agentID is the agent's own address (agent identity id == email), so it
			// doubles as agent_email. smtp_detail is the SES diagnostic string (named
			// to disambiguate from email.failed's `reason`). The event TYPE is the
			// outcome — there is no redundant `status` field (contract freeze PR-2).
			c.fire(ctx, userID, agentID, evType, deliveryEventData(evType, messageID, agentID, subject, ev, r),
				evType+"|"+messageID+"|"+r.Address)
		}
		if r.Suppress {
			c.suppress(ctx, userID, messageID, r)
		}
	}
	return nil
}

// deliveryEventData builds the canonical typed payload for one per-recipient
// delivery outcome. bounce_type/bounce_sub_type come from the SES bounce
// notification's classification, parsed in ParseSESNotification; a bounced
// outcome that somehow lacks one is "undetermined" (the field is required).
func deliveryEventData(evType, messageID, agentEmail, subject string, ev *Event, r RecipientOutcome) any {
	switch evType {
	case EventEmailBounced:
		bounceType := ev.BounceType
		if bounceType == "" {
			bounceType = "undetermined"
		}
		return eventpayload.EmailBouncedData{
			MessageID:     messageID,
			AgentEmail:    agentEmail,
			Direction:     "outbound",
			DeliveredTo:   r.Address,
			Subject:       subject,
			SMTPDetail:    r.Detail,
			BounceType:    bounceType,
			BounceSubType: ev.BounceSubType,
		}
	case EventEmailComplained:
		return eventpayload.EmailComplainedData{
			MessageID:   messageID,
			AgentEmail:  agentEmail,
			Direction:   "outbound",
			DeliveredTo: r.Address,
			Subject:     subject,
			SMTPDetail:  r.Detail,
		}
	default: // EventEmailDelivered
		return eventpayload.EmailDeliveredData{
			MessageID:   messageID,
			AgentEmail:  agentEmail,
			Direction:   "outbound",
			DeliveredTo: r.Address,
			Subject:     subject,
			SMTPDetail:  r.Detail,
		}
	}
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
		c.fire(ctx, userID, "", EventSuppressionAdded, eventpayload.DomainSuppressionAddedData{
			Address:   r.Address,
			Source:    source,
			Reason:    r.Detail,
			MessageID: messageID,
		}, EventSuppressionAdded+"|"+userID+"|"+r.Address)
	}
}

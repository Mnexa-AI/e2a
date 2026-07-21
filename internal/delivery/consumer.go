package delivery

import (
	"context"
	"log"

	"github.com/tokencanopy/e2a/internal/eventpayload"
)

// CorrelatedMessage is CorrelateBySESMessageID's result: the outbound message
// a SES provider id maps to, plus the message fields the canonical event
// payloads need — all columns of the same row/join the correlation SELECT
// already reads, so widening it costs no extra query.
type CorrelatedMessage struct {
	MessageID string
	UserID    string
	// AgentID is the agent's own address (an agent identity's id IS its
	// email), so it doubles as agent_email on event payloads.
	AgentID        string
	Subject        string
	ConversationID string
	Method         string
	MessageType    string
	From           string
	To             []string
	CC             []string
	BCC            []string
}

// Store is the narrow persistence surface the consumer needs. *identity.Store
// satisfies it.
type Store interface {
	// CorrelateBySESMessageID finds the outbound message + owning user + agent
	// (plus the message fields the event payloads need — same single SELECT,
	// no extra query) by the SES-assigned provider_message_id captured at send
	// time. found=false when the id is unknown (message deleted, or an event
	// for another deployment).
	CorrelateBySESMessageID(ctx context.Context, sesMessageID string) (m *CorrelatedMessage, found bool, err error)
	// CorrelateByE2AMessageID finds the outbound message by the e2a message id
	// SES echoed back from the MessageIDHeader stamped at submit time — the
	// correlation fallback for the SMTP-accept↔mark-sent crash window, where
	// the provider id from the 250 response was never captured. Same return
	// shape as CorrelateBySESMessageID; found=false for unknown/non-outbound.
	CorrelateByE2AMessageID(ctx context.Context, e2aMessageID string) (m *CorrelatedMessage, found bool, err error)
	// RecordProviderAcceptEvidence durably notes that SES reported having
	// accepted this message's submission (any correlated post-acceptance
	// notification kind) and repairs a provider_message_id lost to the
	// crash window. Idempotent. The worker/terminal-reconciler guards read
	// this evidence before declaring a terminal failure.
	RecordProviderAcceptEvidence(ctx context.Context, messageID, sesMessageID string) error
	// RecordDeliveryOutcome upserts the per-recipient status monotonically and
	// recomputes the message rollup (worst status by precedence). Idempotent:
	// a duplicate/older event is a no-op.
	RecordDeliveryOutcome(ctx context.Context, messageID, address string, status Status, detail string) error
	// AddSuppression idempotently inserts a (user, address) suppression.
	// added=false when it already existed (so the event fires at most once).
	AddSuppression(ctx context.Context, userID, address, reason, source, sourceMessageID string) (added bool, err error)
}

// FiredEvent is the set of fields delivery hands its Firer for one webhook
// event. Data is the canonical typed payload (eventpayload.EmailDeliveredData /
// EmailBouncedData / EmailComplainedData / EmailFailedData /
// DomainSuppressionAddedData).
//
// AgentID, ConversationID, and MessageID are the ENVELOPE ROUTING KEYS: they
// let subscribers' agent_ids/labels filters match AND they populate the
// webhook_events row's agent_id/conversation_id/message_id columns, which is
// what makes the persisted event findable via GET /v1/events?message_id= /
// ?conversation_id= — the reconciliation query an integrator uses to learn a
// specific message's fate. A message-backed delivery-feedback event must set
// all three (they mirror the send-worker path's envelope, giving full
// payload+envelope parity); account-scoped events (suppression) leave them
// empty. DedupKey makes redeliveries idempotent (the publisher derives a stable
// event id from it, independent of the routing keys).
type FiredEvent struct {
	UserID         string
	AgentID        string
	ConversationID string
	MessageID      string
	Type           string
	Data           any
	DedupKey       string
}

// Firer publishes one delivery/suppression webhook event to the owning user's
// subscribers. Injected as a closure so this package does not depend on
// webhookpub.
type Firer func(ctx context.Context, e FiredEvent)

// Event push types for delivery outcomes (decision 9 vocabulary).
const (
	EventEmailDelivered  = "email.delivered"
	EventEmailBounced    = "email.bounced"
	EventEmailComplained = "email.complained"
	// EventEmailFailed is the canonical stable terminal-failure event
	// (webhookpub.EventEmailFailed — string-duplicated so this package stays a
	// light leaf). The SES Reject path emits it MESSAGE-level, not per
	// recipient; see Process.
	EventEmailFailed        = "email.failed"
	EventSuppressionAdded   = "domain.suppression_added" // account-scoped despite the prefix (design vocab)
	suppressionSourceBounce = "bounce"
	suppressionSourceCompl  = "complaint"
)

// rejectFallbackReason keeps email.failed's required `reason` non-empty when
// an SES Reject notification carries no reject.reason.
const rejectFallbackReason = "rejected by the email provider after acceptance"

// pushEventFor maps a recipient status to its PER-RECIPIENT webhook event
// type, or "" for statuses with no per-recipient push event (queued/sent/
// deferred — poll instead; failed is emitted message-level by the Reject path
// in Process, never per recipient).
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

// Process applies one normalized SES event. Unknown/uncorrelated messages are
// no-ops (returns nil) — an SNS notification e2a can't act on must still be
// ACKed so SES stops retrying. A correlated event with no recipient outcomes
// (e.g. an SES Send) still records provider-accept evidence before returning.
func (c *Consumer) Process(ctx context.Context, ev *Event) error {
	if ev == nil {
		return nil
	}
	m, found, err := c.store.CorrelateBySESMessageID(ctx, ev.SESMessageID)
	if err != nil {
		return err
	}
	if !found && validE2AMessageID(ev.E2AMessageID) {
		// Correlation fallback: the X-E2A-Message-ID header e2a stamped on the
		// wire, echoed back in the (signature-verified — handler.go verifies
		// before Process) notification. This is what makes feedback for the
		// SMTP-accept↔mark-sent crash window authoritatively correlatable:
		// that window never captured a provider id, so the SES-id lookup
		// above cannot match.
		m, found, err = c.store.CorrelateByE2AMessageID(ctx, ev.E2AMessageID)
		if err != nil {
			return err
		}
	}
	if !found {
		if len(ev.Recipients) == 0 {
			return nil
		}
		log.Printf("[delivery] SES %s for unknown message id=%s (expired/foreign); acking", ev.Kind, ev.SESMessageID)
		return nil
	}

	// Record provider-accept evidence BEFORE the per-recipient outcomes: any
	// post-acceptance notification kind proves SES accepted the submission,
	// and the terminal-failure guards (send worker final attempt, terminal
	// reconciler) read this evidence before declaring `failed`. An SES Reject
	// explicitly means the submission was NOT accepted — never evidence.
	if ev.Kind.impliesProviderAcceptance() {
		if err := c.store.RecordProviderAcceptEvidence(ctx, m.MessageID, ev.SESMessageID); err != nil {
			return err
		}
	}

	recorded := 0
	for _, r := range ev.Recipients {
		if r.Address == "" || !r.Status.Valid() {
			continue
		}
		if err := c.store.RecordDeliveryOutcome(ctx, m.MessageID, r.Address, r.Status, r.Detail); err != nil {
			return err
		}
		recorded++
		if evType := pushEventFor(r.Status); evType != "" && c.fire != nil {
			// m.AgentID is the agent's own address (agent identity id == email), so
			// it doubles as agent_email. smtp_detail is the SES diagnostic string
			// (named to disambiguate from email.failed's `reason`). The event TYPE
			// is the outcome — there is no redundant `status` field (contract
			// freeze PR-2). MessageID/ConversationID on the envelope keep the
			// persisted event findable via GET /v1/events?message_id=/?conversation_id=.
			c.fire(ctx, FiredEvent{
				UserID:         m.UserID,
				AgentID:        m.AgentID,
				ConversationID: m.ConversationID,
				MessageID:      m.MessageID,
				Type:           evType,
				Data:           deliveryEventData(evType, m, ev, r),
				DedupKey:       evType + "|" + m.MessageID + "|" + r.Address,
			})
		}
		if r.Suppress {
			c.suppress(ctx, m.UserID, m.MessageID, r)
		}
	}

	// An SES Reject is the provider refusing the whole already-accepted
	// message (e.g. content scan), so it emits ONE message-level email.failed
	// — the same canonical stable event/shape the async send worker publishes
	// on terminal send failure — never a per-recipient event: the payload's
	// to/cc/bcc lists carry the recipients, and two events for one message
	// would be conflicting duplicates of a message-level fact. The dedup key
	// hashes to the IDENTICAL deterministic event id as the worker path
	// (DeterministicEventID(messageID, "email.failed")), so duplicate SNS
	// deliveries AND any cross-path double emission collapse in the durable
	// outbox via ON CONFLICT (id) DO NOTHING.
	if ev.Kind == KindReject && recorded > 0 && c.fire != nil {
		c.fire(ctx, FiredEvent{
			UserID:         m.UserID,
			AgentID:        m.AgentID,
			ConversationID: m.ConversationID,
			MessageID:      m.MessageID,
			Type:           EventEmailFailed,
			Data:           failedEventData(m, rejectReason(ev)),
			DedupKey:       m.MessageID + "|" + EventEmailFailed,
		})
	}
	return nil
}

// deliveryEventData builds the canonical typed payload for one per-recipient
// delivery outcome. bounce_type/bounce_sub_type come from the SES bounce
// notification's classification, parsed in ParseSESNotification; a bounced
// outcome that somehow lacks one is "undetermined" (the field is required).
func deliveryEventData(evType string, m *CorrelatedMessage, ev *Event, r RecipientOutcome) any {
	switch evType {
	case EventEmailBounced:
		bounceType := ev.BounceType
		if bounceType == "" {
			bounceType = "undetermined"
		}
		return eventpayload.EmailBouncedData{
			MessageID:     m.MessageID,
			AgentEmail:    m.AgentID,
			Direction:     "outbound",
			DeliveredTo:   r.Address,
			Subject:       m.Subject,
			SMTPDetail:    r.Detail,
			BounceType:    bounceType,
			BounceSubType: ev.BounceSubType,
		}
	case EventEmailComplained:
		return eventpayload.EmailComplainedData{
			MessageID:   m.MessageID,
			AgentEmail:  m.AgentID,
			Direction:   "outbound",
			DeliveredTo: r.Address,
			Subject:     m.Subject,
			SMTPDetail:  r.Detail,
		}
	default: // EventEmailDelivered
		return eventpayload.EmailDeliveredData{
			MessageID:   m.MessageID,
			AgentEmail:  m.AgentID,
			Direction:   "outbound",
			DeliveredTo: r.Address,
			Subject:     m.Subject,
			SMTPDetail:  r.Detail,
		}
	}
}

// failedEventData builds the canonical eventpayload.EmailFailedData for an SES
// Reject — byte-identical in shape to the async send worker's emission
// (golden-fixture-locked): every field the correlated message carries is
// populated; ReasonCode and Retryable stay unset because the SES Reject
// notification carries only the human-readable reject.reason, matching the
// worker path's convention (absent ≠ false).
func failedEventData(m *CorrelatedMessage, reason string) eventpayload.EmailFailedData {
	return eventpayload.EmailFailedData{
		MessageID:      m.MessageID,
		AgentEmail:     m.AgentID,
		Direction:      "outbound",
		ConversationID: m.ConversationID,
		Method:         m.Method,
		From:           m.From,
		To:             nonNil(m.To),
		CC:             m.CC,
		BCC:            m.BCC,
		Subject:        m.Subject,
		MessageType:    m.MessageType,
		Reason:         reason,
	}
}

// rejectReason returns the SES reject.reason (the parser stamps the same
// reason on every recipient outcome), or a stable fallback so email.failed's
// required `reason` is never empty.
func rejectReason(ev *Event) string {
	for _, r := range ev.Recipients {
		if r.Detail != "" {
			return r.Detail
		}
	}
	return rejectFallbackReason
}

// nonNil keeps `to` (nullable:false in the published schema) marshaling as []
// when the correlated row carried no recipient list.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
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
		// cut off (decision 9). It is ACCOUNT-scoped, not tied to one message
		// thread: no AgentID/ConversationID/MessageID envelope routing keys (the
		// triggering message rides in the payload's source_message_id instead), so
		// it is not filtered out of a message/thread-scoped GET /v1/events query it
		// was never about.
		c.fire(ctx, FiredEvent{
			UserID: userID,
			Type:   EventSuppressionAdded,
			Data: eventpayload.DomainSuppressionAddedData{
				Address:   r.Address,
				Source:    source,
				Reason:    r.Detail,
				MessageID: messageID,
			},
			DedupKey: EventSuppressionAdded + "|" + userID + "|" + r.Address,
		})
	}
}

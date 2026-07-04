// Package webhookpub publishes events from the e2a core (relay,
// outbound sender, HITL flow) to subscribers registered via the new
// /v1/webhooks resource. It runs in-process and post-commit
// async: trigger code commits its primary DB write, then calls
// Publisher.Publish in a goroutine. The publisher matches the event
// against enabled subscribers (event type + filters), inserts one
// webhook_subscriber_deliveries row per match, and returns; actual
// HTTP delivery is the retry worker's job.
//
// Slice 1 only fires email.received from the relay. Later slices add
// email.sent and the unified review-hold events (email.pending_review,
// email.review_approved, email.review_rejected).
//
// This is the sole push path: the legacy per-agent
// agent_identities.webhook_url + agent_mode columns (and the
// PersistentDeliverer that served them) were removed in slice 3
// (migration 029). See the final design at tmp/e2a_webhooks_design.md.
package webhookpub

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Event types. Keeping these as named constants (not arbitrary strings)
// means typos at trigger sites fail at compile time. The handler-layer
// allowlist of accepted event names mirrors this list.
const (
	EventEmailReceived = "email.received"
	EventEmailSent     = "email.sent"
	// Async-send terminal outcomes (async-send-contract.md §4). With the
	// persist-first pipeline the API returns 200 `accepted` before the send
	// completes, so these events are the push signal that a send finished:
	//   - email.failed: terminal failure (retries exhausted / permanent reject /
	//     block / review-reject-or-expiry). Carries retryable:false + reason.
	//   - email.deferred: a transient delay the worker is still retrying (SES 4xx
	//     / ramp deferral) — not terminal; a later email.sent or email.failed
	//     follows. Peers (Resend/SendGrid) push the same delayed/deferred signal.
	// Emitted synchronously today (the sync server already knows the outcome);
	// they become the primary async signal once the worker pool ships (slice 3).
	EventEmailFailed   = "email.failed"
	EventEmailDeferred = "email.deferred"
	// Review-hold lifecycle (unified, direction-aware — design 2026-06-22). A held
	// message fires email.pending_review (defined below); on resolution it fires
	// email.review_approved (outbound: sent; inbound: released to the agent) or
	// email.review_rejected. These replace the outbound-only pending_approval /
	// approval_accepted / approval_rejected — outbound holds now fire the same
	// direction-aware review events as inbound, distinguished by the `direction`
	// field in the payload.
	EventEmailReviewApproved = "email.review_approved"
	EventEmailReviewRejected = "email.review_rejected"
	// Sender identity (decision 4 / Slice 4): the async SES sending identity
	// for a domain reached a terminal state. Lets agents skip polling
	// GET /domains/{domain} for sending_status.
	EventDomainSendingVerified = "domain.sending_verified"
	EventDomainSendingFailed   = "domain.sending_failed"
	// Delivery feedback (decision 9 / Slice 4b): async outcome of an outbound
	// message, per recipient. domain.suppression_added is account-scoped
	// (despite the prefix) — fired when an address is auto-suppressed.
	EventEmailDelivered         = "email.delivered"
	EventEmailBounced           = "email.bounced"
	EventEmailComplained        = "email.complained"
	EventDomainSuppressionAdded = "domain.suppression_added"
	// Inbound trust policy (decision 10 / Slice 7): an inbound message did not
	// match the agent's ingestion policy (allowlist/domain). It is delivered but
	// flagged — operators get a signal, nothing is dropped.
	EventEmailFlagged = "email.flagged"
	// EventEmailBlocked fires when a message is refused by screening — the applied
	// action (gate ∨ scan) is `block`. Inbound: the message is accept-then-quarantined
	// (review_rejected, dropped, no human). Outbound: the send is refused (the caller
	// also gets a synchronous 4xx). It is the disposition event for the `block` action,
	// firing for BOTH directions; `reason_source` names the producer that drove it
	// (sender_gate / recipient_gate / inbound_scan / outbound_scan), mirroring the
	// protection_events audit vocabulary so a subscriber can correlate the two.
	EventEmailBlocked = "email.blocked"
	// EventEmailPendingReview fires when an inbound message is held for human review
	// (applied action = review → status pending_review). The same event fires for
	// outbound HITL holds (it is direction-aware) and carries the review TTL plus
	// reason_source (sender_gate / inbound_scan) so a subscriber can drive an inbound
	// review queue from push instead of polling.
	EventEmailPendingReview = "email.pending_review"
)

// AllEventTypes is the canonical allowlist of event names. Used by
// the slice-2 handler validation. Adding a new event type means
// adding a constant above AND extending this slice.
var AllEventTypes = []string{
	EventEmailReceived,
	EventEmailSent,
	EventEmailFailed,
	EventEmailDeferred,
	EventEmailReviewApproved,
	EventEmailReviewRejected,
	EventDomainSendingVerified,
	EventDomainSendingFailed,
	EventEmailDelivered,
	EventEmailBounced,
	EventEmailComplained,
	EventDomainSuppressionAdded,
	EventEmailFlagged,
	EventEmailBlocked,
	EventEmailPendingReview,
}

// IsValidEventType reports whether name is one of the catalog
// event types. Convenience wrapper for the handler-layer validator.
func IsValidEventType(name string) bool {
	for _, e := range AllEventTypes {
		if e == name {
			return true
		}
	}
	return false
}

// Event is the input to Publisher.Publish. Carries the routing keys
// (UserID, AgentID, ConversationID, Labels) needed to apply filters
// plus the Data payload that's serialized into the delivery row's
// event_payload JSONB.
//
// MessageID is optional — set when the event has an originating
// message row. Persisted on the delivery row with ON DELETE SET NULL
// so the messages janitor (10-day TTL) doesn't orphan the delivery.
type Event struct {
	// ID is a unique identifier for this event firing. Stable across
	// retries — receivers dedup on it.
	ID string

	// Type is one of the EventEmail* constants.
	Type string

	// CreatedAt is the time the event was published. Embedded in
	// the wire envelope so receivers can reason about staleness.
	CreatedAt time.Time

	// UserID is the owner — used to find matching webhooks. Routing
	// is strictly bounded to the owning user's subscribers; cross-
	// user delivery is impossible by construction.
	UserID string

	// AgentID, ConversationID, Labels are filter-matching keys. Each
	// is matched against the corresponding key in
	// WebhookFilters. Empty / nil here means "the event has no value
	// for this attribute" — see Publisher's null/empty semantics
	// (filters that REQUIRE a value while the event has none → skip).
	AgentID        string
	ConversationID string
	Labels         []string

	// MessageID is the originating message row, if any. May be empty
	// for events without a direct message backing (e.g.
	// email.pending_review before the held message gets promoted).
	MessageID string

	// Data is the event-specific payload. Wrapped in the envelope
	// {event, id, created_at, data} and serialized into the delivery
	// row's event_payload column.
	Data any
}

// NewEvent constructs an Event with a fresh ID and now() timestamp.
// Trigger sites use this rather than building struct literals so the
// ID format stays consistent (evt_<32-hex>).
func NewEvent(eventType, userID string, data any) Event {
	return Event{
		ID:        generateEventID(),
		Type:      eventType,
		CreatedAt: time.Now().UTC(),
		UserID:    userID,
		Data:      data,
	}
}

// Envelope is the wire shape sent in the HTTP body to webhook
// subscribers. Stable across retries — the event_payload JSON column
// stores the envelope verbatim and the delivery worker POSTs it
// unchanged.
// Type is the event-type discriminator. Named `type` on the wire to match
// the /v1/events REST resource (EventJSON.type) and the Stripe-style SDK
// `construct_event` helper — a webhook delivery body is the same shape as a
// GET /v1/events/{id} object, so consumers handle both identically.
type Envelope struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Data      any       `json:"data"`
}

// AsEnvelope returns the wire shape for serialization.
func (e Event) AsEnvelope() Envelope {
	return Envelope{
		Type:      e.Type,
		ID:        e.ID,
		CreatedAt: e.CreatedAt,
		Data:      e.Data,
	}
}

func generateEventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure means the OS RNG is broken; an
		// all-zero event ID would collide across firings. Panic so
		// the caller surfaces a 500 rather than silently emitting
		// non-unique IDs.
		panic(fmt.Sprintf("webhookpub: crypto/rand failed: %v", err))
	}
	return "evt_" + hex.EncodeToString(b)
}

// DeterministicEventID derives a stable event id from the trigger
// context. Per design §5.1, the input formula per event type is:
//
//	email.received: sha256(message_id || "|" || event_type)
//	email.sent:     sha256(message_id || "|" || event_type)
//	pending_review/review_approved/review_rejected: sha256(pending_msg_id || "|" || event_type)
//	future bounced/complained/delivered: sha256(message_id || "|" || event_type || "|" || ses_event_id)
//
// The "|" delimiter prevents accidental collisions where concatenated
// fields could be ambiguous (e.g. ("abc","def") vs ("abcdef","")).
//
// Returns "evt_" + first 32 hex chars of the sha256 digest (128 bits
// of entropy). Birthday collision probability at 1M events/day × 30
// days × 5 event types is ~3e-23 — negligible.
//
// Determinism is what makes the outbox write idempotent across MTA
// SMTP retries: the retried trigger produces the same id, and the
// outbox INSERT no-ops via ON CONFLICT (id) DO NOTHING.
func DeterministicEventID(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte("|"))
		}
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return "evt_" + hex.EncodeToString(sum[:16])
}

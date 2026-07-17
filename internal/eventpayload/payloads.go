// Package eventpayload defines the canonical typed `data` payloads for the
// STABLE webhook/WS event types (the pre-GA contract freeze, PR-2 of the
// event/webhook contract audit).
//
// One struct per stable event, used by EVERY builder that emits that event —
// the relay (email.received), the queue-first outbound path (email.sent /
// email.failed), the SES delivery-feedback consumer (email.delivered /
// email.bounced / email.complained, domain.suppression_added), and the
// sender-identity worker (domain.sending_verified / domain.sending_failed).
// The WebSocket channel reuses EmailReceivedData verbatim, so webhook and WS
// payloads for the same event are identical by construction.
//
// The structs are also registered as OpenAPI component schemas (see
// internal/httpapi's event-payload schema registration), and the committed
// golden fixtures under testdata/ are the cross-channel drift lock: the
// server-side builder tests and the TS/Python SDK payload tests all assert
// against the same fixture bytes.
//
// BETA events (email.flagged, email.blocked, email.review_requested,
// email.review_approved, email.review_rejected) intentionally stay
// map[string]any at their trigger sites — their payloads are open/unstable
// and must NOT be typed here until they are declared stable.
//
// This package stays a light leaf (time + internal/mailparse only) so the
// stdlib-oriented internal/delivery package can import it without dragging in
// webhookpub's storage dependencies.
package eventpayload

import (
	"time"

	"github.com/tokencanopy/e2a/internal/mailparse"
)

// AttachmentMeta is metadata for one attachment of the received message —
// never the bytes. Same vocabulary as the REST message view's attachment
// metadata (httpapi.AttachmentMetaView): `index` is the stable 0-based
// attachment index (document order) used to fetch the bytes via
// GET /v1/agents/{email}/messages/{id}/attachments/{index}.
type AttachmentMeta struct {
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	// SizeBytes is the DECODED attachment payload size — the byte count of the
	// file after the MIME Content-Transfer-Encoding (base64 / quoted-printable)
	// is undone: the size of the file a download yields. NOT the encoded
	// on-the-wire size inside the raw MIME, and NOT the message-level
	// size_bytes (which is the raw MIME length of the whole message).
	SizeBytes int `json:"size_bytes" doc:"DECODED attachment payload size in bytes (Content-Transfer-Encoding undone) — the size of the file a download yields, not its encoded size inside the raw MIME."`
	Index     int `json:"index" doc:"Stable 0-based attachment index (document order) — the fetch key for the attachment-bytes endpoint."`
}

// EmailReceivedData is the `data` payload of an email.received event. The
// event is a metadata-only NOTIFICATION, never a content carrier: fetch the
// full message (body + attachment bytes) from
// GET /v1/agents/{delivered_to}/messages/{message_id}.
type EmailReceivedData struct {
	MessageID  string `json:"message_id"`
	AgentEmail string `json:"agent_email" doc:"The receiving agent's email — its id and address (an agent's id IS its email)."`
	Direction  string `json:"direction" doc:"Always \"inbound\" on this event."`
	// ConversationID is empty for a thread-starting message.
	ConversationID string `json:"conversation_id,omitempty"`
	// From is the display/reply sender (prefers Reply-To). For the
	// authenticated, gated identity use AuthenticatedFrom.
	From string `json:"from" doc:"Display/reply sender (prefers Reply-To). For the verified identity use authenticated_from."`
	// AuthenticatedFrom is the From-header identity that SPF/DKIM/DMARC and
	// the inbound trust policy actually pertain to. It can differ from From
	// (which prefers Reply-To for reply routing): a consumer of an
	// allowlist/domain-gated agent MUST treat authenticated_from — not from —
	// as the gated/verified identity.
	AuthenticatedFrom string   `json:"authenticated_from" doc:"The From-header identity SPF/DKIM/DMARC verified — treat THIS (not from) as the gated identity."`
	To                []string `json:"to" nullable:"false"`
	CC                []string `json:"cc,omitempty" nullable:"false"`
	ReplyTo           []string `json:"reply_to,omitempty" nullable:"false"`
	// DeliveredTo is the agent address this copy was delivered to — a SCALAR
	// by construction: the relay emits one event per per-agent delivery, so
	// unlike the peer To/CC lists (the message's parsed headers) this is
	// always exactly one address. It is the fetch key for the message.
	DeliveredTo string `json:"delivered_to" doc:"The one agent address this per-agent copy was delivered to (scalar by construction — one event per delivery). Fetch key for the message."`
	Subject     string `json:"subject"`
	// AuthHeaders is the signed X-E2A-Auth-* attestation (HMAC-keyed by the
	// deployment secret, replay-stamped) that lets a subscriber INDEPENDENTLY
	// verify the inbound SPF/DKIM/DMARC verdict. Small signed metadata, so it
	// rides on the notification — persisted with the message, so the WS
	// drain-on-reconnect path re-emits the same attestation the live delivery
	// carried. Present-but-empty when the intake recorded none — never absent.
	AuthHeaders map[string]string `json:"auth_headers" nullable:"false"`
	ReceivedAt  time.Time         `json:"received_at" format:"date-time"`
	// Attachments is per-attachment METADATA (never bytes) parsed from the
	// raw message. Omitted when the message has none.
	Attachments []AttachmentMeta `json:"attachments,omitempty" nullable:"false"`
}

// EmailSentData is the `data` payload of an email.sent event — an outbound
// send reached its terminal sent state, either by provider acceptance or by
// atomic local loopback delivery.
type EmailSentData struct {
	MessageID      string `json:"message_id"`
	AgentEmail     string `json:"agent_email"`
	Direction      string `json:"direction" doc:"Always \"outbound\" on this event."`
	ConversationID string `json:"conversation_id,omitempty"`
	// ProviderMessageID is the provider-assigned (SES) message id — distinct
	// from the e2a message_id, and the correlation key for the async
	// delivered/bounced/complained feedback events. Omitted for providerless
	// local loopback delivery.
	ProviderMessageID string   `json:"provider_message_id,omitempty"`
	Method            string   `json:"method" doc:"Transport used for the send. Open set; tolerate unknown values. Known values: smtp, loopback."`
	From              string   `json:"from"`
	To                []string `json:"to" nullable:"false"`
	CC                []string `json:"cc,omitempty" nullable:"false"`
	BCC               []string `json:"bcc,omitempty" nullable:"false"`
	Subject           string   `json:"subject"`
	MessageType       string   `json:"message_type" doc:"Send kind. Open set; tolerate unknown values. Known values: send, reply, forward."`
}

// EmailFailedData is the `data` payload of an email.failed event — an
// outbound send terminally failed (retries exhausted / permanent reject).
// Same fields as EmailSentData minus provider_message_id (the provider never
// accepted it), plus the failure reason.
type EmailFailedData struct {
	MessageID      string   `json:"message_id"`
	AgentEmail     string   `json:"agent_email"`
	Direction      string   `json:"direction" doc:"Always \"outbound\" on this event."`
	ConversationID string   `json:"conversation_id,omitempty"`
	Method         string   `json:"method" doc:"Transport used for the send. Open set; tolerate unknown values. Known values: smtp."`
	From           string   `json:"from"`
	To             []string `json:"to" nullable:"false"`
	CC             []string `json:"cc,omitempty" nullable:"false"`
	BCC            []string `json:"bcc,omitempty" nullable:"false"`
	Subject        string   `json:"subject"`
	MessageType    string   `json:"message_type" doc:"Send kind. Open set; tolerate unknown values. Known values: send, reply, forward."`
	// Reason is the human-readable terminal failure diagnostic (e.g. the SMTP
	// response of a permanent reject).
	Reason string `json:"reason"`
	// ReasonCode is an optional machine-readable failure code. Omitted when
	// the send path has no classification beyond Reason.
	ReasonCode string `json:"reason_code,omitempty"`
	// Retryable reports whether re-submitting the same send could succeed.
	// Populated only where the send path genuinely knows it; omitted
	// otherwise (absent ≠ false).
	Retryable *bool `json:"retryable,omitempty"`
}

// EmailDeliveredData is the `data` payload of an email.delivered event — the
// recipient's server accepted an outbound message, per recipient (one event
// per (message, recipient)). The event type IS the outcome; there is no
// redundant `status` field.
type EmailDeliveredData struct {
	MessageID  string `json:"message_id"`
	AgentEmail string `json:"agent_email"`
	Direction  string `json:"direction" doc:"Always \"outbound\" on this event."`
	// DeliveredTo is the ONE recipient address this per-recipient outcome is
	// about (scalar by construction).
	DeliveredTo string `json:"delivered_to" doc:"The one recipient address this per-recipient outcome is about."`
	Subject     string `json:"subject,omitempty"`
	// SMTPDetail is the provider diagnostic string (e.g. the remote SMTP
	// response), when the feedback notification carried one.
	SMTPDetail string `json:"smtp_detail,omitempty"`
}

// EmailBouncedData is the `data` payload of an email.bounced event — an
// outbound message bounced for a recipient. EmailDeliveredData's fields plus
// the SES bounce classification.
type EmailBouncedData struct {
	MessageID   string `json:"message_id"`
	AgentEmail  string `json:"agent_email"`
	Direction   string `json:"direction" doc:"Always \"outbound\" on this event."`
	DeliveredTo string `json:"delivered_to" doc:"The one recipient address this per-recipient outcome is about."`
	Subject     string `json:"subject,omitempty"`
	SMTPDetail  string `json:"smtp_detail,omitempty"`
	// BounceType is the normalized SES bounce classification. Only a
	// permanent (hard) bounce auto-suppresses the address.
	//
	// Deliberately a CLOSED enum, unlike the evolving response vocabularies
	// (which are open sets): this is a normalized, exhaustive classification —
	// normalizeBounceType in internal/delivery/ses.go maps every provider
	// value into exactly these three, with `undetermined` as the guaranteed
	// catch-all — so the vocabulary cannot grow without a deliberate contract
	// change.
	BounceType string `json:"bounce_type" enum:"permanent,transient,undetermined"`
	// BounceSubType is the raw SES bounceSubType (e.g. General, NoEmail,
	// MailboxFull), when present.
	BounceSubType string `json:"bounce_sub_type,omitempty"`
}

// EmailComplainedData is the `data` payload of an email.complained event — a
// recipient marked an outbound message as spam (feedback-loop complaint).
// Same shape as EmailDeliveredData; SMTPDetail carries the complaint
// feedback type when present.
type EmailComplainedData struct {
	MessageID   string `json:"message_id"`
	AgentEmail  string `json:"agent_email"`
	Direction   string `json:"direction" doc:"Always \"outbound\" on this event."`
	DeliveredTo string `json:"delivered_to" doc:"The one recipient address this per-recipient outcome is about."`
	Subject     string `json:"subject,omitempty"`
	SMTPDetail  string `json:"smtp_detail,omitempty"`
}

// DomainSendingVerifiedData is the `data` payload of a domain.sending_verified
// event — the domain's async SES sending identity reached the verified
// terminal state.
type DomainSendingVerifiedData struct {
	Domain        string `json:"domain"`
	SendingStatus string `json:"sending_status" doc:"Terminal sending-identity status. Open set; tolerate unknown values. Known values: verified."`
}

// DomainSendingFailedData is the `data` payload of a domain.sending_failed
// event — the domain's async SES sending identity reached a failed terminal
// state.
type DomainSendingFailedData struct {
	Domain        string `json:"domain"`
	SendingStatus string `json:"sending_status" doc:"Terminal sending-identity status. Open set; tolerate unknown values. Known values: failed."`
	Reason        string `json:"reason,omitempty"`
}

// DomainSuppressionAddedData is the `data` payload of a
// domain.suppression_added event — an address was auto-suppressed after a
// hard bounce or complaint. Account-scoped despite the `domain.` prefix.
type DomainSuppressionAddedData struct {
	Address string `json:"address"`
	// Source is an OPEN set (evolving response-side vocabulary, like the REST
	// Suppression.source it mirrors — and the DB CHECK in
	// migrations/031_delivery_feedback.sql already admits source='manual').
	Source string `json:"source" doc:"How the suppression was created. Open set: new values may be added over time, so treat these as strings and tolerate unknown values. Known values: bounce, complaint."`
	Reason string `json:"reason,omitempty"`
	// MessageID is the outbound message whose feedback triggered the
	// suppression, when still known.
	MessageID string `json:"message_id,omitempty"`
}

// AttachmentMetadata extracts the per-attachment metadata of a raw RFC 5322
// message for EmailReceivedData.Attachments — the same extraction the REST
// message views use (mailparse.Attachments), so the event and the resource
// agree on indexes and sizes. Returns nil when the message has none.
func AttachmentMetadata(raw []byte) []AttachmentMeta {
	if len(raw) == 0 {
		return nil
	}
	atts := mailparse.Attachments(raw)
	if len(atts) == 0 {
		return nil
	}
	out := make([]AttachmentMeta, 0, len(atts))
	for i, a := range atts {
		out = append(out, AttachmentMeta{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			SizeBytes:   len(a.Data),
			Index:       i,
		})
	}
	return out
}

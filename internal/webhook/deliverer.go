package webhook

import (
	"time"
)

type Payload struct {
	MessageID      string `json:"message_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	From           string `json:"from"`
	// To is the parsed To: header from the inbound message — every fan-out
	// delivery for one inbound message carries the same list. Recipient is
	// this delivery's per-agent target (always one of the addressed agents,
	// not necessarily in To: when the agent was Bcc'd).
	To []string `json:"to"`
	CC []string `json:"cc,omitempty"`
	// ReplyTo is the parsed Reply-To: header (RFC 5322 § 3.6.2 — list, single
	// value is typical but multi is legal). Empty list when the header is
	// absent; the relay never silently falls back to From: so consumers can
	// distinguish "sender didn't request a different reply mailbox" from
	// "sender explicitly named these mailboxes".
	ReplyTo     []string          `json:"reply_to,omitempty"`
	Recipient   string            `json:"recipient"`
	RawMessage  []byte            `json:"raw_message"`
	AuthHeaders map[string]string `json:"auth_headers"`
	ReceivedAt  time.Time         `json:"received_at"`
}

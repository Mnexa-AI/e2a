package relay

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
	"github.com/Mnexa-AI/e2a/internal/eventpayload/goldenassert"
	"github.com/Mnexa-AI/e2a/internal/identity"
)

// TestBuildEmailReceivedPayload_Shape verifies the email.received event is a
// metadata-only notification: it carries the routing/identity fields, the signed
// auth_headers attestation, attachment METADATA, and the message_id + recipient
// fetch keys — but NOT the message body (raw_message), which a subscriber
// fetches from GET /v1/agents/{recipient}/messages/{message_id}.
func TestBuildEmailReceivedPayload_Shape(t *testing.T) {
	threadInfo := threadInfo{
		To:      []string{"bot@example.com"},
		CC:      []string{"cc@example.com"},
		ReplyTo: []string{"reply@example.com"},
	}
	agent := &identity.AgentIdentity{
		ID:     "bot@example.com",
		Domain: "example.com",
	}
	authHeaders := map[string]string{"X-E2A-Auth-Verified": "true"}

	payload := buildEmailReceivedPayload(
		"msg_123",
		"conv_abc",
		"reply@example.com", // displaySender (Reply-To preferred)
		"alice@example.com", // authenticatedFrom (From-header identity)
		"bot@example.com",
		"Hello",
		threadInfo,
		authHeaders,
		agent,
		time.Now().UTC(),
		nil,
	)

	// delivered_to is the fetch key (agent address) for messages.get(delivered_to, id).
	if payload.DeliveredTo != "bot@example.com" {
		t.Errorf("delivered_to = %v, want bot@example.com (fetch key)", payload.DeliveredTo)
	}
	if payload.MessageID != "msg_123" {
		t.Errorf("message_id = %v", payload.MessageID)
	}
	// from is the display sender (Reply-To); authenticated_from is the From
	// identity the policy + auth verdict pertain to — they can differ.
	if payload.From != "reply@example.com" {
		t.Errorf("from = %v, want reply@example.com (display sender)", payload.From)
	}
	if payload.AuthenticatedFrom != "alice@example.com" {
		t.Errorf("authenticated_from = %v, want alice@example.com (From identity)", payload.AuthenticatedFrom)
	}
	// agent_email is the single flat agent reference (an agent's id IS its email).
	if payload.AgentEmail != "bot@example.com" {
		t.Errorf("agent_email = %v, want bot@example.com", payload.AgentEmail)
	}
	if payload.Direction != "inbound" {
		t.Errorf("direction = %v, want inbound", payload.Direction)
	}
}

// TestBuildEmailReceivedPayload_Golden asserts the relay builder's marshaled
// output byte-for-byte matches the committed golden fixture — the same file
// the eventpayload envelope test, the WS channel test, and the TS/Python SDK
// tests assert against (the cross-channel drift lock).
func TestBuildEmailReceivedPayload_Golden(t *testing.T) {
	receivedAt := time.Date(2026, 7, 1, 10, 30, 0, 123456789, time.UTC)
	ti := threadInfo{
		To:      []string{"support@agents.example.com"},
		CC:      []string{"ops@customer.example.com"},
		ReplyTo: []string{"reply@customer.example.com"},
	}
	agent := &identity.AgentIdentity{ID: "support@agents.example.com", Domain: "agents.example.com"}
	authHeaders := map[string]string{
		"X-E2A-Auth-Sender":   "alice@customer.example.com",
		"X-E2A-Auth-Verified": "true",
	}

	// A raw MIME message with one 12345-byte application/pdf attachment —
	// matching the fixture's attachment metadata via the SAME extraction the
	// REST message views use.
	pdf := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 12345)))
	raw := []byte("From: alice@customer.example.com\r\n" +
		"To: support@agents.example.com\r\n" +
		"Subject: Order #1234 delayed\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b1\"\r\n" +
		"\r\n" +
		"--b1\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"hello\r\n" +
		"--b1\r\n" +
		"Content-Type: application/pdf; name=\"invoice.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		pdf + "\r\n" +
		"--b1--\r\n")

	payload := buildEmailReceivedPayload(
		"msg_01h2xcejqtf2nbrexx3vqjhp41",
		"conv_9f8e7d6c",
		"reply@customer.example.com",
		"alice@customer.example.com",
		"support@agents.example.com",
		"Order #1234 delayed",
		ti,
		authHeaders,
		agent,
		receivedAt,
		eventpayload.AttachmentMetadata(raw),
	)
	goldenassert.Data(t, "../eventpayload/testdata/email.received.json", payload)
}

// TestBuildEmailReceivedPayload_GoldenMinimal is the presence-semantics lock:
// the builder fed only the REQUIRED inputs (no conversation, no cc/reply_to,
// no auth attestation, no attachments) must marshal byte-identical to the
// committed required-fields-only fixture — so a flipped `omitempty` (optional
// field emitted when unset, or a required field dropped when empty) fails
// here, not in a consumer.
func TestBuildEmailReceivedPayload_GoldenMinimal(t *testing.T) {
	receivedAt := time.Date(2026, 7, 1, 10, 30, 0, 123456789, time.UTC)
	payload := buildEmailReceivedPayload(
		"msg_01h2xcejqtf2nbrexx3vqjhp41",
		"", // thread-starting message: no conversation_id
		"reply@customer.example.com",
		"", // unauthenticated: present-but-empty, never absent
		"support@agents.example.com",
		"Order #1234 delayed",
		threadInfo{To: []string{"support@agents.example.com"}},
		nil, // no auth attestation → auth_headers must serialize as {}
		&identity.AgentIdentity{ID: "support@agents.example.com", Domain: "agents.example.com"},
		receivedAt,
		nil, // no attachments → field must be ABSENT
	)
	goldenassert.Data(t, "../eventpayload/testdata/email.received.min.json", payload)
}

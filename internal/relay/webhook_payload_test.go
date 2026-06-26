package relay

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// TestBuildEmailReceivedPayload_Shape verifies the email.received event is a
// metadata-only notification: it carries the routing/identity fields, the signed
// auth_headers attestation, and the message_id + recipient fetch keys — but NOT
// the message body (raw_message), which a subscriber fetches from
// GET /v1/agents/{recipient}/messages/{message_id}.
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
	)

	// Metadata + fetch keys + the signed auth_headers attestation are present.
	for _, key := range []string{
		"message_id", "conversation_id", "agent",
		"from", "authenticated_from", "to", "cc", "reply_to", "recipient",
		"subject", "auth_headers", "received_at",
	} {
		if _, ok := payload[key]; !ok {
			t.Errorf("payload missing %q", key)
		}
	}

	// The body is NOT on the notification — it is fetched from REST.
	if _, ok := payload["raw_message"]; ok {
		t.Errorf("metadata-only payload must not carry %q", "raw_message")
	}

	// recipient is the fetch key (agent address) for messages.get(recipient, id).
	if payload["recipient"] != "bot@example.com" {
		t.Errorf("recipient = %v, want bot@example.com (fetch key)", payload["recipient"])
	}

	if payload["message_id"] != "msg_123" {
		t.Errorf("message_id = %v", payload["message_id"])
	}
	// from is the display sender (Reply-To); authenticated_from is the From
	// identity the policy + auth verdict pertain to — they can differ.
	if payload["from"] != "reply@example.com" {
		t.Errorf("from = %v, want reply@example.com (display sender)", payload["from"])
	}
	if payload["authenticated_from"] != "alice@example.com" {
		t.Errorf("authenticated_from = %v, want alice@example.com (From identity)", payload["authenticated_from"])
	}
	agentObj, ok := payload["agent"].(map[string]interface{})
	if !ok {
		t.Fatalf("agent is not a map: %T", payload["agent"])
	}
	if agentObj["id"] != "bot@example.com" {
		t.Errorf("agent.id = %v", agentObj["id"])
	}
	if agentObj["domain"] != "example.com" {
		t.Errorf("agent.domain = %v", agentObj["domain"])
	}
}

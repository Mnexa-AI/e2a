package relay

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// TestBuildEmailReceivedPayload_Shape verifies the data envelope sent
// to webhooks-as-a-resource subscribers carries the same fields the
// legacy webhook.Payload exposes, so a customer writing one webhook
// handler against either model sees the same shape.
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

	payload := buildEmailReceivedPayload(
		"msg_123",
		"conv_abc",
		"reply@example.com", // displaySender (Reply-To preferred)
		"alice@example.com", // authenticatedFrom (From-header identity)
		"bot@example.com",
		"Hello",
		threadInfo,
		[]byte("raw RFC 5322 bytes"),
		map[string]string{
			"X-E2A-Auth-Verified": "true",
		},
		agent,
	)

	for _, key := range []string{
		"message_id", "conversation_id", "agent",
		"from", "authenticated_from", "to", "cc", "reply_to", "recipient",
		"subject", "raw_message", "auth_headers", "received_at",
	} {
		if _, ok := payload[key]; !ok {
			t.Errorf("payload missing %q", key)
		}
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

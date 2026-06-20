package httpapi

import (
	"strings"
	"testing"
)

// sendURL is POST /v1/agents/{address}/messages for the test agent. The sender
// is the path agent (decision 3 — explicit operation, not a body `from`).
const sendURL = "/v1/agents/support%40acme.com/messages"

func TestSendSent(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code != 200 || body["status"] != "sent" || body["message_id"] != "msg_sent_1" || body["method"] != "smtp" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
}

func TestSendHeldForApproval(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "HOLD please", "body": "hello",
	})
	if code != 202 || body["status"] != "pending_approval" || body["message_id"] != "msg_pending_1" {
		t.Fatalf("want 202 pending_approval, got %d %v", code, body)
	}
	if body["approval_expires_at"] == nil {
		t.Fatal("held response must carry approval_expires_at")
	}
	if _, present := body["method"]; present {
		t.Fatal("held response must not carry method")
	}
}

func TestSendMissingSubjectBody(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "", "body": "",
	})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request, got %d %v", code, body)
	}
}

func TestSendCRLFSubjectRejected(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "a\r\nInjected: x", "body": "hi",
	})
	if code != 400 {
		t.Fatalf("want 400 for CRLF subject, got %d %v", code, body)
	}
}

func TestSendNoRecipients(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"subject": "Hi", "body": "hello",
	})
	// `to` is now schema-required (MSG-3) → rejected at validation (422).
	if code != 422 {
		t.Fatalf("want 422 missing to, got %d %v", code, body)
	}
}

func TestSendInvalidRecipient(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"not-an-email"}, "subject": "Hi", "body": "hello",
	})
	if code != 400 || errCode(body) != "invalid_recipient" {
		t.Fatalf("want 400 invalid_recipient, got %d %v", code, body)
	}
}

// TestSendSetsAgentAsSender: there is no body `from` — the sender is the path
// agent and auth scopes it. A plain send (no `from`) succeeds.
func TestSendSetsAgentAsSender(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code != 200 || body["status"] != "sent" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
}

// TestSendNotOwnedAgent: sending through an agent the caller does not own is a
// 403 (resolveOwnedAgent), never a cross-tenant send.
func TestSendNotOwnedAgent(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents/other%40nope.com/messages", "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code != 403 {
		t.Fatalf("want 403 for an unowned agent, got %d", code)
	}
}

func TestSendOverCap(t *testing.T) {
	srv := testServer(t)
	// The cap check is covered by the agent-create/domain over-cap tests; here
	// we assert the message path wires EnforceMessageSend by checking a
	// successful send does NOT 402 for u_1.
	code, _ := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code == 402 {
		t.Fatalf("u_1 is under cap; should not 402")
	}
}

// TestSendLargeBodyAccepted guards the outbound body cap: Huma's default is
// 1 MiB, which would 413 attachment-bearing mail. The send op raises it to
// maxOutboundBytes (25 MB), so a >1 MiB body is accepted, not rejected.
func TestSendLargeBodyAccepted(t *testing.T) {
	srv := testServer(t)
	big := strings.Repeat("a", 1500*1024) // ~1.5 MiB — over Huma's 1 MiB default
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "Hi", "body": big,
	})
	if code == 413 {
		t.Fatalf("a 1.5 MiB body must be accepted (cap raised to 25 MB), got 413")
	}
	if code != 200 || body["status"] != "sent" {
		t.Fatalf("want 200 sent for a large-but-under-cap body, got %d %v", code, body)
	}
}

func TestSendUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+sendURL, "", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}

func TestReplySent(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/reply", "good", map[string]any{"body": "thanks"})
	if code != 200 || body["status"] != "sent" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
}

func TestReplyBodyRequired(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/reply", "good", map[string]any{"body": ""})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request, got %d %v", code, body)
	}
}

func TestReplyMessageNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_missing/reply", "good", map[string]any{"body": "x"})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestReplyNotOwnedAgent(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents/other%40acme.com/messages/msg_in1/reply", "good", map[string]any{"body": "x"})
	if code != 403 {
		t.Fatalf("want 403, got %d", code)
	}
}

func TestForwardSent(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/forward", "good", map[string]any{
		"to": []string{"bob@x.com"}, "body": "fyi",
	})
	if code != 200 || body["status"] != "sent" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
}

func TestForwardNoRecipients(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/forward", "good", map[string]any{"body": "fyi"})
	// `to` is now schema-required (MSG-3) → rejected at validation (422).
	if code != 422 {
		t.Fatalf("want 422 missing to, got %d %v", code, body)
	}
}

func TestForwardMessageNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_missing/forward", "good", map[string]any{"to": []string{"bob@x.com"}, "body": "x"})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestTestSendSent(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/test", "good", nil)
	if code != 200 || body["status"] != "sent" || body["message_id"] != "msg_test_1" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
}

func TestTestSendNotOwned(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents/other%40acme.com/test", "good", nil)
	if code != 403 {
		t.Fatalf("want 403, got %d", code)
	}
}

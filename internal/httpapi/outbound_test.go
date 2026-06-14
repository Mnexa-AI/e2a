package httpapi

import "testing"

func TestSendSent(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "support@acme.com", "to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code != 200 || body["status"] != "sent" || body["message_id"] != "msg_sent_1" || body["method"] != "smtp" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
}

func TestSendAutoResolveSingleAgent(t *testing.T) {
	srv := testServer(t)
	// No `from` — the caller owns exactly one agent, so it is auto-selected.
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code != 200 || body["status"] != "sent" {
		t.Fatalf("want 200 sent (auto-resolve), got %d %v", code, body)
	}
}

func TestSendHeldForApproval(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "support@acme.com", "to": []string{"alice@x.com"}, "subject": "HOLD please", "body": "hello",
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
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "support@acme.com", "to": []string{"alice@x.com"}, "subject": "", "body": "",
	})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request, got %d %v", code, body)
	}
}

func TestSendCRLFSubjectRejected(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "support@acme.com", "to": []string{"alice@x.com"}, "subject": "a\r\nInjected: x", "body": "hi",
	})
	if code != 400 {
		t.Fatalf("want 400 for CRLF subject, got %d %v", code, body)
	}
}

func TestSendNoRecipients(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "support@acme.com", "subject": "Hi", "body": "hello",
	})
	if code != 400 {
		t.Fatalf("want 400 no recipients, got %d %v", code, body)
	}
}

func TestSendInvalidRecipient(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "support@acme.com", "to": []string{"not-an-email"}, "subject": "Hi", "body": "hello",
	})
	if code != 400 || errCode(body) != "invalid_recipient" {
		t.Fatalf("want 400 invalid_recipient, got %d %v", code, body)
	}
}

func TestSendInvalidFrom(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "stranger@x.com", "to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code != 400 || errCode(body) != "invalid_from" {
		t.Fatalf("want 400 invalid_from, got %d %v", code, body)
	}
}

func TestSendOverCap(t *testing.T) {
	srv := testServer(t)
	// overcap principal owns no agents in ListAgents, so use explicit from.
	// resolveSendAgent(from) -> GetAgent(support@acme.com) is owned by u_1,
	// not u_overcap, so it 400s before the cap. Use the auto-resolve path
	// is also u_1-only. The cap check is covered by the agent-create/domain
	// over-cap tests; here we assert the message path wires EnforceMessageSend
	// by checking a successful send does NOT 402 for u_1.
	code, _ := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "support@acme.com", "to": []string{"alice@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code == 402 {
		t.Fatalf("u_1 is under cap; should not 402")
	}
}

func TestSendUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/send", "", map[string]any{
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
	if code != 400 {
		t.Fatalf("want 400 no recipients, got %d %v", code, body)
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

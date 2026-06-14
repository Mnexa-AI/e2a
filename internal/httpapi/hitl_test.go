package httpapi

import "testing"

func TestApproveSent(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_pending/approve", "good", map[string]any{})
	if code != 200 || body["status"] != "sent" || body["message_id"] != "msg_pending" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
	if body["provider_message_id"] != "<prov@ses>" {
		t.Fatalf("expected provider_message_id, got %v", body)
	}
}

func TestApproveWithOverrides(t *testing.T) {
	srv := testServer(t)
	// reviewer overrides ride in the body
	code, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_pending/approve", "good",
		map[string]any{"subject": "edited", "to": []string{"a@x.com"}})
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
}

func TestApproveNotPending(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_notpending/approve", "good", map[string]any{})
	if code != 409 || errCode(body) != "message_not_pending" {
		t.Fatalf("want 409 message_not_pending, got %d %v", code, body)
	}
}

func TestApproveNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_missing/approve", "good", map[string]any{})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestApproveNotOwnedAgent(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents/other%40acme.com/messages/msg_pending/approve", "good", map[string]any{})
	if code != 403 {
		t.Fatalf("want 403, got %d", code)
	}
}

func TestReject(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_pending/reject", "good", map[string]any{"reason": "not now"})
	if code != 200 || body["status"] != "rejected" || body["rejection_reason"] != "not now" {
		t.Fatalf("want 200 rejected, got %d %v", code, body)
	}
}

func TestRejectNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_missing/reject", "good", map[string]any{})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

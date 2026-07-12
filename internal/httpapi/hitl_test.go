package httpapi

import "testing"

// These exercise the account-scoped review queue approve/reject dispatch
// (/v1/reviews/{id}/approve|reject) — the only approve/reject path since the
// deprecated agent-path (/v1/agents/{email}/messages/{id}/approve|reject) was
// removed in the pre-GA vocabulary freeze.

func TestApproveSent(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_pending/approve", "good", map[string]any{})
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
	code, _ := postJSON(t, srv.URL+"/v1/reviews/msg_pending/approve", "good",
		map[string]any{"subject": "edited", "to": []string{"a@x.com"}})
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
}

func TestApproveNotPending(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_notpending/approve", "good", map[string]any{})
	if code != 409 || errCode(body) != "message_not_pending" {
		t.Fatalf("want 409 message_not_pending, got %d %v", code, body)
	}
}

func TestApproveNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/reviews/msg_missing/approve", "good", map[string]any{})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestReject(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_pending/reject", "good", map[string]any{"reason": "not now"})
	if code != 200 || body["status"] != "review_rejected" || body["rejection_reason"] != "not now" {
		t.Fatalf("want 200 rejected, got %d %v", code, body)
	}
}

func TestRejectNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/reviews/msg_missing/reject", "good", map[string]any{})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

// --- inbound review release (slice 3) ---

// TestApproveInboundReleases asserts that approving an INBOUND hold releases it
// (status review_approved, no SES send fields) rather than running the outbound
// send path.
func TestApproveInboundReleases(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_in_held/approve", "good", map[string]any{})
	if code != 200 || body["status"] != "review_approved" || body["message_id"] != "msg_in_held" {
		t.Fatalf("want 200 review_approved, got %d %v", code, body)
	}
	// A release is not a send — no provider_message_id / method / edited.
	if _, ok := body["provider_message_id"]; ok {
		t.Fatalf("inbound release must not carry provider_message_id: %v", body)
	}
	if _, ok := body["edited"]; ok {
		t.Fatalf("inbound release must not carry edited: %v", body)
	}
}

// TestRejectInboundDrops asserts that rejecting an INBOUND hold drops it
// (status review_rejected) with the reviewer reason echoed.
func TestRejectInboundDrops(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_in_held/reject", "good", map[string]any{"reason": "prompt injection"})
	if code != 200 || body["status"] != "review_rejected" || body["rejection_reason"] != "prompt injection" {
		t.Fatalf("want 200 review_rejected, got %d %v", code, body)
	}
}

// TestApproveInboundNotPending asserts the compare-and-set conflict (the hold was
// already resolved by a human or the TTL sweep) surfaces as 409, not a double
// release.
func TestApproveInboundNotPending(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_in_notpending/approve", "good", map[string]any{})
	if code != 409 || errCode(body) != "message_not_pending" {
		t.Fatalf("want 409 message_not_pending, got %d %v", code, body)
	}
}

// TestRejectInboundNotPending mirrors the approve conflict on the reject path.
func TestRejectInboundNotPending(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_in_notpending/reject", "good", map[string]any{"reason": "x"})
	if code != 409 || errCode(body) != "message_not_pending" {
		t.Fatalf("want 409 message_not_pending, got %d %v", code, body)
	}
}

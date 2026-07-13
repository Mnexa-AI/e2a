package httpapi

import (
	"fmt"
	"testing"
)

// MED-4 — a send whose total to+cc+bcc recipient count exceeds the cap (50)
// is rejected before any delivery. With the GA maxItems:"50" schema tags, a
// single field over 50 is now caught earlier — at the Huma schema-validation
// layer — as a 422 invalid_request (the same canonical validation code as 400,
// just the "well-formed but unprocessable" status). The runtime combined-count
// cap (to+cc+bcc > 50 with each field ≤ 50) is covered by
// TestSendRecipientCapCountsAllFields below and still returns 400
// too_many_recipients.
func TestSendTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	to := make([]string, 51)
	for i := range to {
		to[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": to, "subject": "Hi", "text": "hello",
	})
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("want 422 invalid_request (schema-level maxItems), got %d %v", code, body)
	}
}

// The cap counts to+cc+bcc together, so 50 split across the three fields must
// pass and 51 split across them must fail. Each field is ≤ 50, so the schema
// layer does NOT fire — the runtime recipientCountError fires instead, returning
// 400 too_many_recipients (the combined-count path).
func TestSendRecipientCapCountsAllFields(t *testing.T) {
	srv := testServer(t)
	mk := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d@x.com", prefix, i)
		}
		return out
	}
	// 20 + 20 + 11 = 51 -> over the cap (each field ≤ 50, so runtime fires).
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": mk("t", 20), "cc": mk("c", 20), "bcc": mk("b", 11), "subject": "Hi", "text": "hello",
	})
	if code != 400 || errCode(body) != "too_many_recipients" {
		t.Fatalf("split over cap: want 400 too_many_recipients, got %d %v", code, body)
	}
	// 20 + 20 + 10 = 50 -> at the cap, allowed (subject HOLD avoided -> sent).
	code, _ = postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": mk("t", 20), "cc": mk("c", 20), "bcc": mk("b", 10), "subject": "Hi", "text": "hello",
	})
	if code != 200 {
		t.Fatalf("at cap: want 200, got %d", code)
	}
}

// The forward path enforces the same schema-level maxItems cap (>50 in a single
// field → 422). The runtime combined-count path is shared with send via
// recipientCountError.
func TestForwardTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	to := make([]string, 51)
	for i := range to {
		to[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/forward", "good", map[string]any{
		"to": to, "text": "fwd",
	})
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("forward: want 422 invalid_request (schema-level maxItems), got %d %v", code, body)
	}
}

// The reply path enforces the same schema-level maxItems cap on cc (>50 in a
// single field → 422). The runtime combined-count path is shared via
// recipientCountError.
func TestReplyTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	cc := make([]string, 51)
	for i := range cc {
		cc[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/reply", "good", map[string]any{
		"text": "re", "cc": cc,
	})
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("reply: want 422 invalid_request (schema-level maxItems), got %d %v", code, body)
	}
}

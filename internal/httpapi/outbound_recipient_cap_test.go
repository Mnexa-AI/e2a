package httpapi

import (
	"fmt"
	"testing"
)

// MED-4 — a send whose total to+cc+bcc recipient count exceeds the cap (50)
// must be rejected with 400 too_many_recipients before any delivery.
func TestSendTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	to := make([]string, 51)
	for i := range to {
		to[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": to, "subject": "Hi", "body": "hello",
	})
	if code != 400 || errCode(body) != "too_many_recipients" {
		t.Fatalf("want 400 too_many_recipients, got %d %v", code, body)
	}
}

// The cap counts to+cc+bcc together, so 50 split across the three fields must
// pass and 51 split across them must fail.
func TestSendRecipientCapCountsAllFields(t *testing.T) {
	srv := testServer(t)
	mk := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d@x.com", prefix, i)
		}
		return out
	}
	// 20 + 20 + 11 = 51 -> over the cap.
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": mk("t", 20), "cc": mk("c", 20), "bcc": mk("b", 11), "subject": "Hi", "body": "hello",
	})
	if code != 400 || errCode(body) != "too_many_recipients" {
		t.Fatalf("split over cap: want 400 too_many_recipients, got %d %v", code, body)
	}
	// 20 + 20 + 10 = 50 -> at the cap, allowed (subject HOLD avoided -> sent).
	code, _ = postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": mk("t", 20), "cc": mk("c", 20), "bcc": mk("b", 10), "subject": "Hi", "body": "hello",
	})
	if code != 200 {
		t.Fatalf("at cap: want 200, got %d", code)
	}
}

// The forward validator must enforce the same cap.
func TestForwardTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	to := make([]string, 51)
	for i := range to {
		to[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/forward", "good", map[string]any{
		"to": to, "body": "fwd",
	})
	if code != 400 || errCode(body) != "too_many_recipients" {
		t.Fatalf("forward: want 400 too_many_recipients, got %d %v", code, body)
	}
}

// The reply validator must enforce the same cap on user-supplied cc+bcc.
func TestReplyTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	cc := make([]string, 51)
	for i := range cc {
		cc[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/reply", "good", map[string]any{
		"body": "re", "cc": cc,
	})
	if code != 400 || errCode(body) != "too_many_recipients" {
		t.Fatalf("reply: want 400 too_many_recipients, got %d %v", code, body)
	}
}

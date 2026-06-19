package httpapi

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// Inbound messages get a parsed view (quoted reply stripped); outbound don't.
func TestMessageViewParsed(t *testing.T) {
	raw := []byte("From: a@x.com\r\nContent-Type: text/plain\r\n\r\nMy reply.\r\n\r\n" +
		"On Mon, Bob <b@y.com> wrote:\r\n> quoted original\r\n")

	in := messageViewFromIdentity(&identity.Message{
		ID: "msg_1", Direction: "inbound", Sender: "a@x.com", RawMessage: raw,
	})
	if in.Parsed == nil {
		t.Fatal("inbound message should have a parsed view")
	}
	if in.Parsed.Text != "My reply." {
		t.Fatalf("parsed.text = %q, want quoted-stripped %q", in.Parsed.Text, "My reply.")
	}

	out := messageViewFromIdentity(&identity.Message{
		ID: "msg_2", Direction: "outbound", RawMessage: raw,
	})
	if out.Parsed != nil {
		t.Fatalf("outbound message must not carry a parsed view, got %+v", out.Parsed)
	}

	// Inbound with no raw → no parsed view (nothing to parse).
	none := messageViewFromIdentity(&identity.Message{ID: "msg_3", Direction: "inbound"})
	if none.Parsed != nil {
		t.Fatal("inbound with empty raw should have no parsed view")
	}
}

// Fix #1: MessageView (detail) is a superset of MessageSummaryView (list) — it
// must carry webhook_status, webhook_error and size_bytes for both directions.
func TestMessageViewCarriesWebhookStatusAndSize(t *testing.T) {
	v := messageViewFromIdentity(&identity.Message{
		ID: "msg_wh", Direction: "inbound", Sender: "a@x.com",
		RawMessage:    []byte("hello"),
		WebhookStatus: "failed", WebhookError: "connection refused", SizeBytes: 5,
	})
	if v.WebhookStatus != "failed" {
		t.Errorf("webhook_status = %q, want %q", v.WebhookStatus, "failed")
	}
	if v.WebhookError != "connection refused" {
		t.Errorf("webhook_error = %q, want %q", v.WebhookError, "connection refused")
	}
	if v.SizeBytes != 5 {
		t.Errorf("size_bytes = %d, want 5", v.SizeBytes)
	}
}

// Held-draft (pending_approval) messages expose body_text/html via Body, the
// second representation the unified read serves (sent/inbound use raw_message).
func TestMessageViewHeldDraftBody(t *testing.T) {
	draft := messageViewFromIdentity(&identity.Message{
		ID: "msg_d", Direction: "outbound", Status: "pending_approval",
		BodyText: "draft text", BodyHTML: "<p>draft</p>",
	})
	if draft.Body == nil || draft.Body.Text != "draft text" || draft.Body.HTML != "<p>draft</p>" {
		t.Fatalf("held draft should expose body, got %+v", draft.Body)
	}
	// A sent/inbound message (no body columns) has no Body.
	sent := messageViewFromIdentity(&identity.Message{ID: "msg_s", Direction: "inbound", RawMessage: []byte("From: a@x\r\n\r\nhi")})
	if sent.Body != nil {
		t.Fatalf("inbound/sent must not carry a draft Body, got %+v", sent.Body)
	}
}

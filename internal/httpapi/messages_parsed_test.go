package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/tokencanopy/e2a/internal/emailauth"
	"github.com/tokencanopy/e2a/internal/identity"
)

func TestMessageViewCanonicalAuthenticationShape(t *testing.T) {
	domain := "example.com"
	authentication := &emailauth.Authentication{
		SPF:   emailauth.SPFResult{Status: emailauth.StatusNone},
		DKIM:  []emailauth.DKIMResult{},
		DMARC: emailauth.DMARCResult{Status: emailauth.StatusPass, Domain: &domain, AlignedBy: []emailauth.AlignmentMechanism{emailauth.AlignedByDKIM}},
	}
	view := messageViewFromIdentity(&identity.Message{
		ID: "msg_auth", Direction: "inbound", HeaderFrom: "alice@example.com", EnvelopeFrom: "bounce@example.com", Authentication: authentication,
	})
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["verified_domain"] != "example.com" {
		t.Fatalf("verified_domain = %#v, want example.com", got["verified_domain"])
	}
	for _, key := range []string{"header_from", "envelope_from", "authentication"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing %s in %s", key, b)
		}
	}
	for _, retired := range []string{"from", "authenticated_from", "auth", "auth_headers"} {
		if _, ok := got[retired]; ok {
			t.Fatalf("retired field %s present in %s", retired, b)
		}
	}

	outbound, err := json.Marshal(messageViewFromIdentity(&identity.Message{ID: "msg_out", Direction: "outbound", Sender: "agent@example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	var outboundShape map[string]any
	if err := json.Unmarshal(outbound, &outboundShape); err != nil {
		t.Fatal(err)
	}
	if value, ok := outboundShape["authentication"]; !ok || value != nil {
		t.Fatalf("outbound authentication = %#v, present=%v", value, ok)
	}
}

func TestMessageSummaryCarriesDecisionWithoutAuthenticationEvidence(t *testing.T) {
	domain := "example.com"
	authentication := &emailauth.Authentication{
		SPF: emailauth.SPFResult{Status: emailauth.StatusNone}, DKIM: []emailauth.DKIMResult{},
		DMARC: emailauth.DMARCResult{Status: emailauth.StatusPass, Domain: &domain, AlignedBy: []emailauth.AlignmentMechanism{emailauth.AlignedByDKIM}},
	}
	b, err := json.Marshal(messageSummaryFromIdentity(identity.Message{
		ID: "msg_summary", Direction: "inbound", HeaderFrom: "alice@example.com", Authentication: authentication,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["verified_domain"] != domain {
		t.Fatalf("verified_domain = %#v, want %q", got["verified_domain"], domain)
	}
	if _, exists := got["authentication"]; exists {
		t.Fatalf("summary must omit full authentication evidence: %s", b)
	}
}

// TestMessageViewReplyToDirectionGated: the wire `reply_to` field means the
// PARSED INBOUND Reply-To header. The same identity column now doubles as
// storage for an outbound override (so held sends survive the approval
// recompose), which must NOT leak into the view. Inbound preserves it; outbound
// is always [] regardless of the stored override.
func TestMessageViewReplyToDirectionGated(t *testing.T) {
	inbound := messageViewFromIdentity(&identity.Message{
		ID: "msg_in", Direction: "inbound", ReplyTo: []string{"real-reply@x.com"},
	})
	if len(inbound.ReplyTo) != 1 || inbound.ReplyTo[0] != "real-reply@x.com" {
		t.Errorf("inbound view ReplyTo = %v, want [real-reply@x.com]", inbound.ReplyTo)
	}

	outbound := messageViewFromIdentity(&identity.Message{
		ID: "msg_out", Direction: "outbound", ReplyTo: []string{"override@acme.com"},
	})
	if len(outbound.ReplyTo) != 0 {
		t.Errorf("outbound view ReplyTo = %v, want [] (override must not leak)", outbound.ReplyTo)
	}

	summary := messageSummaryFromIdentity(identity.Message{
		ID: "msg_out", Direction: "outbound", ReplyTo: []string{"override@acme.com"},
	})
	if len(summary.ReplyTo) != 0 {
		t.Errorf("outbound summary ReplyTo = %v, want []", summary.ReplyTo)
	}
}

// Any message carrying raw MIME gets a parsed view (quoted reply stripped) —
// inbound and sent outbound alike. Held drafts (no raw) don't.
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

	// Sent outbound carries raw MIME while retained draft columns preserve the
	// accepted outbound content,
	// so it now gets the same parsed view — fixing the empty-body thread render.
	out := messageViewFromIdentity(&identity.Message{
		ID: "msg_2", Direction: "outbound", RawMessage: raw,
	})
	if out.Parsed == nil {
		t.Fatal("sent outbound (with raw MIME) should have a parsed view")
	}
	if out.Parsed.Text != "My reply." {
		t.Fatalf("outbound parsed.text = %q, want %q", out.Parsed.Text, "My reply.")
	}

	// Messages with no raw → no parsed view (nothing to parse).
	none := messageViewFromIdentity(&identity.Message{ID: "msg_3", Direction: "inbound"})
	if none.Parsed != nil {
		t.Fatal("message with empty raw should have no parsed view")
	}
}

// parsed.html carries the decoded text/html part for display; text-only
// messages omit it.
func TestMessageViewParsedHTML(t *testing.T) {
	htmlRaw := []byte("From: a@x.com\r\nContent-Type: multipart/alternative; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nplain body\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<div>rich <b>body</b></div>\r\n--B--\r\n")
	v := messageViewFromIdentity(&identity.Message{
		ID: "msg_h", Direction: "inbound", Sender: "a@x.com", RawMessage: htmlRaw,
	})
	if v.Parsed == nil || v.Parsed.HTML != "<div>rich <b>body</b></div>" {
		t.Fatalf("parsed.html = %+v, want decoded HTML part", v.Parsed)
	}

	// Text-only message: html omitted (empty).
	textOnly := messageViewFromIdentity(&identity.Message{
		ID: "msg_t", Direction: "inbound", Sender: "a@x.com",
		RawMessage: []byte("From: a@x.com\r\nContent-Type: text/plain\r\n\r\njust text"),
	})
	if textOnly.Parsed == nil || textOnly.Parsed.HTML != "" {
		t.Fatalf("text-only parsed.html should be empty, got %+v", textOnly.Parsed)
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

// A policy "flag" verdict is delivered to the agent rather than held for
// review, so message reads must preserve the only pollable warning signal.
func TestMessageViewsPreserveFlagVerdict(t *testing.T) {
	msg := identity.Message{
		ID:         "msg_flagged",
		Direction:  "inbound",
		Flagged:    true,
		FlagReason: "sender not on allowlist",
	}

	detail := messageViewFromIdentity(&msg)
	if !detail.Flagged || detail.FlagReason != msg.FlagReason {
		t.Errorf("detail flag verdict = (%v, %q), want (true, %q)", detail.Flagged, detail.FlagReason, msg.FlagReason)
	}

	summary := messageSummaryFromIdentity(msg)
	if !summary.Flagged || summary.FlagReason != msg.FlagReason {
		t.Errorf("summary flag verdict = (%v, %q), want (true, %q)", summary.Flagged, summary.FlagReason, msg.FlagReason)
	}

	review := reviewView(identity.ReviewListItem{
		ID:         msg.ID,
		Direction:  msg.Direction,
		Flagged:    msg.Flagged,
		FlagReason: msg.FlagReason,
	})
	if !review.Flagged || review.FlagReason != msg.FlagReason {
		t.Errorf("review flag verdict = (%v, %q), want (true, %q)", review.Flagged, review.FlagReason, msg.FlagReason)
	}
}

// Held-draft (pending_approval) messages expose body_text/html via Body, the
// second representation the unified read serves (sent/inbound use raw_message).
func TestMessageViewHeldDraftBody(t *testing.T) {
	draft := messageViewFromIdentity(&identity.Message{
		ID: "msg_d", Direction: "outbound", Status: "pending_review",
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

// The shared message-detail shape always carries raw_message. Before an
// outbound review draft is approved there is no canonical MIME yet, so the
// required field is null; once composed, []byte uses the documented base64
// JSON representation.
func TestMessageViewRawMessageWireLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  *identity.Message
		want any
	}{
		{
			name: "held outbound draft",
			msg: &identity.Message{
				ID: "msg_held", Direction: "outbound", Status: "pending_review", BodyText: "review me",
			},
			want: nil,
		},
		{
			name: "composed outbound message",
			msg: &identity.Message{
				ID: "msg_sent", Direction: "outbound", Status: "sent", RawMessage: []byte("raw MIME"),
			},
			want: "cmF3IE1JTUU=",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(messageViewFromIdentity(tc.msg))
			if err != nil {
				t.Fatalf("marshal MessageView: %v", err)
			}
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatalf("unmarshal MessageView: %v", err)
			}
			got, present := wire["raw_message"]
			if !present {
				t.Fatal("raw_message must remain present in every message detail")
			}
			if got != tc.want {
				t.Fatalf("raw_message = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestMessageViewFromIdentity_NoHoldReasonLeak pins the PR's central safety
// guarantee: hold_reason is surfaced ONLY on the account-scoped
// review-detail path (handleGetReview sets them post-construction), never by the
// shared constructor. The agent-facing GET /v1/agents/{email}/messages/{id}
// returns bare messageViewFromIdentity output, so even a row that carries a hold
// verdict must not expose it here. A future edit that starts populating these in
// the constructor would leak screening internals onto the agent surface — this
// test fails loudly if that happens.
func TestMessageViewFromIdentity_NoHoldReasonLeak(t *testing.T) {
	score := 0.91
	v := messageViewFromIdentity(&identity.Message{
		ID:           "msg_leak",
		Direction:    "inbound",
		ReviewReason: identity.ReviewReasonInboundScan,
		ScanScore:    &score,
		ScanAction:   "review",
	})
	if v.HoldReason != nil {
		t.Errorf("agent-surface MessageView leaked hold_reason = %#v, want nil", v.HoldReason)
	}
	// The screening breakdown is review-only too: the shared constructor must
	// never populate it (handleGetReview attaches it after ownership is proven).
	if v.Protection != nil {
		t.Errorf("agent-surface MessageView leaked protection = %+v, want nil", v.Protection)
	}
}

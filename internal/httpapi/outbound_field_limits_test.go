package httpapi

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// GA contract — outbound request fields are bounded so a single hostile API
// key can't exhaust memory or bloat the DB with a multi-megabyte subject/body.
// The maxLength / maxItems limits are enforced at the Huma schema-validation
// layer (422 invalid_request) BEFORE the handler runs.

// An oversized subject (> 2000 chars) is rejected at the schema layer.
func TestSendRejectsOversizedSubject(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"r@x.com"}, "subject": strings.Repeat("A", maxSubjectLen+1), "text": "hello",
	})
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("want 422 invalid_request for oversized subject, got %d %v", code, body)
	}
}

// A subject at exactly the limit is accepted (maxLength is inclusive).
func TestSendAcceptsSubjectAtLimit(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"r@x.com"}, "subject": strings.Repeat("A", maxSubjectLen), "text": "hello",
	})
	if code != 200 {
		t.Fatalf("subject at limit: want 200, got %d", code)
	}
}

// An oversized plain-text body (> 1 MiB) is rejected at the schema layer.
func TestSendRejectsOversizedTextBody(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"r@x.com"}, "subject": "Hi", "text": strings.Repeat("A", maxBodyFieldBytes+1),
	})
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("want 422 invalid_request for oversized text, got %d %v", code, body)
	}
}

// An oversized HTML body (> 1 MiB) is rejected at the schema layer.
func TestSendRejectsOversizedHTMLBody(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"r@x.com"}, "subject": "Hi", "text": "hello",
		"html": strings.Repeat("<p>A</p>", (maxBodyFieldBytes/len("<p>A</p>"))+1),
	})
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("want 422 invalid_request for oversized html, got %d %v", code, body)
	}
}

// --- composed-message hard cap (10 MB) ---

// composedMessageSizeError is the runtime check that enforces the SES v1
// stored-message ceiling on the fully-composed content (subject + text + html +
// DECODED attachments). These exercise it directly — fast, no server needed.

// A composed message under the cap returns nil (no error).
func TestComposedMessageSizeUnderCap(t *testing.T) {
	subject := "Subject"
	text := "body text"
	html := "<p>html</p>"
	att := outbound.Attachment{
		Filename:    "small.txt",
		ContentType: "text/plain",
		Data:        base64.StdEncoding.EncodeToString([]byte("hello")),
	}
	if env := composedMessageSizeError(subject, text, html, []outbound.Attachment{att}); env != nil {
		t.Fatalf("under-cap composed message should pass, got %v", env)
	}
}

// A composed message over the cap (large decoded attachment pushing the total
// past 10 MB) is rejected with 413 payload_too_large — computed on DECODED
// bytes, not the base64 wire size.
func TestComposedMessageSizeOverCap(t *testing.T) {
	// Build an attachment whose DECODED size alone exceeds the composed cap.
	// maxComposedMessageBytes is 10 MiB; we decode to just over it. The
	// per-attachment cap (maxAttachmentBytes = 10 MiB) still admits this
	// because it's ≤ 10 MiB — the composed cap is the tighter ceiling.
	decodedSize := maxComposedMessageBytes + 1
	if decodedSize > maxAttachmentBytes {
		// Keep under the per-attachment cap so validateAttachments would pass;
		// the composed check is what fires. (maxComposedMessageBytes ==
		// maxAttachmentBytes today, so use text fields to push over.)
		decodedSize = maxAttachmentBytes
	}
	raw := make([]byte, decodedSize)
	att := outbound.Attachment{
		Filename:    "big.bin",
		ContentType: "application/octet-stream",
		Data:        base64.StdEncoding.EncodeToString(raw),
	}
	// Push the composed total over the cap via the text field (subject + text +
	// html + decoded attachment > maxComposedMessageBytes).
	overshoot := maxComposedMessageBytes - decodedSize + 1
	text := strings.Repeat("x", overshoot)
	env := composedMessageSizeError("s", text, "", []outbound.Attachment{att})
	if env == nil {
		t.Fatal("over-cap composed message should be rejected")
	}
	if env.Code() != "payload_too_large" || env.GetStatus() != 413 {
		t.Fatalf("want 413 payload_too_large, got status=%d code=%s", env.GetStatus(), env.Code())
	}
	details, ok := env.Err.Details.(map[string]any)
	if !ok {
		t.Fatalf("composed-size error details = %T, want map", env.Err.Details)
	}
	wantTotal := len("s") + len(text) + decodedSize
	if details["composed_bytes"] != wantTotal || details["max_composed_bytes"] != maxComposedMessageBytes {
		t.Fatalf("composed-size details = %#v, want composed_bytes=%d max_composed_bytes=%d", details, wantTotal, maxComposedMessageBytes)
	}
}

// The composed cap counts DECODED attachment bytes, not the larger base64 wire
// size — a caller whose decoded total is under the cap but whose base64 wire
// size is larger must NOT be falsely rejected.
func TestComposedMessageSizeCountsDecodedNotWire(t *testing.T) {
	// A decoded payload of 1 MiB base64-encodes to ~1.33 MiB on the wire.
	// The composed cap (10 MiB) is on decoded bytes, so this must pass.
	decoded := make([]byte, 1*1024*1024)
	att := outbound.Attachment{
		Filename:    "ok.bin",
		ContentType: "application/octet-stream",
		Data:        base64.StdEncoding.EncodeToString(decoded),
	}
	if env := composedMessageSizeError("s", "text", "", []outbound.Attachment{att}); env != nil {
		t.Fatalf("decoded-size check should pass for 1 MiB decoded attachment, got %v", env)
	}
}

// --- struct-tag / const drift guard ---

// The maxLength struct-tag literals can't reference Go consts, so this test
// guards against drift: if someone bumps a const but forgets the tag (or vice
// versa), this fails. Recipient totals are handler-validated because per-field
// maxItems would preempt the documented too_many_recipients error contract.
func TestOutboundFieldLimitTagsMatchConsts(t *testing.T) {
	type want struct {
		field    string
		tag      string
		expected string
	}
	cases := []struct {
		name string
		typ  any
		want []want
	}{
		{"SendEmailRequest", SendEmailRequest{}, []want{
			{"Subject", "maxLength", fmt.Sprintf("%d", maxSubjectLen)},
			{"Body", "maxLength", fmt.Sprintf("%d", maxBodyFieldBytes)},
			{"HTMLBody", "maxLength", fmt.Sprintf("%d", maxBodyFieldBytes)},
		}},
		{"ReplyRequest", ReplyRequest{}, []want{
			{"Body", "maxLength", fmt.Sprintf("%d", maxBodyFieldBytes)},
			{"HTMLBody", "maxLength", fmt.Sprintf("%d", maxBodyFieldBytes)},
		}},
		{"ForwardRequest", ForwardRequest{}, []want{
			{"Body", "maxLength", fmt.Sprintf("%d", maxBodyFieldBytes)},
			{"HTMLBody", "maxLength", fmt.Sprintf("%d", maxBodyFieldBytes)},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rt := reflect.TypeOf(c.typ)
			for _, w := range c.want {
				f, ok := rt.FieldByName(w.field)
				if !ok {
					t.Fatalf("%s.%s: field not found", c.name, w.field)
				}
				got := f.Tag.Get(w.tag)
				if got != w.expected {
					t.Errorf("%s.%s tag %q = %q, want %q", c.name, w.field, w.tag, got, w.expected)
				}
			}
		})
	}
}

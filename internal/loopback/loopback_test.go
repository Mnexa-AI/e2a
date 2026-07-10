package loopback

import (
	"net/mail"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// TestComposeMIMEReplyToOverride pins that a caller-supplied Reply-To override is
// honored on the loopback (self-send) path, matching the SMTP path. Without the
// override the loopback message carries NO Reply-To (unchanged note-to-self
// default); with it, the header is present verbatim in both the single-part and
// attachment branches.
func TestComposeMIMEReplyToOverride(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}
	const override = "Support <support@acme.com>"

	replyToHeader := func(t *testing.T, raw []byte) string {
		t.Helper()
		// Strip the synthetic leading Received: line the loopback prepends so
		// mail.ReadMessage sees a clean header block.
		s := string(raw)
		if i := strings.Index(s, "\r\nFrom:"); i >= 0 {
			s = s[i+2:]
		}
		m, err := mail.ReadMessage(strings.NewReader(s))
		if err != nil {
			t.Fatalf("parse loopback MIME: %v\n%s", err, string(raw))
		}
		return m.Header.Get("Reply-To")
	}

	cases := []struct {
		name string
		req  outbound.SendRequest
	}{
		{"single-part", outbound.SendRequest{To: []string{"bot@example.com"}, Subject: "hi", Body: "note", ReplyTo: override}},
		{"attachments", outbound.SendRequest{To: []string{"bot@example.com"}, Subject: "hi", Body: "note", ReplyTo: override,
			Attachments: []outbound.Attachment{{Filename: "a.txt", ContentType: "text/plain", Data: "aGVsbG8="}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := ComposeMIME(agent, tc.req, ProviderID("example.com"), "example.com")
			if err != nil {
				t.Fatalf("ComposeMIME: %v", err)
			}
			if got := replyToHeader(t, raw); got != override {
				t.Errorf("Reply-To = %q, want %q", got, override)
			}
		})
	}

	// No override → no Reply-To header (preserve existing loopback behavior).
	raw, err := ComposeMIME(agent, outbound.SendRequest{To: []string{"bot@example.com"}, Subject: "hi", Body: "note"}, ProviderID("example.com"), "example.com")
	if err != nil {
		t.Fatalf("ComposeMIME (plain): %v", err)
	}
	if got := replyToHeader(t, raw); got != "" {
		t.Errorf("plain self-send Reply-To = %q, want empty", got)
	}
}

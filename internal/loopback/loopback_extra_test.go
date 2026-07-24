package loopback

import (
	"net/mail"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/outbound"
)

// TestIsSelfSend pins the self-send detection contract: exactly one To
// recipient equal (case-insensitive, trimmed) to the agent's own address,
// with no Cc/Bcc. Anything else — external or mixed recipients, extra To
// entries, any Cc/Bcc — must route through normal SMTP.
func TestIsSelfSend(t *testing.T) {
	const agent = "bot@example.com"

	cases := []struct {
		name string
		req  outbound.SendRequest
		want bool
	}{
		{"single To matching agent", outbound.SendRequest{To: []string{"bot@example.com"}}, true},
		{"case-insensitive match", outbound.SendRequest{To: []string{"BOT@Example.COM"}}, true},
		{"whitespace-trimmed match", outbound.SendRequest{To: []string{"  bot@example.com \t"}}, true},
		{"empty To", outbound.SendRequest{}, false},
		{"different recipient", outbound.SendRequest{To: []string{"other@example.com"}}, false},
		{"two To entries including self", outbound.SendRequest{To: []string{"bot@example.com", "bot@example.com"}}, false},
		{"self plus external To", outbound.SendRequest{To: []string{"bot@example.com", "ext@example.com"}}, false},
		{"Cc present even when To is self", outbound.SendRequest{To: []string{"bot@example.com"}, CC: []string{"ext@example.com"}}, false},
		{"Cc carrying agent alias blocks self-send", outbound.SendRequest{To: []string{"bot@example.com"}, CC: []string{"bot@example.com"}}, false},
		{"Bcc present even when To is self", outbound.SendRequest{To: []string{"bot@example.com"}, BCC: []string{"ext@example.com"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSelfSend(tc.req, agent); got != tc.want {
				t.Errorf("IsSelfSend(%+v, %q) = %v, want %v", tc.req, agent, got, tc.want)
			}
		})
	}
}

// TestStripAgentSelfAliases pins the alias-strip contract: case-insensitive,
// whitespace-trimmed matches of the agent address are removed; everything
// else survives in order; the input slice is never mutated.
func TestStripAgentSelfAliases(t *testing.T) {
	const agent = "bot@example.com"

	t.Run("removes matches, keeps others in order", func(t *testing.T) {
		in := []string{"bot@example.com", "ext@example.com", " BOT@example.com ", "other@example.com"}
		got := StripAgentSelfAliases(in, agent)
		want := []string{"ext@example.com", "other@example.com"}
		if len(got) != len(want) {
			t.Fatalf("StripAgentSelfAliases = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("StripAgentSelfAliases = %v, want %v", got, want)
			}
		}
	})

	t.Run("does not mutate input", func(t *testing.T) {
		in := []string{"bot@example.com", "ext@example.com"}
		_ = StripAgentSelfAliases(in, agent)
		if in[0] != "bot@example.com" || in[1] != "ext@example.com" {
			t.Errorf("input mutated: %v", in)
		}
	})

	t.Run("empty slice returns empty", func(t *testing.T) {
		if got := StripAgentSelfAliases(nil, agent); len(got) != 0 {
			t.Errorf("StripAgentSelfAliases(nil) = %v, want empty", got)
		}
	})

	t.Run("no matches returns equal slice", func(t *testing.T) {
		in := []string{"a@example.com", "b@example.com"}
		got := StripAgentSelfAliases(in, agent)
		if len(got) != 2 || got[0] != in[0] || got[1] != in[1] {
			t.Errorf("StripAgentSelfAliases = %v, want %v", got, in)
		}
	})
}

// TestIsSelfSendAfterStrip covers the documented reply-all-on-self-thread
// flow: stripping the agent alias from Cc first lets IsSelfSend recognize
// the message as a self-send.
func TestIsSelfSendAfterStrip(t *testing.T) {
	const agent = "bot@example.com"
	req := outbound.SendRequest{
		To: []string{"bot@example.com"},
		CC: []string{"bot@example.com", "ext@example.com"},
	}
	if IsSelfSend(req, agent) {
		t.Fatal("IsSelfSend before strip = true, want false (Cc present)")
	}
	req.CC = StripAgentSelfAliases(req.CC, agent)
	if IsSelfSend(req, agent) {
		t.Fatal("IsSelfSend after strip with external Cc = true, want false")
	}
	req.CC = StripAgentSelfAliases([]string{"bot@example.com"}, agent)
	if !IsSelfSend(req, agent) {
		t.Fatal("IsSelfSend after stripping only-alias Cc = false, want true")
	}
}

// TestProviderIDShape pins the provider-ID contract: an RFC 5322-shaped
// Message-ID under the loopback.<domain> host, with the e2a.local fallback
// when no from-domain is configured, and uniqueness across calls.
func TestProviderIDShape(t *testing.T) {
	t.Run("uses configured domain", func(t *testing.T) {
		id := ProviderID("example.com")
		if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@loopback.example.com>") {
			t.Errorf("ProviderID = %q, want <hex@loopback.example.com>", id)
		}
		if _, err := mail.ParseAddress(id); err == nil {
			// ParseAddress accepts angle-addrs; Message-ID is not an address,
			// so this is only a loose sanity check — shape assertions above are authoritative.
			t.Logf("note: %q parses as an address (informational only)", id)
		}
	})

	t.Run("empty domain falls back to e2a.local", func(t *testing.T) {
		id := ProviderID("")
		if !strings.HasSuffix(id, "@loopback.e2a.local>") {
			t.Errorf("ProviderID(\"\") = %q, want suffix @loopback.e2a.local>", id)
		}
	})

	t.Run("unique across calls", func(t *testing.T) {
		a, b := ProviderID("example.com"), ProviderID("example.com")
		if a == b {
			t.Errorf("two ProviderID calls returned identical IDs: %q", a)
		}
	})
}

// TestComposeMIMEReceivedLine pins the synthetic Received: header the
// loopback path prepends: it carries the "with loopback" grep signal, the
// provider ID, and the recipient, ahead of the composed message.
func TestComposeMIMEReceivedLine(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}
	providerID := ProviderID("example.com")
	raw, err := ComposeMIME(agent, outbound.SendRequest{To: []string{agent.ID}, Subject: "hi", Body: "note"}, providerID, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.HasPrefix(s, "Received: by example.com (e2a) with loopback id "+providerID+" for <bot@example.com>;") {
		t.Errorf("message does not start with expected Received line:\n%s", s)
	}
	if strings.Count(s, "with loopback") != 1 {
		t.Errorf("want exactly one 'with loopback' marker, got %d", strings.Count(s, "with loopback"))
	}
}

// TestComposeMIMEReceivedLineDefaultHost covers the e2a.local fallback host
// in the Received line when no from-domain is configured.
func TestComposeMIMEReceivedLineDefaultHost(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}
	raw, err := ComposeMIME(agent, outbound.SendRequest{To: []string{agent.ID}, Subject: "hi", Body: "note"}, ProviderID(""), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "Received: by e2a.local (e2a) with loopback id ") {
		t.Errorf("Received line missing e2a.local fallback host:\n%s", string(raw))
	}
}

// TestComposeMIMEFromHeader pins the From header shape: display name quoted
// when the agent has one, bare address when it doesn't.
func TestComposeMIMEFromHeader(t *testing.T) {
	parseFrom := func(t *testing.T, raw []byte) string {
		t.Helper()
		s := string(raw)
		if i := strings.Index(s, "\r\nFrom:"); i >= 0 {
			s = s[i+2:]
		}
		m, err := mail.ReadMessage(strings.NewReader(s))
		if err != nil {
			t.Fatalf("parse loopback MIME: %v\n%s", err, string(raw))
		}
		return m.Header.Get("From")
	}

	t.Run("named agent gets quoted display name", func(t *testing.T) {
		agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com", Name: "Support Bot"}
		raw, err := ComposeMIME(agent, outbound.SendRequest{To: []string{agent.ID}, Subject: "hi", Body: "note"}, ProviderID("example.com"), "example.com")
		if err != nil {
			t.Fatal(err)
		}
		if got := parseFrom(t, raw); got != `"Support Bot" <bot@example.com>` {
			t.Errorf("From = %q, want %q", got, `"Support Bot" <bot@example.com>`)
		}
	})

	t.Run("nameless agent gets bare address", func(t *testing.T) {
		agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}
		raw, err := ComposeMIME(agent, outbound.SendRequest{To: []string{agent.ID}, Subject: "hi", Body: "note"}, ProviderID("example.com"), "example.com")
		if err != nil {
			t.Fatal(err)
		}
		if got := parseFrom(t, raw); got != "bot@example.com" {
			t.Errorf("From = %q, want %q", got, "bot@example.com")
		}
	})
}

// TestComposeMIMEHTMLBody pins the single-part content-type switch: an
// HTMLBody selects text/html and the HTML body; without it the message
// stays text/plain.
func TestComposeMIMEHTMLBody(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}

	parse := func(t *testing.T, raw []byte) *mail.Message {
		t.Helper()
		s := string(raw)
		if i := strings.Index(s, "\r\nFrom:"); i >= 0 {
			s = s[i+2:]
		}
		m, err := mail.ReadMessage(strings.NewReader(s))
		if err != nil {
			t.Fatalf("parse loopback MIME: %v\n%s", err, string(raw))
		}
		return m
	}

	t.Run("html body wins over plain", func(t *testing.T) {
		raw, err := ComposeMIME(agent, outbound.SendRequest{
			To: []string{agent.ID}, Subject: "hi", Body: "plain", HTMLBody: "<p>html</p>",
		}, ProviderID("example.com"), "example.com")
		if err != nil {
			t.Fatal(err)
		}
		m := parse(t, raw)
		if ct := m.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
	})

	t.Run("composer error propagates", func(t *testing.T) {
		// A CR/LF in the attachment filename is refused by the underlying
		// composer (header-injection guard); ComposeMIME must surface the error.
		_, err := ComposeMIME(agent, outbound.SendRequest{
			To: []string{agent.ID}, Subject: "hi", Body: "note",
			Attachments: []outbound.Attachment{{Filename: "evil\r\nInjected: yes", ContentType: "text/plain", Data: "aGVsbG8="}},
		}, ProviderID("example.com"), "example.com")
		if err == nil {
			t.Fatal("ComposeMIME with CR/LF attachment filename returned nil error")
		}
	})

	t.Run("plain-only stays text/plain", func(t *testing.T) {
		raw, err := ComposeMIME(agent, outbound.SendRequest{
			To: []string{agent.ID}, Subject: "hi", Body: "plain",
		}, ProviderID("example.com"), "example.com")
		if err != nil {
			t.Fatal(err)
		}
		m := parse(t, raw)
		if ct := m.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("Content-Type = %q, want text/plain", ct)
		}
	})
}

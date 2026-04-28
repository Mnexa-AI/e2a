package outbound

import (
	"mime"
	"net/mail"
	"strings"
	"testing"
)

func TestComposeMessageBasic(t *testing.T) {
	raw, err := ComposeMessage(
		"agent@bot.example.com",
		[]string{"alice@gmail.com"},
		nil,
		"Hello Alice",
		"This is a test message.",
		"text/plain",
		"",
		"relay.e2a.dev",
		"",
		"",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("failed to parse composed message: %v", err)
	}

	if got := msg.Header.Get("From"); got != "agent@bot.example.com" {
		t.Errorf("From = %q, want agent@bot.example.com", got)
	}
	if got := msg.Header.Get("To"); got != "alice@gmail.com" {
		t.Errorf("To = %q, want alice@gmail.com", got)
	}
	if got := msg.Header.Get("Subject"); got != "Hello Alice" {
		t.Errorf("Subject = %q, want Hello Alice", got)
	}
	if got := msg.Header.Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
	if got := msg.Header.Get("Mime-Version"); got != "1.0" {
		t.Errorf("MIME-Version = %q, want 1.0", got)
	}
}

func TestComposeMessageSubjectRFC2047Encoding(t *testing.T) {
	raw, err := ComposeMessage(
		"from@test.com", []string{"to@test.com"}, nil,
		"Résumé 📄 for アリス", "Body", "text/plain", "", "test.dev", "", "",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}

	// Raw header must be 7-bit ASCII (RFC 5322 §2.2).
	headerEnd := strings.Index(string(raw), "\r\n\r\n")
	if headerEnd < 0 {
		t.Fatal("no header/body separator")
	}
	for i, b := range []byte(string(raw)[:headerEnd]) {
		if b > 127 {
			t.Fatalf("non-ASCII byte 0x%x at offset %d in headers", b, i)
		}
	}

	// Go's mail.ReadMessage + WordDecoder should round-trip the subject.
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dec := new(mime.WordDecoder)
	got, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if got != "Résumé 📄 for アリス" {
		t.Errorf("decoded subject = %q, want %q", got, "Résumé 📄 for アリス")
	}
}

func TestComposeMessageASCIISubjectUnchanged(t *testing.T) {
	// Pure-ASCII subjects should pass through without encoded-word wrapping.
	raw, err := ComposeMessage(
		"from@test.com", []string{"to@test.com"}, nil,
		"Hello Alice", "Body", "text/plain", "", "test.dev", "", "",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}
	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	if got := msg.Header.Get("Subject"); got != "Hello Alice" {
		t.Errorf("Subject = %q, want Hello Alice (no encoded-word for ASCII)", got)
	}
}

func TestComposeMessageMultipleRecipients(t *testing.T) {
	raw, err := ComposeMessage(
		"from@test.com",
		[]string{"alice@gmail.com", "bob@gmail.com"},
		[]string{"carol@gmail.com"},
		"Hello",
		"Body",
		"text/plain",
		"",
		"test.dev",
		"",
		"",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}

	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	if got := msg.Header.Get("To"); got != "alice@gmail.com, bob@gmail.com" {
		t.Errorf("To = %q, want alice@gmail.com, bob@gmail.com", got)
	}
	if got := msg.Header.Get("Cc"); got != "carol@gmail.com" {
		t.Errorf("Cc = %q, want carol@gmail.com", got)
	}
}

func TestComposeMessageCCOnlyOmitsToHeader(t *testing.T) {
	raw, err := ComposeMessage(
		"from@test.com",
		nil,
		[]string{"carol@gmail.com"},
		"Hello",
		"Body",
		"text/plain",
		"",
		"test.dev",
		"",
		"",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}

	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	if got := msg.Header.Get("To"); got != "" {
		t.Errorf("To = %q, want empty (CC-only)", got)
	}
	if got := msg.Header.Get("Cc"); got != "carol@gmail.com" {
		t.Errorf("Cc = %q, want carol@gmail.com", got)
	}
}

func TestComposeMessageNoBccHeader(t *testing.T) {
	// BCC should never appear in composed message headers
	raw, err := ComposeMessage(
		"from@test.com",
		[]string{"alice@gmail.com"},
		nil,
		"Hello",
		"Body",
		"text/plain",
		"",
		"test.dev",
		"",
		"",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}

	if strings.Contains(strings.ToLower(string(raw)), "bcc") {
		t.Error("composed message should never contain a Bcc header")
	}
}

func TestComposeMessageDefaultContentType(t *testing.T) {
	raw, err := ComposeMessage("from@test.com", []string{"to@test.com"}, nil, "Sub", "Body", "", "", "test.dev", "", "")
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}

	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	ct := msg.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("expected default text/plain, got %q", ct)
	}
}

func TestComposeMessageReplyTo(t *testing.T) {
	raw, err := ComposeMessage(
		"from@test.com", []string{"to@test.com"}, nil, "Re: Hello", "Reply body",
		"text/plain", "<original-msg-id@example.com>", "test.dev", "", "",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}

	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	if got := msg.Header.Get("In-Reply-To"); got != "<original-msg-id@example.com>" {
		t.Errorf("In-Reply-To = %q, want <original-msg-id@example.com>", got)
	}
	if got := msg.Header.Get("References"); got != "<original-msg-id@example.com>" {
		t.Errorf("References = %q, want <original-msg-id@example.com>", got)
	}
}

func TestComposeMessageNoReplyTo(t *testing.T) {
	raw, err := ComposeMessage("from@test.com", []string{"to@test.com"}, nil, "Sub", "Body", "", "", "test.dev", "", "")
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}

	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	if got := msg.Header.Get("In-Reply-To"); got != "" {
		t.Errorf("expected no In-Reply-To header, got %q", got)
	}
	if got := msg.Header.Get("References"); got != "" {
		t.Errorf("expected no References header, got %q", got)
	}
}

func TestComposeMessageConversationIDHeader(t *testing.T) {
	raw, err := ComposeMessage(
		"from@test.com", []string{"to@test.com"}, nil,
		"Hello", "Body", "text/plain", "", "test.dev", "", "081158ac-bf25-4eb6-a6b0-02828ec670c3",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}
	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	if got := msg.Header.Get("X-E2A-Conversation-Id"); got != "081158ac-bf25-4eb6-a6b0-02828ec670c3" {
		t.Errorf("X-E2A-Conversation-Id = %q, want the UUID", got)
	}
}

func TestComposeMessageNoConversationIDHeader(t *testing.T) {
	raw, err := ComposeMessage(
		"from@test.com", []string{"to@test.com"}, nil,
		"Hello", "Body", "text/plain", "", "test.dev", "", "",
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}
	if strings.Contains(string(raw), "X-E2A-Conversation-Id") {
		t.Error("empty conversation_id should not emit X-E2A-Conversation-Id header")
	}
}

func TestComposeMultipartMessage(t *testing.T) {
	raw, err := ComposeMultipartMessage(
		"bot@agent.example.com", []string{"alice@gmail.com"}, nil,
		"Re: Hello", "Plain text body", "<p>HTML body</p>",
		"<orig@example.com>", "relay.e2a.dev", "", "",
	)
	if err != nil {
		t.Fatalf("ComposeMultipartMessage failed: %v", err)
	}

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("failed to parse multipart message: %v", err)
	}

	ct := msg.Header.Get("Content-Type")
	if !strings.Contains(ct, "multipart/alternative") {
		t.Errorf("Content-Type = %q, want multipart/alternative", ct)
	}
	if got := msg.Header.Get("In-Reply-To"); got != "<orig@example.com>" {
		t.Errorf("In-Reply-To = %q, want <orig@example.com>", got)
	}

	body := string(raw)
	if !strings.Contains(body, "Plain text body") {
		t.Error("expected plain text part in message body")
	}
	if !strings.Contains(body, "<p>HTML body</p>") {
		t.Error("expected HTML part in message body")
	}
}

func TestComposeMultipartMessageFallsBackToPlain(t *testing.T) {
	raw, err := ComposeMultipartMessage(
		"bot@agent.example.com", []string{"alice@gmail.com"}, nil,
		"Hello", "Plain text only", "",
		"", "relay.e2a.dev", "", "",
	)
	if err != nil {
		t.Fatalf("ComposeMultipartMessage failed: %v", err)
	}

	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	ct := msg.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain fallback, got %q", ct)
	}
}

func TestComposeMultipartMessageWithCC(t *testing.T) {
	raw, err := ComposeMultipartMessage(
		"bot@example.com",
		[]string{"alice@gmail.com"},
		[]string{"bob@gmail.com", "carol@gmail.com"},
		"Hello", "Plain", "<p>HTML</p>",
		"", "relay.e2a.dev", "", "",
	)
	if err != nil {
		t.Fatalf("ComposeMultipartMessage failed: %v", err)
	}

	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	if got := msg.Header.Get("Cc"); got != "bob@gmail.com, carol@gmail.com" {
		t.Errorf("Cc = %q, want bob@gmail.com, carol@gmail.com", got)
	}
}

func TestComposeMultipartMessageConversationIDHeader(t *testing.T) {
	raw, err := ComposeMultipartMessage(
		"bot@agent.example.com", []string{"alice@gmail.com"}, nil,
		"Hi", "Plain", "<p>HTML</p>",
		"", "relay.e2a.dev", "", "conv-xyz",
	)
	if err != nil {
		t.Fatalf("ComposeMultipartMessage failed: %v", err)
	}
	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	if got := msg.Header.Get("X-E2A-Conversation-Id"); got != "conv-xyz" {
		t.Errorf("X-E2A-Conversation-Id = %q, want conv-xyz", got)
	}
}

// Regression: an attacker-controlled conversation_id containing CR/LF must
// not smuggle additional headers (e.g. blind Bcc) into the composed
// message. The header writer strips CR and LF from values as last-line
// defense; the API layer also rejects with 400, but the composer must
// remain safe even if a future caller forgets to validate.
func TestComposeMessageStripsCRLFFromConversationID(t *testing.T) {
	malicious := "abc\r\nBcc: leak@attacker.com"
	out, err := ComposeMessage(
		"sender@agents.e2a.dev",
		[]string{"target@victim.com"}, nil,
		"hi", "benign", "text/plain",
		"", "agents.e2a.dev", "",
		malicious,
	)
	if err != nil {
		t.Fatalf("ComposeMessage failed: %v", err)
	}
	msg := string(out)

	// Injection would manifest as a fresh header line — i.e. CRLF
	// followed by "Bcc:" — anywhere in the message.
	if strings.Contains(msg, "\r\nBcc:") {
		t.Errorf("header injection: smuggled Bcc started a new header line\n%s", msg)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(msg))
	if err != nil {
		t.Fatalf("composed message not parseable: %v", err)
	}
	got := parsed.Header.Get("X-E2A-Conversation-Id")
	if want := "abcBcc: leak@attacker.com"; got != want {
		t.Errorf("X-E2A-Conversation-Id = %q, want %q (CR/LF stripped, remaining bytes intact)", got, want)
	}
	if parsed.Header.Get("Bcc") != "" {
		t.Errorf("Bcc header should be absent, got %q", parsed.Header.Get("Bcc"))
	}
}

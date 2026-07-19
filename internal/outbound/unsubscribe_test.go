package outbound

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
)

func TestNormalizeRecipientsForManagedUnsubscribe(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}
	req := SendRequest{
		To:  []string{"Person <USER@Example.net>", "user@example.net"},
		CC:  []string{"USER@example.net"},
		BCC: []string{"other@example.net"},
	}
	to, cc, bcc, envelope, err := NormalizeRecipients(agent, "send.e2a.dev", req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(to, ",") != "user@example.net" || len(cc) != 0 || strings.Join(bcc, ",") != "other@example.net" {
		t.Fatalf("normalized to=%v cc=%v bcc=%v", to, cc, bcc)
	}
	if strings.Join(envelope, ",") != "user@example.net,other@example.net" {
		t.Fatalf("envelope=%v", envelope)
	}
}

func TestNormalizeRecipientsUnmanagedSelfAliasRetainsNoValidRecipientsError(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Email: "bot@example.com"}
	_, _, _, _, err := NormalizeRecipients(agent, "send.e2a.dev", SendRequest{To: []string{agent.EmailAddress()}})
	if err == nil || err.Error() != "no valid recipients" {
		t.Fatalf("error=%v", err)
	}
}

func TestComposeManagedUnsubscribeAddsFooterAndHeaders(t *testing.T) {
	link := "https://api.example.com/u/u1_token"
	agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}
	s := NewSender(nil, "send.e2a.dev")
	got, err := s.ComposeForAccept(agent, SendRequest{
		To: []string{"user@example.net"}, Subject: "hello", Body: "plain <body>", HTMLBody: "<p>html & body</p>",
		Unsubscribe: &UnsubscribeOptions{Mode: "managed", URL: link},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(got.Raw)
	if strings.Count(raw, "List-Unsubscribe: <"+link+">") != 1 || strings.Count(raw, "List-Unsubscribe-Post: List-Unsubscribe=One-Click") != 1 {
		t.Fatalf("managed list headers missing or duplicated:\n%s", raw)
	}
	if !strings.Contains(raw, "Unsubscribe from emails sent by bot@example.com: "+link) {
		t.Fatalf("plain footer missing:\n%s", raw)
	}
	if !strings.Contains(raw, `Unsubscribe from emails sent by bot@example.com`) || !strings.Contains(raw, `href="https://api.example.com/u/u1_token"`) {
		t.Fatalf("HTML footer missing/unsafe:\n%s", raw)
	}

	unmanaged, err := s.ComposeForAccept(agent, SendRequest{To: []string{"user@example.net"}, Subject: "hello", Body: "plain <body>", HTMLBody: "<p>html & body</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(unmanaged.Raw, []byte("List-Unsubscribe")) || bytes.Contains(unmanaged.Raw, []byte("Unsubscribe from emails")) {
		t.Fatalf("omitted option changed unmanaged MIME:\n%s", unmanaged.Raw)
	}
}

func TestComposeManagedUnsubscribeWithAttachmentAndReplyThreading(t *testing.T) {
	link := "https://api.example.com/u/u1_attachment"
	agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}
	got, err := NewSender(nil, "send.e2a.dev").ComposeForAccept(agent, SendRequest{
		To: []string{"user@example.net"}, Subject: "Re: hello", Body: "reply body", HTMLBody: "<p>reply</p>",
		ReplyToMessageID: "<parent@example.net>", References: []string{"<root@example.net>", "<parent@example.net>"},
		Attachments: []Attachment{{Filename: "note.txt", ContentType: "text/plain", Data: "aGk="}},
		Unsubscribe: &UnsubscribeOptions{Mode: "managed", URL: link},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(got.Raw)
	for _, want := range []string{"List-Unsubscribe: <" + link + ">", "Unsubscribe from emails sent by bot@example.com", "In-Reply-To: <parent@example.net>", `filename="note.txt"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("MIME missing %q:\n%s", want, raw)
		}
	}
}

func TestComposeManagedUnsubscribeRequiresOneRecipientAndURL(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Domain: "example.com"}
	s := NewSender(nil, "send.e2a.dev")
	for name, req := range map[string]SendRequest{
		"multiple":    {To: []string{"a@example.net"}, BCC: []string{"b@example.net"}, Subject: "s", Body: "b", Unsubscribe: &UnsubscribeOptions{Mode: "managed", URL: "https://api.example/u/x"}},
		"missing_url": {To: []string{"a@example.net"}, Subject: "s", Body: "b", Unsubscribe: &UnsubscribeOptions{Mode: "managed"}},
	} {
		if _, err := s.ComposeForAccept(agent, req); !IsValidationError(err) {
			t.Errorf("%s err=%v, want ValidationError", name, err)
		}
	}
}

func TestComposeManagedUnsubscribeRejectsTextWhenFooterCrossesComposedCap(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Email: "bot@example.com"}
	subject := "s"
	body := strings.Repeat("x", MaxComposedMessageBytes-len(subject))
	req := SendRequest{
		To: []string{"user@example.net"}, Subject: subject, Body: body,
		Unsubscribe: &UnsubscribeOptions{Mode: "managed", URL: "https://api.example/u/token"},
	}
	if before := ComposedSize(req.Subject, req.Body, req.HTMLBody, req.Attachments); before != MaxComposedMessageBytes {
		t.Fatalf("pre-footer size=%d, want %d", before, MaxComposedMessageBytes)
	}
	if _, err := NewSender(nil, "send.e2a.dev").ComposeForAccept(agent, req); !IsComposedSizeError(err) {
		t.Fatalf("error=%T %v, want composed-size error", err, err)
	}
}

func TestComposeManagedUnsubscribeRejectsMultipartWhenFootersCrossComposedCap(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Email: "bot@example.com"}
	req := SendRequest{
		To: []string{"user@example.net"}, Subject: "s", Body: "x",
		HTMLBody:    strings.Repeat("h", MaxComposedMessageBytes-3),
		Unsubscribe: &UnsubscribeOptions{Mode: "managed", URL: "https://api.example/u/token"},
	}
	if before := ComposedSize(req.Subject, req.Body, req.HTMLBody, req.Attachments); before >= MaxComposedMessageBytes {
		t.Fatalf("pre-footer size=%d, want below %d", before, MaxComposedMessageBytes)
	}
	if _, err := NewSender(nil, "send.e2a.dev").ComposeForAccept(agent, req); !IsComposedSizeError(err) {
		t.Fatalf("error=%T %v, want composed-size error", err, err)
	}
}

func TestComposeUnmanagedAtComposedCapRemainsAccepted(t *testing.T) {
	agent := &identity.AgentIdentity{ID: "bot@example.com", Email: "bot@example.com"}
	req := SendRequest{To: []string{"user@example.net"}, Subject: "s", Body: strings.Repeat("x", MaxComposedMessageBytes-1)}
	if _, err := NewSender(nil, "send.e2a.dev").ComposeForAccept(agent, req); err != nil {
		t.Fatalf("unmanaged message at cap rejected: %v", err)
	}
}

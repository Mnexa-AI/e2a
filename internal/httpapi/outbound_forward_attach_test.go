package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// TestForwardCarriesOriginalAttachments is the regression for #298: forwarding a
// message must ship the source message's stored attachment parts by default
// (the way mail clients do), without the caller re-fetching each one. Any
// caller-supplied attachments are additive on top of the originals.
func TestForwardCarriesOriginalAttachments(t *testing.T) {
	origPDF := []byte("PDF-ORIGINAL-BYTES")
	raw := []byte("From: alice@x.com\r\n" +
		"To: support@acme.com\r\n" +
		"Subject: Invoice\r\n" +
		"Message-ID: <abc@x.com>\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n" +
		"\r\n" +
		"--B\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"see attached\r\n" +
		"--B\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		base64.StdEncoding.EncodeToString(origPDF) + "\r\n" +
		"--B--\r\n")

	var captured outbound.SendRequest
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) { return &identity.User{ID: "u_1"}, nil },
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "support@acme.com" {
				return &identity.AgentIdentity{ID: "support@acme.com", Email: "support@acme.com", UserID: "u_1", DomainVerified: true}, nil
			}
			return nil, errors.New("not found")
		},
		GetRepliableMessage: func(ctx context.Context, messageID string) (*identity.Message, error) {
			if messageID == "msg_att" {
				return &identity.Message{
					ID: "msg_att", AgentID: "support@acme.com", Sender: "alice@x.com",
					Subject: "Invoice", EmailMessageID: "<abc@x.com>", RawMessage: raw,
				}, nil
			}
			return nil, errors.New("not found")
		},
		DeliverOutbound: func(ctx context.Context, u *identity.User, ag *identity.AgentIdentity, req outbound.SendRequest, mt, rt string, ref *identity.Message, ic agent.AcceptIdemCompleter) (*agent.OutboundResult, *agent.OutboundError) {
			captured = req
			return &agent.OutboundResult{MessageID: "msg_sent_1", Method: "smtp"}, nil
		},
	}))
	t.Cleanup(srv.Close)

	post := func(body map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+"/v1/agents/support%40acme.com/messages/msg_att/forward", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer good")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("forward: want 200, got %d", resp.StatusCode)
		}
	}

	// Forward with NO caller-supplied attachments → original is carried.
	post(map[string]any{"to": []string{"bob@x.com"}, "body": "fyi"})
	if len(captured.Attachments) != 1 {
		t.Fatalf("forward without re-attach: want 1 carried attachment, got %d", len(captured.Attachments))
	}
	if captured.Attachments[0].Filename != "invoice.pdf" {
		t.Errorf("carried filename = %q, want invoice.pdf", captured.Attachments[0].Filename)
	}
	if got, want := captured.Attachments[0].Data, base64.StdEncoding.EncodeToString(origPDF); got != want {
		t.Errorf("carried data = %q, want %q", got, want)
	}

	// Forward WITH a caller-supplied attachment → originals first, caller additive.
	post(map[string]any{
		"to":   []string{"bob@x.com"},
		"body": "fyi",
		"attachments": []map[string]any{
			{"filename": "extra.txt", "content_type": "text/plain", "data": base64.StdEncoding.EncodeToString([]byte("more"))},
		},
	})
	if len(captured.Attachments) != 2 {
		t.Fatalf("forward with re-attach: want 2 attachments (original + caller), got %d", len(captured.Attachments))
	}
	if captured.Attachments[0].Filename != "invoice.pdf" || captured.Attachments[1].Filename != "extra.txt" {
		t.Errorf("want [invoice.pdf, extra.txt], got [%q, %q]", captured.Attachments[0].Filename, captured.Attachments[1].Filename)
	}
}

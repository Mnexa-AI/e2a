package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// replyDirTestServer builds a minimal /v1 server whose GetRepliableMessage
// stub returns a fixture keyed by id, and whose DeliverOutbound captures the
// composed SendRequest so tests can assert the derived recipients/threading.
// It exercises the reply-to-/forward-outbound path end to end over HTTP.
func replyDirTestServer(t *testing.T, fixtures map[string]*identity.Message, captured *outbound.SendRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "support@acme.com" {
				return &identity.AgentIdentity{ID: "support@acme.com", Email: "support@acme.com", UserID: "u_1", DomainVerified: true}, nil
			}
			return nil, errors.New("not found")
		},
		GetRepliableMessage: func(ctx context.Context, messageID string) (*identity.Message, error) {
			if m, ok := fixtures[messageID]; ok {
				return m, nil
			}
			return nil, errors.New("not found")
		},
		DeliverOutbound: func(ctx context.Context, u *identity.User, ag *identity.AgentIdentity, req outbound.SendRequest, mt, rt string, ref *identity.Message, ic agent.AcceptIdemCompleter) (*agent.OutboundResult, *agent.OutboundError) {
			*captured = req
			return &agent.OutboundResult{MessageID: "msg_sent_1", Method: "smtp"}, nil
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

// outboundFixture is a message the agent itself sent, as actually stored:
// recipients in the to_recipients/cc columns, NO email_message_id (the composer
// omits Message-ID; the relay-assigned id lives in provider_message_id), and a
// composed raw message without a Message-ID header. This mirrors what
// CreateOutboundMessage persists so the threading assertions aren't fiction.
func outboundFixture() *identity.Message {
	raw := []byte("From: support@acme.com\r\n" +
		"To: bob@x.com, carol@x.com\r\n" +
		"Cc: dave@x.com\r\n" +
		"Subject: Project update\r\n\r\nhello team")
	return &identity.Message{
		ID: "msg_out1", AgentID: "support@acme.com", Direction: "outbound",
		Sender: "support@acme.com", Subject: "Project update",
		ProviderMessageID: "<sent-1@acme.com>", ConversationID: "conv_out",
		ToRecipients: []string{"bob@x.com", "carol@x.com"},
		CC:           []string{"dave@x.com"},
		RawMessage:   raw,
	}
}

// Reply to the agent's own outbound message → To = original To, no Cc (plain
// reply), threaded onto the outbound's Message-ID. This is the core bug fix:
// before, this 404'd because the lookup filtered direction='inbound'.
func TestReplyToOutbound_TargetsOriginalRecipients(t *testing.T) {
	var captured outbound.SendRequest
	srv := replyDirTestServer(t, map[string]*identity.Message{"msg_out1": outboundFixture()}, &captured)

	status, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_out1/reply", "good",
		map[string]any{"body": "following up"})
	if status != 200 {
		t.Fatalf("reply to outbound: want 200, got %d", status)
	}
	if !reflect.DeepEqual(captured.To, []string{"bob@x.com", "carol@x.com"}) {
		t.Errorf("To = %v, want original To [bob@x.com carol@x.com]", captured.To)
	}
	if len(captured.CC) != 0 {
		t.Errorf("CC = %v, want empty on plain reply", captured.CC)
	}
	if captured.ReplyToMessageID != "<sent-1@acme.com>" {
		t.Errorf("ReplyToMessageID = %q, want the outbound Message-ID", captured.ReplyToMessageID)
	}
	if captured.Subject != "Re: Project update" {
		t.Errorf("Subject = %q, want Re: Project update", captured.Subject)
	}
	if len(captured.References) == 0 || captured.References[len(captured.References)-1] != "<sent-1@acme.com>" {
		t.Errorf("References = %v, want chain ending in the outbound Message-ID", captured.References)
	}
}

// reply_all to your own outbound adds the original Cc; the caller's extra Cc is
// merged. BCC is never carried.
func TestReplyAllToOutbound_AddsOriginalCC(t *testing.T) {
	var captured outbound.SendRequest
	srv := replyDirTestServer(t, map[string]*identity.Message{"msg_out1": outboundFixture()}, &captured)

	status, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_out1/reply", "good",
		map[string]any{"body": "all", "reply_all": true, "cc": []string{"erin@x.com"}})
	if status != 200 {
		t.Fatalf("reply_all to outbound: want 200, got %d", status)
	}
	if !reflect.DeepEqual(captured.To, []string{"bob@x.com", "carol@x.com"}) {
		t.Errorf("To = %v, want original To", captured.To)
	}
	// Original Cc (dave) + caller Cc (erin), self-alias stripping does not touch
	// these external addresses.
	if !reflect.DeepEqual(captured.CC, []string{"dave@x.com", "erin@x.com"}) {
		t.Errorf("CC = %v, want [dave@x.com erin@x.com]", captured.CC)
	}
}

// An outbound target with no recorded recipients must fail closed with 400,
// never fall back to the agent's own address (a self-send loop).
func TestReplyToOutbound_NoRecipients_400(t *testing.T) {
	var captured outbound.SendRequest
	fix := outboundFixture()
	fix.ToRecipients = nil
	fix.CC = nil
	srv := replyDirTestServer(t, map[string]*identity.Message{"msg_out1": fix}, &captured)

	status, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_out1/reply", "good",
		map[string]any{"body": "hi"})
	if status != http.StatusBadRequest {
		t.Fatalf("reply to recipient-less outbound: want 400, got %d", status)
	}
	if captured.To != nil {
		t.Errorf("DeliverOutbound should not have been called; captured.To = %v", captured.To)
	}
}

// A message owned by a DIFFERENT agent must 404 even though the id resolves in
// the (id-only) store lookup — the handler's agent-ownership guard is the only
// thing scoping the id-only query.
func TestReplyToOutbound_CrossAgent_404(t *testing.T) {
	var captured outbound.SendRequest
	fix := outboundFixture()
	fix.AgentID = "other@acme.com" // not the path agent (support@acme.com)
	srv := replyDirTestServer(t, map[string]*identity.Message{"msg_out1": fix}, &captured)

	status, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_out1/reply", "good",
		map[string]any{"body": "hi"})
	if status != http.StatusNotFound {
		t.Fatalf("reply to another agent's message: want 404, got %d", status)
	}
	if captured.To != nil {
		t.Errorf("DeliverOutbound must not run for a cross-agent target; captured.To = %v", captured.To)
	}
}

// Forwarding the agent's own outbound message works and threads as a NEW mail
// (recipients come from the request body, subject is Fwd:).
func TestForwardOutbound_Works(t *testing.T) {
	var captured outbound.SendRequest
	srv := replyDirTestServer(t, map[string]*identity.Message{"msg_out1": outboundFixture()}, &captured)

	status, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_out1/forward", "good",
		map[string]any{"to": []string{"newperson@x.com"}, "body": "fyi"})
	if status != 200 {
		t.Fatalf("forward outbound: want 200, got %d", status)
	}
	if !reflect.DeepEqual(captured.To, []string{"newperson@x.com"}) {
		t.Errorf("To = %v, want caller-supplied [newperson@x.com]", captured.To)
	}
	if captured.Subject != "Fwd: Project update" {
		t.Errorf("Subject = %q, want Fwd: Project update", captured.Subject)
	}
}

// Regression: replying to a received (inbound) message is unchanged — targets
// the original sender via From, not the To column.
func TestReplyToInbound_Unchanged(t *testing.T) {
	var captured outbound.SendRequest
	inbound := &identity.Message{
		ID: "msg_in1", AgentID: "support@acme.com", Direction: "inbound",
		Sender: "alice@x.com", Subject: "Question", EmailMessageID: "<abc@x.com>",
		RawMessage: []byte("From: alice@x.com\r\nTo: support@acme.com\r\nSubject: Question\r\nMessage-ID: <abc@x.com>\r\n\r\nhi"),
	}
	srv := replyDirTestServer(t, map[string]*identity.Message{"msg_in1": inbound}, &captured)

	status, _ := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/reply", "good",
		map[string]any{"body": "answer"})
	if status != 200 {
		t.Fatalf("reply to inbound: want 200, got %d", status)
	}
	if !reflect.DeepEqual(captured.To, []string{"alice@x.com"}) {
		t.Errorf("To = %v, want inbound sender [alice@x.com]", captured.To)
	}
}

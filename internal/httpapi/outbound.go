package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/danielgtaylor/huma/v2"
)

// SendResultView is the unified outbound response: {status, message_id,
// method} for an immediate send/loopback, or {status:"pending_approval",
// message_id, approval_expires_at} (202) when held for HITL approval.
type SendResultView struct {
	Status            string     `json:"status"`
	MessageID         string     `json:"message_id"`
	Method            string     `json:"method,omitempty"`
	ApprovalExpiresAt *time.Time `json:"approval_expires_at,omitempty"`
}

// SendEmailRequest mirrors the legacy /send body.
type SendEmailRequest struct {
	From           string                `json:"from,omitempty"`
	To             []string              `json:"to,omitempty"`
	CC             []string              `json:"cc,omitempty"`
	BCC            []string              `json:"bcc,omitempty"`
	Subject        string                `json:"subject,omitempty"`
	Body           string                `json:"body,omitempty"`
	HTMLBody       string                `json:"html_body,omitempty"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Attachments    []outbound.Attachment `json:"attachments,omitempty"`
}

type sendInput struct {
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           SendEmailRequest
}

type sendOutput struct {
	Status int
	Body   SendResultView
}

func (s *Server) registerOutbound() {
	huma.Register(s.API, huma.Operation{
		OperationID: "sendMessage", Method: http.MethodPost, Path: "/v1/send",
		Summary: "Send a new email", Tags: []string{"messages"},
		Description: "Send a new email from an agent you own. 202 + pending_approval when the agent has HITL enabled. Honors Idempotency-Key.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleSend)
	// reply/forward are ported next: they need the legacy reply/forward
	// request-builders (ParseReplyRecipients, References chain, forward-body
	// composition, self-alias stripping) extracted so /v1 reuses them
	// verbatim — see docs/design/outbound-v1-extraction.md.
}

// validateOutboundBody runs the shared pre-send validation.
func (s *Server) validateOutboundBody(subject, body string, to, cc, bcc []string, conversationID string) *ErrorEnvelope {
	if subject == "" || body == "" {
		return NewError(http.StatusBadRequest, "invalid_request", "subject and body are required")
	}
	if strings.ContainsAny(subject, "\r\n") {
		return NewError(http.StatusBadRequest, "invalid_request", "subject must not contain CR or LF characters")
	}
	if len(to) == 0 && len(cc) == 0 {
		return NewError(http.StatusBadRequest, "invalid_request", "at least one recipient in to or cc is required")
	}
	if err := agent.ValidateRecipients(to, cc, bcc); err != nil {
		return NewError(http.StatusBadRequest, "invalid_recipient", err.Error())
	}
	if err := validateConversationID(conversationID); err != nil {
		return NewError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return nil
}

// resolveSendAgent resolves the sending agent from the explicit `from`
// address, or auto-selects when the caller owns exactly one agent (legacy
// behavior preserved for /send in Slice 1).
func (s *Server) resolveSendAgent(ctx context.Context, user *identity.User, from string) (*identity.AgentIdentity, *ErrorEnvelope) {
	if from != "" {
		ag, err := s.deps.GetAgent(ctx, identity.NormalizeEmail(from))
		if err != nil || ag == nil || ag.UserID != user.ID {
			return nil, NewError(http.StatusBadRequest, "invalid_from", "invalid from: agent not found")
		}
		return ag, nil
	}
	agents, err := s.deps.ListAgents(ctx, user.ID)
	if err != nil || len(agents) == 0 {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "from field required (no agents found)")
	}
	if len(agents) > 1 {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "from field required when user has multiple agents")
	}
	return &agents[0], nil
}

// deliver runs the domain-verified + enforce-cap checks then DeliverOutbound
// under the idempotency handshake, mapping the OutboundResult to the wire view.
func (s *Server) deliver(ctx context.Context, user *identity.User, ag *identity.AgentIdentity, req outbound.SendRequest, msgType, replyTo, route, idemKey string, rawBody []byte) (*sendOutput, error) {
	if !ag.DomainVerified {
		return nil, NewError(http.StatusForbidden, "domain_not_verified", "agent domain must be verified before sending")
	}
	if s.deps.EnforceMessageSend != nil {
		if err := s.deps.EnforceMessageSend(ctx, user.ID); err != nil {
			if env, ok := limitEnvelope(err); ok {
				return nil, env
			}
			return nil, NewError(http.StatusInternalServerError, "internal_error", "limits check failed")
		}
	}
	if s.deps.DeliverOutbound == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "outbound delivery unavailable")
	}
	status, view, err := runIdempotent(s, ctx, user.ID, idemKey, route, rawBody, func() (int, SendResultView, error) {
		res, derr := s.deps.DeliverOutbound(ctx, user, ag, req, msgType, replyTo)
		if derr != nil {
			return 0, SendResultView{}, NewError(derr.Status, derr.Code, derr.Msg)
		}
		if res.Held {
			return http.StatusAccepted, SendResultView{Status: "pending_approval", MessageID: res.PendingMessageID, ApprovalExpiresAt: res.ApprovalExpiresAt}, nil
		}
		return http.StatusOK, SendResultView{Status: "sent", MessageID: res.MessageID, Method: res.Method}, nil
	})
	if err != nil {
		return nil, err
	}
	return &sendOutput{Status: status, Body: view}, nil
}

func (s *Server) handleSend(ctx context.Context, in *sendInput) (*sendOutput, error) {
	user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	b := in.Body
	if env := s.validateOutboundBody(b.Subject, b.Body, b.To, b.CC, b.BCC, b.ConversationID); env != nil {
		return nil, env
	}
	ag, env := s.resolveSendAgent(ctx, user, b.From)
	if env != nil {
		return nil, env
	}
	req := outbound.SendRequest{
		From: b.From, To: b.To, CC: b.CC, BCC: b.BCC, Subject: b.Subject,
		Body: b.Body, HTMLBody: b.HTMLBody, ConversationID: b.ConversationID, Attachments: b.Attachments,
	}
	return s.deliver(ctx, user, ag, req, "send", "", "/v1/send", in.IdempotencyKey, in.RawBody)
}

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

	huma.Register(s.API, huma.Operation{
		OperationID: "replyToMessage", Method: http.MethodPost, Path: "/v1/agents/{address}/messages/{id}/reply",
		Summary: "Reply to a message", Tags: []string{"messages"},
		Description: "Reply to an inbound message; recipients/threading are derived from the original. 202 when held for HITL.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleReply)

	huma.Register(s.API, huma.Operation{
		OperationID: "forwardMessage", Method: http.MethodPost, Path: "/v1/agents/{address}/messages/{id}/forward",
		Summary: "Forward a message", Tags: []string{"messages"},
		Description: "Forward an inbound message to new recipients; the original is quoted. 202 when held for HITL.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleForward)

	huma.Register(s.API, huma.Operation{
		OperationID: "testAgent", Method: http.MethodPost, Path: "/v1/agents/{address}/test",
		Summary: "Send a test email to the agent's own address", Tags: []string{"agents"},
		Description: "Send a platform test email to the agent's own address to confirm inbound delivery. 202 when held for HITL.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleTestSend)
}

func (s *Server) handleTestSend(ctx context.Context, in *AddressParam) (*sendOutput, error) {
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	user, uerr := s.requireUser(ctx)
	if uerr != nil {
		return nil, uerr
	}
	if env := s.checkSendLimit(ag.ID); env != nil {
		return nil, env
	}
	if !ag.DomainVerified {
		return nil, NewError(http.StatusForbidden, "domain_not_verified", "agent domain must be verified before sending test email")
	}
	if s.deps.EnforceMessageSend != nil {
		if err := s.deps.EnforceMessageSend(ctx, user.ID); err != nil {
			if env, ok := limitEnvelope(err); ok {
				return nil, env
			}
			return nil, NewError(http.StatusInternalServerError, "internal_error", "limits check failed")
		}
	}
	if s.deps.SendTest == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "test send unavailable")
	}
	res, derr := s.deps.SendTest(ctx, ag)
	if derr != nil {
		return nil, NewError(derr.Status, derr.Code, derr.Msg)
	}
	if res.Held {
		return &sendOutput{Status: http.StatusAccepted, Body: SendResultView{Status: "pending_approval", MessageID: res.PendingMessageID, ApprovalExpiresAt: res.ApprovalExpiresAt}}, nil
	}
	return &sendOutput{Status: http.StatusOK, Body: SendResultView{Status: "sent", MessageID: res.MessageID, Method: res.Method}}, nil
}

// ReplyRequest mirrors the legacy reply body.
type ReplyRequest struct {
	Body           string                `json:"body,omitempty"`
	HTMLBody       string                `json:"html_body,omitempty"`
	ReplyAll       bool                  `json:"reply_all,omitempty"`
	CC             []string              `json:"cc,omitempty"`
	BCC            []string              `json:"bcc,omitempty"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Attachments    []outbound.Attachment `json:"attachments,omitempty"`
}

type replyInput struct {
	Address        string `path:"address"`
	ID             string `path:"id"`
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           ReplyRequest
}

// loadInbound resolves the owned agent + the inbound message (404 if missing
// or not on this agent).
func (s *Server) loadInbound(ctx context.Context, address, msgID string) (*identity.AgentIdentity, *identity.Message, *identity.User, error) {
	ag, err := s.resolveOwnedAgent(ctx, address)
	if err != nil {
		return nil, nil, nil, err
	}
	user, uerr := s.requireUser(ctx)
	if uerr != nil {
		return nil, nil, nil, uerr
	}
	if s.deps.GetInboundMessage == nil {
		return nil, nil, nil, NewError(http.StatusInternalServerError, "internal_error", "outbound unavailable")
	}
	in, err := s.deps.GetInboundMessage(ctx, msgID)
	if err != nil || in == nil || in.AgentID != ag.ID {
		return nil, nil, nil, NewError(http.StatusNotFound, "not_found", "message not found")
	}
	return ag, in, user, nil
}

func (s *Server) handleReply(ctx context.Context, in *replyInput) (*sendOutput, error) {
	ag, inbound, user, err := s.loadInbound(ctx, in.Address, in.ID)
	if err != nil {
		return nil, err
	}
	b := in.Body
	if b.Body == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "body is required")
	}
	// Validate only the user-supplied CC/BCC; the implicit To comes from the
	// (already-validated) inbound message — mirrors the legacy handler.
	if e := agent.ValidateRecipients(b.CC, b.BCC); e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_recipient", e.Error())
	}
	if e := validateConversationID(b.ConversationID); e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", e.Error())
	}
	// Build the reply request via the same outbound helpers the legacy
	// handler uses (subject normalization, recipient parsing, References).
	subject := inbound.Subject
	if subject != "" && !strings.HasPrefix(strings.ToLower(subject), "re: ") {
		subject = "Re: " + subject
	} else if subject == "" {
		subject = "Re: your message"
	}
	rr, e := outbound.ParseReplyRecipients(inbound.RawMessage, b.ReplyAll, b.CC)
	if e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_recipient", e.Error())
	}
	replyTo := rr.To
	if len(replyTo) == 0 {
		replyTo = []string{inbound.Sender}
	}
	req := outbound.SendRequest{
		To: replyTo, CC: rr.CC, BCC: b.BCC, Subject: subject, Body: b.Body, HTMLBody: b.HTMLBody,
		ReplyToMessageID: inbound.EmailMessageID,
		References:       outbound.BuildReferencesChain(inbound.RawMessage, inbound.EmailMessageID),
		ConversationID:   b.ConversationID, Attachments: b.Attachments,
	}
	req.CC = agent.StripAgentSelfAliases(req.CC, ag.EmailAddress())
	req.BCC = agent.StripAgentSelfAliases(req.BCC, ag.EmailAddress())
	return s.deliver(ctx, user, ag, req, "reply", inbound.EmailMessageID, "/v1/reply/"+in.ID, in.IdempotencyKey, in.RawBody)
}

// ForwardRequest mirrors the legacy forward body.
type ForwardRequest struct {
	To             []string              `json:"to,omitempty"`
	CC             []string              `json:"cc,omitempty"`
	BCC            []string              `json:"bcc,omitempty"`
	Body           string                `json:"body,omitempty"`
	HTMLBody       string                `json:"html_body,omitempty"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Attachments    []outbound.Attachment `json:"attachments,omitempty"`
}

type forwardInput struct {
	Address        string `path:"address"`
	ID             string `path:"id"`
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           ForwardRequest
}

func (s *Server) handleForward(ctx context.Context, in *forwardInput) (*sendOutput, error) {
	ag, inbound, user, err := s.loadInbound(ctx, in.Address, in.ID)
	if err != nil {
		return nil, err
	}
	b := in.Body
	if len(b.To) == 0 && len(b.CC) == 0 {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "at least one recipient in to or cc is required")
	}
	if e := agent.ValidateRecipients(b.To, b.CC, b.BCC); e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_recipient", e.Error())
	}
	if e := validateConversationID(b.ConversationID); e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", e.Error())
	}
	subject := outbound.BuildForwardSubject(inbound.Subject)
	fwdCtx := outbound.ExtractForwardContext(inbound.RawMessage)
	composedBody := outbound.BuildForwardBody(b.Body, fwdCtx)
	var composedHTML string
	if b.HTMLBody != "" || fwdCtx.HTML != "" || fwdCtx.Text != "" {
		composedHTML = outbound.BuildForwardHTMLBody(b.HTMLBody, fwdCtx)
	}
	req := outbound.SendRequest{
		To: b.To, CC: b.CC, BCC: b.BCC, Subject: subject, Body: composedBody, HTMLBody: composedHTML,
		ConversationID: b.ConversationID, Attachments: b.Attachments,
	}
	req.CC = agent.StripAgentSelfAliases(req.CC, ag.EmailAddress())
	req.BCC = agent.StripAgentSelfAliases(req.BCC, ag.EmailAddress())
	return s.deliver(ctx, user, ag, req, "forward", inbound.EmailMessageID, "/v1/forward/"+in.ID, in.IdempotencyKey, in.RawBody)
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
	if env := s.checkSendLimit(ag.ID); env != nil {
		return nil, env
	}
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

// checkSendLimit applies the per-agent outbound rate limit (mirrors the
// legacy sendLimit). On block it returns a 429 envelope carrying the
// retry-after seconds; the IETF RateLimit-* response headers are a tracked
// follow-up (Huma error responses can't set headers directly).
func (s *Server) checkSendLimit(agentID string) *ErrorEnvelope {
	if s.deps.SendLimit == nil {
		return nil
	}
	ok, retryAfter := s.deps.SendLimit(agentID)
	if ok {
		return nil
	}
	secs := int(retryAfter.Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}
	return NewError(http.StatusTooManyRequests, "rate_limited",
		"rate limit exceeded — max 60 sends per minute per agent").
		WithDetails(map[string]any{"retry_after_seconds": secs})
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

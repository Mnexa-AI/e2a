package httpapi

import (
	"context"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/danielgtaylor/huma/v2"
)

// approve returns the unified SendResultView (MSG-9) — approve is a send, so it
// shares send/reply/forward's result shape (with edited set).
type approveInput struct {
	Address        string `path:"email"`
	ID             string `path:"id"`
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           agent.ApproveOverrides
}

type approveOutput struct {
	Body SendResultView
}

// RejectResultView is the reject response. Reject is not a send, so it keeps
// its own shape (status + rejection_reason).
type RejectResultView struct {
	Status          string `json:"status"`
	MessageID       string `json:"message_id"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}

// RejectRequest is the reject body (MSG-10, was the inline RejectInputBody).
type RejectRequest struct {
	Reason string `json:"reason,omitempty"`
}

type rejectInput struct {
	Address string `path:"email"`
	ID      string `path:"id"`
	Body    RejectRequest
}

type rejectOutput struct {
	Body RejectResultView
}

func (s *Server) registerHITL() {
	huma.Register(s.API, huma.Operation{
		OperationID: "approveMessage", Method: http.MethodPost, Path: "/v1/agents/{email}/messages/{id}/approve",
		Summary: "Approve a held message", Tags: []string{"messages"},
		Description: "Approve a pending_approval draft (with optional reviewer overrides) and send it. Honors Idempotency-Key (the approve triggers an SES send).",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleApprove)

	huma.Register(s.API, huma.Operation{
		OperationID: "rejectMessage", Method: http.MethodPost, Path: "/v1/agents/{email}/messages/{id}/reject",
		Summary: "Reject a held message", Tags: []string{"messages"},
		Description: "Reject a pending_approval draft so it is never sent.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleReject)
}

func (s *Server) handleApprove(ctx context.Context, in *approveInput) (*approveOutput, error) {
	// HITL approval is an account-owner action. An agent-scoped credential
	// approving its OWN held outbound is self-approval, which defeats the
	// human-in-the-loop gate — so require account scope (403 for agent-scoped).
	// The human magic-link flow is a separate, token-gated handler and is
	// unaffected.
	p, err := s.requireAccountScope(ctx)
	if err != nil {
		return nil, err
	}
	user := p.User
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if env := s.checkSendLimit(ag.ID); env != nil {
		return nil, env
	}
	if s.deps.ApprovePending == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "approve unavailable")
	}
	_, view, err := runIdempotent(s, ctx, user.ID, in.IdempotencyKey, "/v1/approve/"+in.ID, in.RawBody, func() (int, SendResultView, error) {
		sent, derr := s.deps.ApprovePending(ctx, user.ID, in.ID, ag.Email, in.Body)
		if derr != nil {
			return 0, SendResultView{}, NewError(derr.Status, derr.Code, derr.Msg)
		}
		edited := sent.Edited
		return http.StatusOK, SendResultView{
			Status: "sent", MessageID: sent.ID, ProviderMessageID: sent.ProviderMessageID,
			SentAs: sent.SentAs, Method: sent.Method, Edited: &edited,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &approveOutput{Body: view}, nil
}

func (s *Server) handleReject(ctx context.Context, in *rejectInput) (*rejectOutput, error) {
	// Account-owner action — see handleApprove. Rejecting (discarding) a held
	// draft is part of the HITL decision, so it is also account-scope only.
	p, err := s.requireAccountScope(ctx)
	if err != nil {
		return nil, err
	}
	user := p.User
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if s.deps.RejectPending == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "reject unavailable")
	}
	rejected, derr := s.deps.RejectPending(ctx, user.ID, in.ID, ag.Email, in.Body.Reason)
	if derr != nil {
		return nil, NewError(derr.Status, derr.Code, derr.Msg)
	}
	return &rejectOutput{Body: RejectResultView{
		Status: rejected.Status, MessageID: rejected.ID, RejectionReason: rejected.RejectionReason,
	}}, nil
}

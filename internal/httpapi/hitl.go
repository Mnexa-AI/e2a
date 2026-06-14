package httpapi

import (
	"context"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/danielgtaylor/huma/v2"
)

// ApproveResultView is the approve→sent response (mirrors the legacy body).
type ApproveResultView struct {
	Status            string `json:"status"`
	MessageID         string `json:"message_id"`
	ProviderMessageID string `json:"provider_message_id,omitempty"`
	Method            string `json:"method,omitempty"`
	Edited            bool   `json:"edited"`
}

type approveInput struct {
	Address        string `path:"address"`
	ID             string `path:"id"`
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           agent.ApproveOverrides
}

type approveOutput struct {
	Body ApproveResultView
}

// RejectResultView mirrors the legacy reject body.
type RejectResultView struct {
	Status          string `json:"status"`
	MessageID       string `json:"message_id"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}

type rejectInput struct {
	Address string `path:"address"`
	ID      string `path:"id"`
	Body    struct {
		Reason string `json:"reason,omitempty"`
	}
}

type rejectOutput struct {
	Body RejectResultView
}

func (s *Server) registerHITL() {
	huma.Register(s.API, huma.Operation{
		OperationID: "approveMessage", Method: http.MethodPost, Path: "/v1/agents/{address}/messages/{id}/approve",
		Summary: "Approve a held message", Tags: []string{"messages"},
		Description: "Approve a pending_approval draft (with optional reviewer overrides) and send it. Honors Idempotency-Key (the approve triggers an SES send).",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleApprove)

	huma.Register(s.API, huma.Operation{
		OperationID: "rejectMessage", Method: http.MethodPost, Path: "/v1/agents/{address}/messages/{id}/reject",
		Summary: "Reject a held message", Tags: []string{"messages"},
		Description: "Reject a pending_approval draft so it is never sent.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleReject)
}

func (s *Server) handleApprove(ctx context.Context, in *approveInput) (*approveOutput, error) {
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
	if s.deps.ApprovePending == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "approve unavailable")
	}
	_, view, err := runIdempotent(s, ctx, user.ID, in.IdempotencyKey, "/v1/approve/"+in.ID, in.RawBody, func() (int, ApproveResultView, error) {
		sent, derr := s.deps.ApprovePending(ctx, user.ID, in.ID, ag.Email, in.Body)
		if derr != nil {
			return 0, ApproveResultView{}, NewError(derr.Status, derr.Code, derr.Msg)
		}
		return http.StatusOK, ApproveResultView{
			Status: sent.Status, MessageID: sent.ID, ProviderMessageID: sent.ProviderMessageID,
			Method: sent.Method, Edited: sent.Edited,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &approveOutput{Body: view}, nil
}

func (s *Server) handleReject(ctx context.Context, in *rejectInput) (*rejectOutput, error) {
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	user, uerr := s.requireUser(ctx)
	if uerr != nil {
		return nil, uerr
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

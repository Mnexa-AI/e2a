package httpapi

import (
	"context"
	"net/http"
	"reflect"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// The review queue (/v1/reviews) is the human/operator surface for messages
// held in pending_review — BOTH directions: outbound drafts awaiting send
// approval, and inbound messages held by a screening gate. It is a first-class,
// ACCOUNT-SCOPED resource (an agent credential has no reviews endpoint at all —
// the screening gate is structural, not a flag on a shared query). The agent's
// /messages surface never returns holds.

// ReviewView is one item in the review queue — non-secret summary of a held
// message of either direction.
type ReviewView struct {
	ID    string `json:"id" doc:"The review's id. This is the SAME value as the held message's id (msg_…) — a review IS the held message pending approval, so GET /v1/reviews/{id} and the message id are interchangeable. Intentional and stable."`
	Agent string `json:"agent_email" doc:"The inbox (agent address) the held message belongs to."`
	// Direction: outbound = a draft awaiting send approval; inbound = a screened
	// incoming message awaiting release.
	Direction string `json:"direction" enum:"inbound,outbound"`
	// From/To: for outbound, From is the inbox and To the recipients; for inbound,
	// From is the external sender and To the inbox.
	From           string    `json:"from"`
	To             []string  `json:"to" nullable:"false"`
	Subject        string    `json:"subject"`
	ConversationID string    `json:"conversation_id,omitempty"`
	ReviewStatus   string    `json:"review_status" doc:"Hold state of this queue item. Open set; tolerate unknown values. Currently always pending_review (the queue lists held items)."`
	Flagged        bool      `json:"flagged,omitempty"`
	FlagReason     string    `json:"flag_reason,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func reviewView(it identity.ReviewListItem) ReviewView {
	return ReviewView{
		ID:             it.ID,
		Agent:          it.AgentID,
		Direction:      it.Direction,
		From:           it.Sender,
		To:             orEmptyStrings(it.To),
		Subject:        it.Subject,
		ConversationID: it.ConversationID,
		ReviewStatus:   it.Status,
		Flagged:        it.Flagged,
		FlagReason:     it.FlagReason,
		CreatedAt:      it.CreatedAt,
	}
}

type listReviewsOutput struct{ Body Page[ReviewView] }
type reviewDetailOutput struct{ Body MessageView }

// listReviewsInput carries the standard cursor/limit (PageParams). The review
// queue is keyset-paginated on (created_at, id); it grows with the
// pending-review backlog, so it must not return the whole set in one page.
type listReviewsInput struct {
	PageParams
}

type getReviewInput struct {
	ID string `path:"id"`
}

type approveReviewInput struct {
	ID             string `path:"id"`
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key" doc:"Optional idempotency key for safe retries (unique per logical request). A retry with the same key and byte-identical body replays the first request's response instead of re-executing it. Completed keys are remembered for at least 24 hours (the published minimum dedup window). Within the window: same key + different body → 422 idempotency_key_reuse (do not retry as-is); same key while the first request is still executing → 409 idempotency_in_flight (wait, then retry unchanged). Dedup is best-effort: under idempotency-store degradation or a mid-request crash the guarantee degrades to at-least-once — a keyed retry may re-execute rather than replay."`
	Body           agent.ApproveOverrides
}

type rejectReviewInput struct {
	ID   string `path:"id"`
	Body RejectRequest
}

func (s *Server) registerReviews() {
	huma.Register(s.API, huma.Operation{
		OperationID: "listReviews", Method: http.MethodGet, Path: "/v1/reviews",
		Summary: "List messages awaiting review", Tags: []string{"reviews"},
		Description: "The review queue: every message held in pending_review across the account's inboxes — outbound drafts awaiting send approval AND inbound messages held by a screening gate. Account-scoped credentials only; agents cannot see (or resolve) holds.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleListReviews)

	huma.Register(s.API, huma.Operation{
		OperationID: "getReview", Method: http.MethodGet, Path: "/v1/reviews/{id}",
		Summary: "Get a held message (full detail)", Tags: []string{"reviews"},
		Description: "Full detail of one held message — body + recipients (and, for inbound, the screening/auth context) — for a reviewer to make a decision. Account-scoped only.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleGetReview)

	huma.Register(s.API, huma.Operation{
		OperationID: "approveReview", Method: http.MethodPost, Path: "/v1/reviews/{id}/approve",
		Summary: "Approve a held message", Tags: []string{"reviews"},
		Description:  "Approve a hold. Branches on direction: an outbound draft is sent via SES (honoring Idempotency-Key + optional reviewer overrides); an inbound hold is released to the inbox. Returns 202 with status=accepted when outbound delivery is durably queued for async submission, and 200 for a synchronous terminal sent result or an inbound release. Account-scoped only — an agent cannot approve its own hold. Approving an outbound draft applies the same per-agent send-rate limit as a direct send: 429 rate_limited when the agent is over its throughput limit (back off Retry-After seconds and retry). The merged outbound draft after applying reviewer overrides is subject to the same composed-message ceiling: 10 MiB (10485760 bytes), measured as subject + text + html + decoded attachment bytes; exceeding it returns 413 payload_too_large.",
		Security:     []map[string][]string{{"bearer": {}}},
		MaxBodyBytes: maxOutboundBytes, // matches send/reply/forward: a reviewer editing attachments needs the same 40 MB body budget as the original send path.
		Responses: map[string]*huma.Response{
			"202": s.jsonResponse(reflect.TypeOf(SendResultView{}), "SendResultView",
				"Accepted — the approved outbound message was durably queued for async submission (status=accepted); terminal outcome via GET/webhook events."),
			"400": s.jsonResponse(reflect.TypeOf(ErrorEnvelope{}), "ErrorEnvelope",
				"Bad Request — invalid reviewer overrides. error.code includes too_many_recipients when to, cc, and bcc contain more than 50 recipients combined; error.details reports max_recipients and provided."),
			// approve's 409 is shared by two codes, so it gets a merged description
			// instead of the stock idempotencyInFlightResponse.
			"409": s.jsonResponse(reflect.TypeOf(ErrorEnvelope{}), "ErrorEnvelope",
				"Conflict — branch on error.code. message_not_pending: the hold was already resolved (approved/rejected/expired) and cannot be re-approved. idempotency_in_flight: a request with this Idempotency-Key is still executing — wait for it to finish, then retry with the SAME key and byte-identical body to replay its response."),
			"413": s.jsonResponse(reflect.TypeOf(ErrorEnvelope{}), "ErrorEnvelope",
				"Payload Too Large — error.code = payload_too_large: the merged outbound draft exceeds the 10 MiB (10485760 byte) composed-message ceiling after applying reviewer overrides. The measured total is subject + text + html + decoded attachment bytes."),
			"422":     s.idempotencyReuseResponse(),
			"429":     s.rateLimitedResponse(),
			"default": s.errorEnvelopeResponse(),
		},
	}, s.handleApproveReview)

	huma.Register(s.API, huma.Operation{
		OperationID: "rejectReview", Method: http.MethodPost, Path: "/v1/reviews/{id}/reject",
		Summary: "Reject a held message", Tags: []string{"reviews"},
		Description: "Reject a hold. An outbound draft is discarded (never sent); an inbound hold is dropped (never reaches the agent; payload retained hidden for forensics). Account-scoped only.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleRejectReview)
}

func (s *Server) handleListReviews(ctx context.Context, in *listReviewsInput) (*listReviewsOutput, error) {
	p, err := s.requireAccountScope(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.ListReviews == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "reviews are not available on this deployment")
	}
	afterCreatedAt, afterID, err := s.decodeKeyset(in.Cursor)
	if err != nil {
		return nil, err
	}
	limit := effectiveLimit(in.Limit)
	// Fetch limit+1 to detect a further page.
	items, err := s.deps.ListReviews(ctx, p.User.ID, limit+1, afterCreatedAt, afterID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list reviews")
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	views := make([]ReviewView, 0, len(items))
	for _, it := range items {
		views = append(views, reviewView(it))
	}
	var nextCursor string
	if hasMore {
		last := items[len(items)-1]
		if nextCursor, err = s.encodeKeyset(last.CreatedAt, last.ID); err != nil {
			return nil, err
		}
	}
	return &listReviewsOutput{Body: NewPage(views, nextCursor)}, nil
}

// requireOwnedReview resolves a held message by id, scoped to the account, and
// returns it (404 if it isn't a hold the account owns). The ownership check is
// the tenant guard for the id-only review routes.
func (s *Server) requireOwnedReview(ctx context.Context, userID, id string) (*identity.Message, error) {
	if s.deps.GetReviewWithContent == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "reviews are not available on this deployment")
	}
	msg, err := s.deps.GetReviewWithContent(ctx, userID, id)
	if err != nil || msg == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "review not found")
	}
	return msg, nil
}

func (s *Server) handleGetReview(ctx context.Context, in *getReviewInput) (*reviewDetailOutput, error) {
	p, err := s.requireAccountScope(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := s.requireOwnedReview(ctx, p.User.ID, in.ID)
	if err != nil {
		return nil, err
	}
	view := messageViewFromIdentity(msg)
	// messageViewFromIdentity only exposes review_status for outbound (the
	// agent /messages contract never surfaced inbound holds). A /reviews item
	// is always a held message of EITHER direction, and its review lifecycle
	// lives in m.Status — surface it so clients see pending_review on inbound
	// holds too.
	view.HITLStatus = msg.Status
	return &reviewDetailOutput{Body: view}, nil
}

func (s *Server) handleApproveReview(ctx context.Context, in *approveReviewInput) (*approveOutput, error) {
	p, err := s.requireAccountScope(ctx)
	if err != nil {
		return nil, err
	}
	// Ownership + held-state guard; gives us the owning agent (== inbox address).
	msg, err := s.requireOwnedReview(ctx, p.User.ID, in.ID)
	if err != nil {
		return nil, err
	}
	if msg.Direction == "outbound" {
		to, cc, bcc := msg.ToRecipients, msg.CC, msg.BCC
		if in.Body.To != nil {
			to = *in.Body.To
		}
		if in.Body.CC != nil {
			cc = *in.Body.CC
		}
		if in.Body.BCC != nil {
			bcc = *in.Body.BCC
		}
		if env := recipientCountError(to, cc, bcc); env != nil {
			return nil, env
		}
	}
	status, view, err := s.approveHeld(ctx, p.User.ID, in.ID, msg.AgentID, in.Body, in.IdempotencyKey, in.RawBody)
	if err != nil {
		return nil, err
	}
	return &approveOutput{Status: status, Body: view}, nil
}

func (s *Server) handleRejectReview(ctx context.Context, in *rejectReviewInput) (*rejectOutput, error) {
	p, err := s.requireAccountScope(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := s.requireOwnedReview(ctx, p.User.ID, in.ID)
	if err != nil {
		return nil, err
	}
	view, err := s.rejectHeld(ctx, p.User.ID, in.ID, msg.AgentID, in.Body.Reason)
	if err != nil {
		return nil, err
	}
	return &rejectOutput{Body: view}, nil
}

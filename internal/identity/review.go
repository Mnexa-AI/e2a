package identity

import (
	"context"
	"fmt"
)

// ErrNotPendingReview is returned when an approve/reject/expire targets an inbound
// message that is not (or is no longer) in pending_review.
var ErrNotPendingReview = fmt.Errorf("message is not pending review")

// heldInboundStatuses is the SQL value-list of inbound review-hold statuses whose
// messages are NOT agent-visible. EVERY agent-facing read path MUST exclude these —
// a held message must be unreachable by the agent (push AND every poll/read path)
// until released. The human review queue (future endpoint) uses dedicated queries
// that intentionally include them. See docs/design/2026-06-20-agent-screening-hitl.md §4.4.
const heldInboundStatuses = `'pending_review', 'review_rejected', 'review_expired_rejected'`

// ListExpiredReviews returns inbound pending_review messages whose
// approval_expires_at has passed, joined with their agent's hitl_expiration_action
// — the inbound analogue of ListExpiredPending. The expiry worker uses these to
// auto-resolve held messages per the agent's policy.
func (s *Store) ListExpiredReviews(ctx context.Context, limit int) ([]ExpirationCandidate, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT m.id, m.agent_id, a.hitl_expiration_action
		 FROM messages m
		 JOIN agent_identities a ON a.id = m.agent_id
		 WHERE m.status = 'pending_review'
		   AND m.approval_expires_at < now()
		 ORDER BY m.approval_expires_at ASC
		 LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExpirationCandidate
	for rows.Next() {
		var c ExpirationCandidate
		if err := rows.Scan(&c.MessageID, &c.AgentID, &c.ExpirationAction); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// transitionReview flips a pending_review inbound message to a terminal review
// status. The status guard makes it compare-and-set: the first writer wins, a
// concurrent/duplicate transition sees RowsAffected=0 → ErrNotPendingReview.
//
// agentID, when non-empty, scopes the update to that agent — the tenant-isolation
// guard for human-driven transitions (a reviewer may only release a message
// belonging to an agent they own; the handler resolves the owned agent first).
// Worker-driven (TTL) transitions pass "" (system-scoped) and a nil reviewerID.
func (s *Store) transitionReview(ctx context.Context, messageID, agentID, newStatus string, reviewerID *string, rejectionReason string) error {
	args := []any{messageID, newStatus, reviewerID, rejectionReason}
	where := `id = $1 AND direction = 'inbound' AND status = 'pending_review'`
	if agentID != "" {
		args = append(args, agentID)
		where += ` AND agent_id = $5`
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE messages
		    SET status = $2,
		        reviewed_at = now(),
		        reviewed_by_user_id = $3,
		        rejection_reason = NULLIF($4, '')
		  WHERE `+where,
		args...,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotPendingReview
	}
	return nil
}

// ApproveInboundReview releases a held inbound message to the agent (status
// review_approved → visible in the inbox). Scoped to agentID for tenant isolation;
// reviewerID identifies the human. Held content is retained (the message is now
// delivered).
func (s *Store) ApproveInboundReview(ctx context.Context, messageID, agentID, reviewerID string) error {
	return s.transitionReview(ctx, messageID, agentID, MessageStatusReviewApproved, &reviewerID, "")
}

// RejectInboundReview drops a held inbound message (status review_rejected → stays
// hidden from the agent). Scoped to agentID for tenant isolation; reviewerID
// identifies the human; reason is operator-facing. The raw payload is retained
// (hidden) until the message TTL for security forensics — see design §4.4.
func (s *Store) RejectInboundReview(ctx context.Context, messageID, agentID, reviewerID, reason string) error {
	return s.transitionReview(ctx, messageID, agentID, MessageStatusReviewRejected, &reviewerID, reason)
}

// ExpireApproveReview is the worker-side TTL auto-approve: releases the message
// (status review_expired_approved) with no human reviewer. System-scoped.
func (s *Store) ExpireApproveReview(ctx context.Context, messageID string) error {
	return s.transitionReview(ctx, messageID, "", MessageStatusReviewExpiredApproved, nil, "")
}

// ExpireRejectReview is the worker-side TTL auto-reject: drops the message
// (status review_expired_rejected) with no human reviewer. System-scoped.
func (s *Store) ExpireRejectReview(ctx context.Context, messageID, reason string) error {
	return s.transitionReview(ctx, messageID, "", MessageStatusReviewExpiredRejected, nil, reason)
}

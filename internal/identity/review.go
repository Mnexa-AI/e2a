package identity

import (
	"context"
	"fmt"
)

// ErrNotPendingReview is returned when an approve/reject/expire targets an inbound
// message that is not (or is no longer) in pending_review.
var ErrNotPendingReview = fmt.Errorf("message is not pending review")

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
// reviewerID is nil for worker-driven (TTL) transitions.
func (s *Store) transitionReview(ctx context.Context, messageID, newStatus string, reviewerID *string, rejectionReason string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE messages
		    SET status = $2,
		        reviewed_at = now(),
		        reviewed_by_user_id = $3,
		        rejection_reason = NULLIF($4, '')
		  WHERE id = $1 AND direction = 'inbound' AND status = 'pending_review'`,
		messageID, newStatus, reviewerID, rejectionReason,
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
// review_approved → visible in the inbox). reviewerID identifies the human.
func (s *Store) ApproveInboundReview(ctx context.Context, messageID, reviewerID string) error {
	return s.transitionReview(ctx, messageID, MessageStatusReviewApproved, &reviewerID, "")
}

// RejectInboundReview drops a held inbound message (status review_rejected → stays
// hidden from the agent). reviewerID identifies the human; reason is operator-facing.
func (s *Store) RejectInboundReview(ctx context.Context, messageID, reviewerID, reason string) error {
	return s.transitionReview(ctx, messageID, MessageStatusReviewRejected, &reviewerID, reason)
}

// ExpireApproveReview is the worker-side TTL auto-approve: releases the message
// (status review_expired_approved) with no human reviewer.
func (s *Store) ExpireApproveReview(ctx context.Context, messageID string) error {
	return s.transitionReview(ctx, messageID, MessageStatusReviewExpiredApproved, nil, "")
}

// ExpireRejectReview is the worker-side TTL auto-reject: drops the message
// (status review_expired_rejected) with no human reviewer.
func (s *Store) ExpireRejectReview(ctx context.Context, messageID, reason string) error {
	return s.transitionReview(ctx, messageID, MessageStatusReviewExpiredRejected, nil, reason)
}

package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func seedReviewAgent(t *testing.T, store *identity.Store, ctx context.Context, domain string) (userID, agentID string) {
	t.Helper()
	user, err := store.CreateOrGetUser(ctx, "o@"+domain, "O", "g-"+domain)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	ag, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return user.ID, ag.ID
}

func createInbound(t *testing.T, store *identity.Store, ctx context.Context, agentID, sender, subj string, sc identity.InboundScreening) string {
	t.Helper()
	m, err := store.CreateInboundMessage(ctx, "", agentID, sender, agentID, "", subj, "", "unread",
		[]byte("Subject: "+subj+"\r\n\r\nx"), nil, nil, false, "", []string{agentID}, nil, nil, sc)
	if err != nil {
		t.Fatalf("CreateInboundMessage(%s): %v", subj, err)
	}
	return m.ID
}

func inboxIDs(t *testing.T, store *identity.Store, ctx context.Context, agentID string) map[string]bool {
	t.Helper()
	msgs, err := store.GetMessagesByAgent(ctx, identity.MessageListFilter{AgentID: agentID, Direction: "inbound", Status: "all", Limit: 100})
	if err != nil {
		t.Fatalf("GetMessagesByAgent: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range msgs {
		ids[m.ID] = true
	}
	return ids
}

// TestInboundReview_HeldExcludedFromInbox is the delivery-boundary contract: held
// (pending_review) and dropped (review_rejected) inbound messages do NOT appear in
// the agent inbox; approving a held one makes it visible.
func TestInboundReview_HeldExcludedFromInbox(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := seedReviewAgent(t, store, ctx, "held.example.com")

	exp := time.Now().Add(time.Hour)
	normalID := createInbound(t, store, ctx, agentID, "ok@x.com", "normal", identity.InboundScreening{})
	heldID := createInbound(t, store, ctx, agentID, "evil@x.com", "held", identity.InboundScreening{
		Status: identity.MessageStatusPendingReview, ScanAction: "review",
		ReviewReason: identity.ReviewReasonInboundScan, ApprovalExpiresAt: &exp,
	})
	blockedID := createInbound(t, store, ctx, agentID, "evil2@x.com", "blocked", identity.InboundScreening{
		Status: identity.MessageStatusReviewRejected, ScanAction: "block", ReviewReason: identity.ReviewReasonInboundScan,
	})

	ids := inboxIDs(t, store, ctx, agentID)
	if !ids[normalID] {
		t.Errorf("normal message should be in the inbox")
	}
	if ids[heldID] {
		t.Errorf("pending_review message must be excluded from the inbox")
	}
	if ids[blockedID] {
		t.Errorf("blocked (review_rejected) message must be excluded from the inbox")
	}

	// Approve the held message → it becomes visible.
	if err := store.ApproveInboundReview(ctx, heldID, userID); err != nil {
		t.Fatalf("ApproveInboundReview: %v", err)
	}
	if !inboxIDs(t, store, ctx, agentID)[heldID] {
		t.Errorf("approved message should now appear in the inbox")
	}
}

// TestInboundReview_TransitionsCompareAndSet covers approve/reject + the
// compare-and-set guard (a second transition on the same row is a no-op).
func TestInboundReview_TransitionsCompareAndSet(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := seedReviewAgent(t, store, ctx, "trans.example.com")
	exp := time.Now().Add(time.Hour)

	id := createInbound(t, store, ctx, agentID, "e@x.com", "a", identity.InboundScreening{
		Status: identity.MessageStatusPendingReview, ApprovalExpiresAt: &exp,
	})
	if err := store.ApproveInboundReview(ctx, id, userID); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if err := store.ApproveInboundReview(ctx, id, userID); err != identity.ErrNotPendingReview {
		t.Errorf("second approve = %v, want ErrNotPendingReview", err)
	}

	id2 := createInbound(t, store, ctx, agentID, "e2@x.com", "b", identity.InboundScreening{
		Status: identity.MessageStatusPendingReview, ApprovalExpiresAt: &exp,
	})
	if err := store.RejectInboundReview(ctx, id2, userID, "looks malicious"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	var st, reason string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(rejection_reason,'') FROM messages WHERE id=$1`, id2).Scan(&st, &reason); err != nil {
		t.Fatalf("read: %v", err)
	}
	if st != identity.MessageStatusReviewRejected || reason != "looks malicious" {
		t.Errorf("rejected row = status %q reason %q", st, reason)
	}
}

// TestListExpiredReviews_AndExpire covers the worker's store surface: an overdue
// pending_review is listed and resolved per the agent's hitl_expiration_action
// (default 'reject' → review_expired_rejected).
func TestListExpiredReviews_AndExpire(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	_, agentID := seedReviewAgent(t, store, ctx, "expire.example.com")

	past := time.Now().Add(-time.Hour)
	id := createInbound(t, store, ctx, agentID, "e@x.com", "overdue", identity.InboundScreening{
		Status: identity.MessageStatusPendingReview, ApprovalExpiresAt: &past,
	})

	cands, err := store.ListExpiredReviews(ctx, 10)
	if err != nil {
		t.Fatalf("ListExpiredReviews: %v", err)
	}
	var action string
	var found bool
	for _, c := range cands {
		if c.MessageID == id {
			found, action = true, c.ExpirationAction
		}
	}
	if !found {
		t.Fatalf("overdue review not listed")
	}

	if action == identity.HITLExpirationApprove {
		if err := store.ExpireApproveReview(ctx, id); err != nil {
			t.Fatalf("ExpireApproveReview: %v", err)
		}
	} else {
		if err := store.ExpireRejectReview(ctx, id, "ttl_expired"); err != nil {
			t.Fatalf("ExpireRejectReview: %v", err)
		}
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, id).Scan(&st); err != nil {
		t.Fatalf("read: %v", err)
	}
	// Default hitl_expiration_action is 'reject'.
	if st != identity.MessageStatusReviewExpiredRejected {
		t.Errorf("expired status = %q, want review_expired_rejected", st)
	}
	// No longer overdue-listed once terminal.
	cands2, _ := store.ListExpiredReviews(ctx, 10)
	for _, c := range cands2 {
		if c.MessageID == id {
			t.Errorf("terminal review should not be re-listed")
		}
	}
}

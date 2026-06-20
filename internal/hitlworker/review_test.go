package hitlworker_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// TestWorkerExpiresInboundReview verifies the Slice 4b sweep: an overdue
// pending_review inbound hold is auto-resolved by RunOnce per the agent's
// hitl_expiration_action — approve → review_expired_approved (released), reject →
// review_expired_rejected (dropped). No SMTP send is involved.
func TestWorkerExpiresInboundReview(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	ctx := context.Background()

	cases := []struct {
		slug, action, wantStatus string
	}{
		{"revrej", identity.HITLExpirationReject, identity.MessageStatusReviewExpiredRejected},
		{"revapp", identity.HITLExpirationApprove, identity.MessageStatusReviewExpiredApproved},
	}
	ids := make(map[string]string, len(cases))
	for _, c := range cases {
		agent := prepareAgent(t, store, c.slug, c.action)
		exp := time.Now().Add(time.Hour)
		m, err := store.CreateInboundMessage(ctx, "", agent.ID, "evil@x.com", agent.ID, "", "held", "", "unread",
			[]byte("Subject: held\r\n\r\nx"), nil, nil, false, "", []string{agent.ID}, nil, nil,
			identity.InboundScreening{Status: identity.MessageStatusPendingReview, ApprovalExpiresAt: &exp})
		if err != nil {
			t.Fatalf("create inbound (%s): %v", c.slug, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE messages SET approval_expires_at = now() - interval '1 hour' WHERE id=$1`, m.ID); err != nil {
			t.Fatalf("backdate (%s): %v", c.slug, err)
		}
		ids[c.slug] = m.ID
	}

	w.RunOnce(ctx)

	for _, c := range cases {
		var st string
		if err := pool.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, ids[c.slug]).Scan(&st); err != nil {
			t.Fatalf("read (%s): %v", c.slug, err)
		}
		if st != c.wantStatus {
			t.Errorf("%s: status = %q, want %q", c.slug, st, c.wantStatus)
		}
	}
}

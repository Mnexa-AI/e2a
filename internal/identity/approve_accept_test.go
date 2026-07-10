package identity_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestApproveAndAccept transitions a pending_review outbound hold to
// approved+accepted, enqueues via the callback, stamps the returned job id, and is
// a no-op (ErrNotPendingApproval) once the hold is already resolved.
func TestApproveAndAccept(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)

	user, err := store.CreateOrGetUser(ctx, "owner-aa@example.com", "Owner", "google-aa")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	domain := "aa.example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	ag, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	msg, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{"a@gmail.com"}, nil, nil, "Subj", "body", "", nil,
		"send", "conv-aa", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}

	var enqueued int
	enqueue := func(_ context.Context, _ pgx.Tx, _ string) (int64, error) {
		enqueued++
		return 4242, nil
	}
	acc := identity.AcceptedSend{
		To: []string{"a@gmail.com"}, Subject: "Subj", Method: "smtp",
		EnvelopeFrom: "bot@aa.example.com", SentAs: "relay", Raw: []byte("raw-mime"),
	}

	out, err := store.ApproveAndAccept(ctx, msg.ID, user.ID, identity.MessageStatusReviewApproved, false, acc, enqueue)
	if err != nil {
		t.Fatalf("ApproveAndAccept: %v", err)
	}
	if out.Status != identity.MessageStatusReviewApproved {
		t.Errorf("status = %q, want %q", out.Status, identity.MessageStatusReviewApproved)
	}
	if out.DeliveryStatus != "accepted" {
		t.Errorf("delivery_status = %q, want accepted", out.DeliveryStatus)
	}
	if enqueued != 1 {
		t.Errorf("enqueue called %d times, want 1", enqueued)
	}

	// DB reflects the transition + stamped send_job_id + accepted delivery + scrubbed body.
	var (
		status, deliveryStatus string
		sendJobID              *int64
		bodyText               *string
	)
	if err := pool.QueryRow(ctx,
		`SELECT status, delivery_status, send_job_id, body_text FROM messages WHERE id=$1`, msg.ID,
	).Scan(&status, &deliveryStatus, &sendJobID, &bodyText); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != identity.MessageStatusReviewApproved || deliveryStatus != "accepted" {
		t.Errorf("db status/delivery = %q/%q", status, deliveryStatus)
	}
	if sendJobID == nil || *sendJobID != 4242 {
		t.Errorf("send_job_id = %v, want 4242", sendJobID)
	}
	if bodyText != nil {
		t.Errorf("body_text not scrubbed: %v", *bodyText)
	}

	// Idempotent: a second attempt (row no longer pending_review) is a no-op.
	if _, err := store.ApproveAndAccept(ctx, msg.ID, user.ID, identity.MessageStatusReviewApproved, false, acc, enqueue); err != identity.ErrNotPendingApproval {
		t.Errorf("second ApproveAndAccept err = %v, want ErrNotPendingApproval", err)
	}
	if enqueued != 1 {
		t.Errorf("enqueue called %d times after no-op attempt, want still 1", enqueued)
	}
}

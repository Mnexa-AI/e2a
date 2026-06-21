package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func TestListExpiredPending(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "expire-list")

	// Pending but not yet expired
	fresh, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"a@example.com"}, nil, nil, "fresh", "b", "", nil,
		"send", "", "", 3600)
	// Pending and expired (backdate approval_expires_at)
	stale1, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"b@example.com"}, nil, nil, "stale1", "b", "", nil,
		"send", "", "", 60)
	stale2, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"c@example.com"}, nil, nil, "stale2", "b", "", nil,
		"send", "", "", 60)
	pool.Exec(ctx, `UPDATE messages SET approval_expires_at = $1 WHERE id = ANY($2)`,
		time.Now().Add(-10*time.Minute), []string{stale1.ID, stale2.ID})

	got, err := store.ListExpiredPending(ctx, 50)
	if err != nil {
		t.Fatalf("ListExpiredPending: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 expired, got %d: %+v", len(got), got)
	}
	ids := map[string]bool{got[0].MessageID: true, got[1].MessageID: true}
	if !ids[stale1.ID] || !ids[stale2.ID] {
		t.Errorf("expected both stale1 and stale2 in result, got %+v", got)
	}
	if ids[fresh.ID] {
		t.Error("fresh message should not appear")
	}
	for _, c := range got {
		if c.AgentID != a.ID {
			t.Errorf("agent_id = %q, want %q", c.AgentID, a.ID)
		}
		if c.ExpirationAction != identity.HITLExpirationReject {
			t.Errorf("expiration_action = %q, want %q", c.ExpirationAction, identity.HITLExpirationReject)
		}
	}

	// Changing the agent's expiration action should propagate
	if err := store.UpdateAgentHITL(ctx, a.ID, user.ID, identity.HITLDefaultTTLSeconds, identity.HITLExpirationApprove); err != nil {
		t.Fatal(err)
	}
	got2, _ := store.ListExpiredPending(ctx, 50)
	for _, c := range got2 {
		if c.ExpirationAction != identity.HITLExpirationApprove {
			t.Errorf("after toggle: action = %q, want approve", c.ExpirationAction)
		}
	}
}

// TestListExpiredPendingOrdering verifies the worker sees the
// earliest-expired rows first. Matters because the batch size is bounded
// and we want the oldest backlogs to drain first.
func TestListExpiredPendingOrdering(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "expire-order")

	// Create three pending rows; backdate their approval_expires_at to
	// distinct points in the past. Insertion order deliberately differs
	// from expected return order.
	msgNewest, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"n@example.com"}, nil, nil, "newest", "b", "", nil, "send", "", "", 60)
	msgMiddle, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"m@example.com"}, nil, nil, "middle", "b", "", nil, "send", "", "", 60)
	msgOldest, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"o@example.com"}, nil, nil, "oldest", "b", "", nil, "send", "", "", 60)

	base := time.Now()
	pool.Exec(ctx, `UPDATE messages SET approval_expires_at = $1 WHERE id = $2`,
		base.Add(-1*time.Minute), msgNewest.ID)
	pool.Exec(ctx, `UPDATE messages SET approval_expires_at = $1 WHERE id = $2`,
		base.Add(-5*time.Minute), msgMiddle.ID)
	pool.Exec(ctx, `UPDATE messages SET approval_expires_at = $1 WHERE id = $2`,
		base.Add(-30*time.Minute), msgOldest.ID)

	got, err := store.ListExpiredPending(ctx, 50)
	if err != nil {
		t.Fatalf("ListExpiredPending: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	wantOrder := []string{msgOldest.ID, msgMiddle.ID, msgNewest.ID}
	for i, c := range got {
		if c.MessageID != wantOrder[i] {
			t.Errorf("pos %d: MessageID = %q, want %q", i, c.MessageID, wantOrder[i])
		}
	}
}

func TestExpireApproveAndSendHappyPath(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "expire-ok")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Held", "body", "<p>body</p>", nil,
		"send", "conv_exp", "", 60)
	pool.Exec(ctx, `UPDATE messages SET approval_expires_at = $1 WHERE id = $2`,
		time.Now().Add(-1*time.Minute), msg.ID)

	var sentSubject string
	sent, err := store.ExpireApproveAndSend(ctx, msg.ID,
		func(m *identity.Message) (identity.SendResult, error) {
			sentSubject = m.Subject
			return identity.SendResult{
				ProviderMessageID: "<ses-exp@amazonses.com>",
				Method:            "smtp",
				To:                m.ToRecipients,
			}, nil
		})
	if err != nil {
		t.Fatalf("ExpireApproveAndSend: %v", err)
	}
	if sentSubject != "Held" {
		t.Errorf("send got subject %q", sentSubject)
	}
	if sent.Status != identity.MessageStatusExpiredApproved {
		t.Errorf("status = %q", sent.Status)
	}

	var dbStatus string
	var dbBodyText *string
	err = pool.QueryRow(ctx,
		`SELECT status, body_text FROM messages WHERE id = $1`, msg.ID,
	).Scan(&dbStatus, &dbBodyText)
	if err != nil {
		t.Fatal(err)
	}
	if dbStatus != identity.MessageStatusExpiredApproved {
		t.Errorf("db status = %q", dbStatus)
	}
	if dbBodyText != nil {
		t.Errorf("body_text should be scrubbed, got %v", dbBodyText)
	}
}

func TestExpireApproveAndSendSkipsFreshPending(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "expire-skip-fresh")

	fresh, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"a@example.com"}, nil, nil, "fresh", "b", "", nil,
		"send", "", "", 3600)

	_, err := store.ExpireApproveAndSend(ctx, fresh.ID,
		func(m *identity.Message) (identity.SendResult, error) {
			t.Fatal("send callback should not fire for fresh pending row")
			return identity.SendResult{}, nil
		})
	if !errors.Is(err, identity.ErrNotPendingApproval) {
		t.Errorf("expected ErrNotPendingApproval, got %v", err)
	}
}

func TestExpireApproveAndSendSkipsAlreadyTerminal(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "expire-terminal")

	// A message already in 'sent' state — the expiration query should ignore it
	sent, _ := store.CreateOutboundMessage(ctx, a.ID,
		[]string{"x@example.com"}, nil, nil, "already", "send", "smtp", "<p>", "", nil)

	_, err := store.ExpireApproveAndSend(ctx, sent.ID,
		func(m *identity.Message) (identity.SendResult, error) {
			t.Fatal("callback should not fire for non-pending row")
			return identity.SendResult{}, nil
		})
	if !errors.Is(err, identity.ErrNotPendingApproval) {
		t.Errorf("expected ErrNotPendingApproval, got %v", err)
	}
}

func TestExpireApproveAndSendSendFailureRollsBack(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "expire-fail")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"a@example.com"}, nil, nil, "held", "body", "", nil,
		"send", "", "", 60)
	pool.Exec(ctx, `UPDATE messages SET approval_expires_at = $1 WHERE id = $2`,
		time.Now().Add(-1*time.Minute), msg.ID)

	boom := errors.New("smtp out of service")
	_, err := store.ExpireApproveAndSend(ctx, msg.ID,
		func(m *identity.Message) (identity.SendResult, error) {
			return identity.SendResult{}, boom
		})
	if !errors.Is(err, boom) {
		t.Errorf("expected send error, got %v", err)
	}

	// Row still pending, body still present — worker should follow up with ExpireReject
	var status string
	var bodyText *string
	err = pool.QueryRow(ctx,
		`SELECT status, body_text FROM messages WHERE id = $1`, msg.ID,
	).Scan(&status, &bodyText)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusPendingApproval {
		t.Errorf("status = %q, want still pending after send failure", status)
	}
	if bodyText == nil || *bodyText != "body" {
		t.Errorf("body_text should be intact, got %v", bodyText)
	}
}

func TestExpireRejectHappyPath(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "expire-rej")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"x@example.com"}, nil, nil, "x", "body", "<p>html</p>",
		[]byte(`[{"filename":"x","content_type":"text/plain","data":"aA=="}]`),
		"send", "", "", 60)
	pool.Exec(ctx, `UPDATE messages SET approval_expires_at = $1 WHERE id = $2`,
		time.Now().Add(-1*time.Minute), msg.ID)

	got, err := store.ExpireReject(ctx, msg.ID, "ttl_expired")
	if err != nil {
		t.Fatalf("ExpireReject: %v", err)
	}
	if got.Status != identity.MessageStatusExpiredRejected {
		t.Errorf("status = %q", got.Status)
	}
	if got.RejectionReason != "ttl_expired" {
		t.Errorf("reason = %q", got.RejectionReason)
	}

	var bodyText, bodyHTML *string
	var attachments []byte
	err = pool.QueryRow(ctx,
		`SELECT body_text, body_html, attachments_json FROM messages WHERE id = $1`, msg.ID,
	).Scan(&bodyText, &bodyHTML, &attachments)
	if err != nil {
		t.Fatal(err)
	}
	if bodyText != nil || bodyHTML != nil || attachments != nil {
		t.Errorf("not scrubbed: text=%v html=%v att=%v", bodyText, bodyHTML, attachments)
	}
}

func TestExpireRejectRaceReturnsErrNotPending(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "expire-race")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"x@example.com"}, nil, nil, "x", "b", "", nil,
		"send", "", "", 60)

	// Simulate: human rejected through the user-scoped API before the
	// worker got to it.
	if _, err := store.RejectPending(ctx, msg.ID, user.ID, "human override"); err != nil {
		t.Fatal(err)
	}

	_, err := store.ExpireReject(ctx, msg.ID, "ttl_expired")
	if !errors.Is(err, identity.ErrNotPendingApproval) {
		t.Errorf("expected ErrNotPendingApproval when row already terminal, got %v", err)
	}
}

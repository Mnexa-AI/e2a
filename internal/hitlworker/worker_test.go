package hitlworker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/hitlworker"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setupWorker wires a worker + fake SMTP on a fresh test database. Returns
// the worker, the shared store, the underlying pool (for backdating
// expirations in tests), and the smtpDone accessor for asserting what
// reached SMTP.
func setupWorker(t *testing.T) (
	*hitlworker.Worker,
	*identity.Store,
	*pgxpool.Pool,
	func() []testutil.SMTPMessage,
) {
	t.Helper()
	smtpAddr, smtpDone := testutil.FakeSMTPServer(t)
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{Host: smtpAddr.Host, Port: smtpAddr.Port})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	w := hitlworker.New(store, sender, usage.NewUsageTracker(usage.NewStore(pool)), "test.e2a.dev")
	w.SetOutboundEnqueuer(&fakeEnq{})
	return w, store, pool, smtpDone
}

func prepareAgent(t *testing.T, store *identity.Store, slug, expirationAction string) *identity.AgentIdentity {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner-"+slug+"@example.com", "Owner", "google-"+slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, slug+".example.com", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyDomain(ctx, slug+".example.com", user.ID); err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateAgent(ctx, "bot@"+slug+".example.com", slug+".example.com", "", "https://example.com/webhook", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAgentHITL(ctx, a.ID, user.ID, identity.HITLDefaultTTLSeconds, expirationAction); err != nil {
		t.Fatal(err)
	}
	refreshed, err := store.GetAgentByID(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return refreshed
}

// backdateExpiry rewinds a pending row's approval_expires_at into the past
// so the worker's expired-query picks it up without real-time sleeps.
func backdateExpiry(t *testing.T, pool *pgxpool.Pool, messageID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE messages SET approval_expires_at = $1 WHERE id = $2`,
		time.Now().Add(-5*time.Minute), messageID)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}
}

func TestWorkerAutoRejectsExpiredPending(t *testing.T) {
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()

	agent := prepareAgent(t, store, "auto-reject", identity.HITLExpirationReject)
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Held", "body", "<p>html</p>", nil,
		"send", "", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)

	w.RunOnce(ctx)

	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("SMTP should not be hit on auto-reject, got %d messages", len(msgs))
	}

	var status string
	var reason, bodyText, bodyHTML *string
	err = pool.QueryRow(ctx,
		`SELECT status, rejection_reason, body_text, body_html FROM messages WHERE id = $1`,
		msg.ID,
	).Scan(&status, &reason, &bodyText, &bodyHTML)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusReviewExpiredRejected {
		t.Errorf("status = %q, want %q", status, identity.MessageStatusReviewExpiredRejected)
	}
	if reason == nil || *reason != "ttl_expired" {
		t.Errorf("reason = %v, want 'ttl_expired'", reason)
	}
	if bodyText != nil || bodyHTML != nil {
		t.Errorf("body columns not scrubbed: text=%v html=%v", bodyText, bodyHTML)
	}
}

func TestWorkerAutoApprovesExpiredPending(t *testing.T) {
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()

	agent := prepareAgent(t, store, "auto-approve", identity.HITLExpirationApprove)
	msg, _ := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Auto-send subject", "plain body", "<p>html body</p>", nil,
		"send", "", "", "", 60)
	backdateExpiry(t, pool, msg.ID)

	w.RunOnce(ctx)

	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("auto-approve submitted %d SMTP messages inline, want zero", len(msgs))
	}

	var status, deliveryStatus string
	var providerID string
	var bodyText *string
	err := pool.QueryRow(ctx,
		`SELECT status, delivery_status, provider_message_id, body_text FROM messages WHERE id = $1`,
		msg.ID,
	).Scan(&status, &deliveryStatus, &providerID, &bodyText)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusReviewExpiredApproved {
		t.Errorf("status = %q, want %q", status, identity.MessageStatusReviewExpiredApproved)
	}
	if deliveryStatus != "accepted" {
		t.Errorf("delivery_status = %q, want accepted", deliveryStatus)
	}
	if providerID != "" {
		t.Errorf("provider_message_id = %q, want empty before worker delivery", providerID)
	}
	if bodyText != nil {
		t.Errorf("body_text not scrubbed: %v", bodyText)
	}
}

// TestWorkerAutoApproveCarriesReplyTo pins the Reply-To override through the
// queue-first TTL auto-approve path end-to-end: pending row with a caller override →
// TTL expiry → accepted raw MIME carries the
// override, not the agent's own address. This is the exact seam where a missing
// reply_to in ExpireApproveAndSend's locked SELECT silently dropped the override.
func TestWorkerAutoApproveCarriesReplyTo(t *testing.T) {
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()

	agent := prepareAgent(t, store, "auto-approve-rt", identity.HITLExpirationApprove)
	const override = "Support <support@acme.com>"
	msg, _ := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil,
		"RT subject", "plain body", "", nil,
		"send", "", "", override, 60)
	backdateExpiry(t, pool, msg.ID)

	w.RunOnce(ctx)

	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("auto-approve submitted %d SMTP messages inline, want zero", len(msgs))
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT raw_message FROM messages WHERE id=$1`, msg.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Reply-To: "+override) {
		t.Errorf("accepted MIME missing overridden Reply-To %q:\n%s", override, raw)
	}
	if strings.Contains(string(raw), "Reply-To: "+agent.EmailAddress()) {
		t.Errorf("accepted MIME fell back to agent-address Reply-To — override was dropped:\n%s", raw)
	}
}

// TestWorkerAutoApproveSelfSendDeliversViaLoopback: a held self-send
// whose TTL expires with the agent's hitl_expiration_action="approve"
// must be auto-approved via the loopback path — outbound.Sender.Send
// would strip the agent's own address (self-spam guard) and error
// "no valid recipients", which the worker would then translate into
// auto-REJECT, silently inverting the operator-configured policy.
//
// Asserts: outbound row → expired_approved + method=loopback, inbound
// row appears in the agent's mailbox, no SMTP traffic.
func TestWorkerAutoApproveSelfSendDeliversViaLoopback(t *testing.T) {
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()

	agent := prepareAgent(t, store, "auto-approve-self", identity.HITLExpirationApprove)
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{agent.EmailAddress()}, nil, nil,
		"self auto-approve", "note to self body", "", nil,
		"send", "", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)

	w.RunOnce(ctx)

	// SMTP must NOT be hit — loopback is the whole point.
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("SMTP should not be hit on self-send auto-approve, got %d messages: %+v", len(msgs), msgs)
	}

	// Outbound row → expired_approved, method=loopback, body scrubbed.
	var status, method string
	var providerID *string
	var bodyText *string
	err = pool.QueryRow(ctx,
		`SELECT status, method, provider_message_id, body_text FROM messages WHERE id = $1`,
		msg.ID,
	).Scan(&status, &method, &providerID, &bodyText)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusReviewExpiredApproved {
		t.Errorf("status = %q, want %q (worker should NOT have fallen back to expired_rejected)", status, identity.MessageStatusReviewExpiredApproved)
	}
	if method != "loopback" {
		t.Errorf("method = %q, want loopback", method)
	}
	if providerID == nil || *providerID == "" {
		t.Error("provider_message_id should be populated after loopback delivery")
	}
	if bodyText != nil {
		t.Errorf("body_text not scrubbed: %v", bodyText)
	}

	// Inbound row landed in the agent's mailbox.
	var inboundCount int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages
		   WHERE agent_id=$1 AND direction='inbound' AND subject='self auto-approve'`,
		agent.ID).Scan(&inboundCount)
	if inboundCount != 1 {
		t.Errorf("inbound rows after self-send auto-approve = %d, want 1", inboundCount)
	}
	var usageCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_events WHERE agent_id=$1 AND direction='outbound'`,
		agent.ID,
	).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if usageCount != 1 {
		t.Errorf("outbound usage events after self-send auto-approve = %d, want 1", usageCount)
	}
}

func TestWorkerAutoApproveSendFailureFallsBackToRejected(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	ctx := context.Background()

	agent := prepareAgent(t, store, "auto-approve-fail", identity.HITLExpirationApprove)
	msg, _ := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"not-an-email"}, nil, nil,
		"x", "body", "", nil, "send", "", "", "", 60)
	backdateExpiry(t, pool, msg.ID)

	w.RunOnce(ctx)

	var status string
	var reason, bodyText *string
	err := pool.QueryRow(ctx,
		`SELECT status, rejection_reason, body_text FROM messages WHERE id = $1`,
		msg.ID,
	).Scan(&status, &reason, &bodyText)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusReviewExpiredRejected {
		t.Errorf("status = %q, want %q (send failure should fall back to rejected)",
			status, identity.MessageStatusReviewExpiredRejected)
	}
	if reason == nil || !strings.Contains(*reason, "auto-approve failed: compose") {
		t.Errorf("reason = %v, want containing 'auto-approve failed: compose'", reason)
	}
	if bodyText != nil {
		t.Errorf("body_text not scrubbed: %v", bodyText)
	}
}

func TestWorkerAutoApproveUnverifiedAgentRejects(t *testing.T) {
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()

	// Build an agent with HITL=approve but on an unverified domain.
	user, _ := store.CreateOrGetUser(ctx, "owner-unv@example.com", "Owner", "google-worker-unv")
	store.ClaimOrCreateDomain(ctx, "unv.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@unv.example.com", "unv.example.com", "", "https://example.com/webhook", "", user.ID)
	if err := store.UpdateAgentHITL(ctx, a.ID, user.ID, identity.HITLDefaultTTLSeconds, identity.HITLExpirationApprove); err != nil {
		t.Fatal(err)
	}

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"x", "body", "", nil, "send", "", "", "", 60)
	backdateExpiry(t, pool, msg.ID)

	w.RunOnce(ctx)

	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("SMTP should not be hit for unverified agent, got %d", len(msgs))
	}
	var status string
	var reason *string
	pool.QueryRow(ctx,
		`SELECT status, rejection_reason FROM messages WHERE id = $1`, msg.ID,
	).Scan(&status, &reason)
	if status != identity.MessageStatusReviewExpiredRejected {
		t.Errorf("status = %q, want expired_rejected", status)
	}
	if reason == nil || !strings.Contains(*reason, "not verified") {
		t.Errorf("reason = %v, want containing 'not verified'", reason)
	}
}

func TestWorkerSkipsFreshPending(t *testing.T) {
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()

	agent := prepareAgent(t, store, "skip-fresh", identity.HITLExpirationReject)
	msg, _ := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil,
		"fresh", "b", "", nil, "send", "", "", "", 3600)

	w.RunOnce(ctx)

	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("unexpected SMTP messages: %d", len(msgs))
	}
	var status string
	var bodyText *string
	err := pool.QueryRow(ctx,
		`SELECT status, body_text FROM messages WHERE id = $1`, msg.ID,
	).Scan(&status, &bodyText)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusPendingReview {
		t.Errorf("status = %q, should still be pending_approval", status)
	}
	if bodyText == nil {
		t.Error("body_text should still be present on a fresh pending row")
	}
}

func TestWorkerRunOnceIsSafeWhenNoExpiredRows(t *testing.T) {
	w, _, _, smtpDone := setupWorker(t)
	w.RunOnce(context.Background())
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("expected 0 SMTP messages from an empty sweep, got %d", len(msgs))
	}
}

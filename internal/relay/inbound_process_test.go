package relay_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/relay"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/Mnexa-AI/e2a/internal/ws"
)

// TestInbound_ProcessIntake_RealPath exercises the ACTUAL async worker Processor
// (relay.Server.ProcessIntake → processInbound with the MarkInboundIntakeProcessedTx
// hook), not a fake: an accepted intake is processed into a messages row with the
// intake flipped to 'processed' atomically, and a re-drive is a no-op that creates no
// second message (the status-guard idempotency crux).
func TestInbound_ProcessIntake_RealPath(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const domain = "process-real.example.com"
	const agentEmail = "bot@" + domain
	user, err := store.CreateOrGetUser(ctx, "owner@"+domain, "O", "g-process-real")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET verified=true WHERE domain=$1`, domain); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("agent: %v", err)
	}

	cfg := &config.Config{SMTP: config.SMTPConfig{Domain: domain}, Env: "development"}
	signer := headers.NewSigner("test-relay-hmac-key-32-bytes-long!")
	server := relay.NewServer(cfg, store, signer, usage.NewNoopUsageTracker(), ws.NewHub())
	server.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))

	// Plant an accepted intake row (as acceptInbound would).
	raw := []byte("From: alice@sender.test\r\nTo: " + agentEmail + "\r\nMessage-ID: <rp1@sender.test>\r\nSubject: real path\r\n\r\nbody")
	id := identity.NewInboundIntakeID()
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, e := store.InsertInboundIntakeTx(ctx, tx, id, agentEmail, "alice@sender.test", "1.2.3.4", "<rp1@sender.test>", "hash-rp", raw)
		return e
	}); err != nil {
		t.Fatalf("plant intake: %v", err)
	}

	it, err := store.LoadInboundIntake(ctx, id)
	if err != nil || it == nil {
		t.Fatalf("load intake: %v", err)
	}
	if err := server.ProcessIntake(ctx, it); err != nil {
		t.Fatalf("ProcessIntake: %v", err)
	}

	// messages row created + intake flipped processed + linked, atomically.
	var subject, status string
	if err := pool.QueryRow(ctx, `SELECT subject, status FROM messages WHERE agent_id=$1 AND direction='inbound'`, agentEmail).Scan(&subject, &status); err != nil {
		t.Fatalf("messages row: %v", err)
	}
	if subject != "real path" {
		t.Errorf("subject = %q, want %q", subject, "real path")
	}
	var intakeStatus string
	var fk *string
	if err := pool.QueryRow(ctx, `SELECT status, message_fk FROM inbound_intake WHERE id=$1`, id).Scan(&intakeStatus, &fk); err != nil {
		t.Fatalf("read intake: %v", err)
	}
	if intakeStatus != identity.IntakeStatusProcessed || fk == nil {
		t.Fatalf("intake status=%q fk=%v; want processed + linked", intakeStatus, fk)
	}

	// Re-drive: ProcessIntake on the now-processed intake. The hook's status guard
	// (WHERE status='accepted' → 0 rows) aborts the persist tx with
	// ErrIntakeAlreadyProcessed, so NO second messages row is created.
	it2, _ := store.LoadInboundIntake(ctx, id)
	rerr := server.ProcessIntake(ctx, it2)
	if !errors.Is(rerr, identity.ErrIntakeAlreadyProcessed) {
		t.Fatalf("re-drive should return ErrIntakeAlreadyProcessed, got %v", rerr)
	}
	var msgCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE agent_id=$1`, agentEmail).Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgCount != 1 {
		t.Fatalf("re-drive must not create a second message, got %d", msgCount)
	}
}

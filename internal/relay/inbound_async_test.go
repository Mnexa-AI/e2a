package relay_test

import (
	"context"
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

// fakeInboundEnq stands in for the River enqueuer so the accept-tx can be exercised
// without a live River client — it just hands back an increasing job id.
type fakeInboundEnq struct{ calls int }

func (f *fakeInboundEnq) EnqueueInboundProcessTx(_ context.Context, _ pgx.Tx, _ string) (int64, error) {
	f.calls++
	return int64(f.calls), nil
}

// TestInbound_AsyncAcceptAndDedup exercises the queue-first accept-tx (slice 4):
// with E2A_INBOUND_MODE=async, a send lands ONE inbound_intake row (accepted, with a
// job id) and NO messages row (processing is deferred to the worker), and a resend of
// the same message dedups — no second intake row, no second enqueue.
func TestInbound_AsyncAcceptAndDedup(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const domain = "async-inbound.example.com"
	const agentEmail = "bot@" + domain
	user, err := store.CreateOrGetUser(ctx, "owner@"+domain, "O", "g-async-inbound")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET verified=true WHERE domain=$1`, domain); err != nil {
		t.Fatalf("verify domain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("agent: %v", err)
	}

	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	cfg := &config.Config{
		SMTP:    config.SMTPConfig{ListenAddr: "127.0.0.1:" + port, Domain: domain},
		Inbound: config.InboundConfig{Mode: "async"},
		Env:     "development",
	}
	signer := headers.NewSigner("test-relay-hmac-key-32-bytes-long!")
	server := relay.NewServer(cfg, store, signer, usage.NewNoopUsageTracker(), ws.NewHub())
	server.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	enq := &fakeInboundEnq{}
	server.SetInboundEnqueuer(enq)
	go func() { _ = server.ListenAndServe() }()
	t.Cleanup(func() { _ = server.Close() })
	waitForSMTP(t, cfg.SMTP.ListenAddr)

	body := "From: sender@ext.test\r\nTo: " + agentEmail + "\r\nMessage-ID: <dedup-1@ext.test>\r\nSubject: hi\r\n\r\nhello"
	sendSMTP(t, cfg.SMTP.ListenAddr, "sender@ext.test", agentEmail, body)

	// One accepted intake row with a stamped job id.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbound_intake WHERE recipient=$1`, agentEmail).Scan(&n); err != nil {
		t.Fatalf("count intake: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 intake row after async accept, got %d", n)
	}
	var status string
	var jobID int64
	if err := pool.QueryRow(ctx, `SELECT status, process_job_id FROM inbound_intake WHERE recipient=$1`, agentEmail).Scan(&status, &jobID); err != nil {
		t.Fatalf("read intake: %v", err)
	}
	if status != identity.IntakeStatusAccepted || jobID == 0 {
		t.Fatalf("intake status=%q job=%d; want accepted + a stamped job", status, jobID)
	}
	// Processing is deferred — no messages row yet.
	var msgs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE agent_id=$1`, agentEmail).Scan(&msgs); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgs != 0 {
		t.Fatalf("no messages row should exist yet (async defers processing), got %d", msgs)
	}
	if enq.calls != 1 {
		t.Fatalf("enqueuer should fire once, got %d", enq.calls)
	}

	// Resend the SAME message (lost-ack MTA retry) → dedup: still one intake, no
	// second enqueue.
	sendSMTP(t, cfg.SMTP.ListenAddr, "sender@ext.test", agentEmail, body)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbound_intake WHERE recipient=$1`, agentEmail).Scan(&n); err != nil {
		t.Fatalf("recount intake: %v", err)
	}
	if n != 1 {
		t.Fatalf("dedup: want 1 intake row after resend, got %d", n)
	}
	if enq.calls != 1 {
		t.Fatalf("dedup: the enqueuer must not fire on the duplicate, calls=%d", enq.calls)
	}
}

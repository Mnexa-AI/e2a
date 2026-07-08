package relay_test

import (
	"context"
	"net/smtp"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/relay"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/Mnexa-AI/e2a/internal/ws"
)

// TestInbound_PersistFailure_Returns451 is the regression for the silent-loss fix
// (slice 1): when the inbound messages insert fails, the SMTP session must return a
// transient 451 so the sending MTA retries — NOT a 250 that silently drops the mail.
// Before the fix, deliverToAgent swallowed the persist error and deliverMessages
// returned nil → go-smtp wrote 250 for a message that was never stored.
//
// We force the insert to fail with a trigger scoped to THIS test's agent id (so it
// can't affect any other row) and dropped before the pool closes. Safe on the shared
// test DB because `make test` runs packages with -p 1 and relay tests don't
// t.Parallel(), so nothing else inserts into messages during the window.
func TestInbound_PersistFailure_Returns451(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const domain = "persistfail.example.com"
	const agentEmail = "bot@" + domain
	user, err := store.CreateOrGetUser(ctx, "owner@"+domain, "O", "g-persistfail")
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

	// Force the inbound insert for THIS agent to fail. Scoped by agent_id so other
	// rows pass through (RETURN NEW); dropped before TruncateAll/pool.Close (t.Cleanup
	// is LIFO, and TestDB registered its cleanup first).
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION test_fail_persist_slice1() RETURNS trigger AS $f$
		BEGIN
			IF NEW.agent_id = '`+agentEmail+`' THEN
				RAISE EXCEPTION 'forced persist failure (slice-1 regression test)';
			END IF;
			RETURN NEW;
		END; $f$ LANGUAGE plpgsql;`); err != nil {
		t.Fatalf("create trigger fn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER IF EXISTS test_fail_persist_slice1 ON messages;
		CREATE TRIGGER test_fail_persist_slice1 BEFORE INSERT ON messages
			FOR EACH ROW EXECUTE FUNCTION test_fail_persist_slice1();`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DROP TRIGGER IF EXISTS test_fail_persist_slice1 ON messages; DROP FUNCTION IF EXISTS test_fail_persist_slice1();`)
	})

	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	cfg := &config.Config{
		SMTP: config.SMTPConfig{ListenAddr: "127.0.0.1:" + port, Domain: domain},
		Env:  "development",
	}
	signer := headers.NewSigner("test-relay-hmac-key-32-bytes-long!")
	server := relay.NewServer(cfg, store, signer, usage.NewNoopUsageTracker(), ws.NewHub())
	server.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	go func() { _ = server.ListenAndServe() }()
	t.Cleanup(func() { _ = server.Close() })
	waitForSMTP(t, cfg.SMTP.ListenAddr)

	body := "From: sender@ext.test\r\nTo: " + agentEmail + "\r\nSubject: hi\r\n\r\nhello"
	derr := sendSMTPCaptureErr(cfg.SMTP.ListenAddr, "sender@ext.test", agentEmail, body)

	// The core assertion: the persist failure surfaces as a transient 451, not a 250.
	if derr == nil {
		t.Fatal("DATA returned success (250) despite a persist failure — silent loss regression")
	}
	if !strings.Contains(derr.Error(), "451") {
		t.Fatalf("expected a 451 transient error, got: %v", derr)
	}
	// And nothing was persisted for the agent.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE agent_id=$1`, agentEmail).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 persisted messages after a failed insert, got %d", n)
	}
}

// sendSMTPCaptureErr runs a full SMTP send and RETURNS the DATA-completion error
// (surfaced at the final "." on w.Close()) instead of failing the test — so a caller
// can assert the server's transient/permanent response code.
func sendSMTPCaptureErr(addr, from, to, body string) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return err
	}
	// The server's Data handler result (250 / 451) surfaces here, at the final ".".
	return w.Close()
}

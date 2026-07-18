package relay_test

import (
	"context"
	"net"
	"net/smtp"
	"testing"
	"time"

	"github.com/tokencanopy/e2a/internal/config"
	"github.com/tokencanopy/e2a/internal/headers"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/relay"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/ws"
)

// TestRelay_RcptTo_SubdomainAgentUnderVerifiedParent proves the slice-2 inbound
// contract end-to-end over a real SMTP socket:
//
//	(a) mail to a subdomain agent (otto@acme.team.mnexa.ai) whose PARENT domain
//	    (team.mnexa.ai) is verified is ACCEPTED and resolves to that agent —
//	    the agent stores the parent in agent_identities.registered_domain, so the RCPT
//	    gate's DomainVerified read (which joins to the parent's domains row) is
//	    true, and resolveAgent matches on the full subdomain address (the PK).
//	(b) mail to a subdomain with no matching agent is cleanly REJECTED as
//	    unknown-recipient (SMTP 550) — the broadened acceptance does not turn
//	    into a catch-all for the parent's whole subtree.
//	(e) mail to an exact-domain agent still resolves — no regression.
func TestRelay_RcptTo_SubdomainAgentUnderVerifiedParent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SMTP integration test under -short")
	}
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)

	user, _ := store.CreateOrGetUser(ctx, "owner@mnexa.ai", "Owner", "google-sub-inbound")
	// Verify the PARENT only — no separate subdomain registration.
	if _, err := store.ClaimOrCreateDomain(ctx, "team.mnexa.ai", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain parent: %v", err)
	}
	if err := store.VerifyDomain(ctx, "team.mnexa.ai", user.ID); err != nil {
		t.Fatalf("VerifyDomain parent: %v", err)
	}
	// Subdomain agent — bound to the parent domain (mirrors the create handler's
	// covering-parent resolution), full subdomain kept as the address/identity.
	subdomainAgent := "otto@acme.team.mnexa.ai"
	if _, err := store.CreateAgent(ctx, subdomainAgent, "team.mnexa.ai", "", "", "", user.ID); err != nil {
		t.Fatalf("CreateAgent subdomain: %v", err)
	}
	// An exact-domain agent for the no-regression check.
	if _, err := store.ClaimOrCreateDomain(ctx, "exact.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain exact: %v", err)
	}
	if err := store.VerifyDomain(ctx, "exact.example.com", user.ID); err != nil {
		t.Fatalf("VerifyDomain exact: %v", err)
	}
	exactAgent := "bot@exact.example.com"
	if _, err := store.CreateAgent(ctx, exactAgent, "exact.example.com", "", "", "", user.ID); err != nil {
		t.Fatalf("CreateAgent exact: %v", err)
	}

	signer := headers.NewSigner("test-relay-hmac-key-32-bytes-long!")
	port, _ := freePort()
	cfg := &config.Config{SMTP: config.SMTPConfig{ListenAddr: "127.0.0.1:" + port, Domain: "test.relay"}, Env: "development"}
	server := relay.NewServer(cfg, store, signer, usage.NewNoopUsageTracker(), ws.NewHub())
	go func() { _ = server.ListenAndServe() }()
	t.Cleanup(func() { _ = server.Close() })

	// Wait-for-ready.
	deadline := time.Now().Add(2 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		var conn net.Conn
		if conn, err = net.Dial("tcp", cfg.SMTP.ListenAddr); err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("server not ready: %v", err)
	}

	// rcpt dials a fresh connection, runs MAIL+RCPT, and returns the RCPT error
	// (nil = accepted). A fresh connection per check keeps a rejected RCPT from
	// polluting the next assertion's session state.
	rcpt := func(addr string) error {
		c, derr := smtp.Dial(cfg.SMTP.ListenAddr)
		if derr != nil {
			t.Fatalf("Dial: %v", derr)
		}
		defer c.Close()
		if merr := c.Mail("sender@elsewhere.test"); merr != nil {
			t.Fatalf("MAIL FROM: %v", merr)
		}
		return c.Rcpt(addr)
	}

	// (a) subdomain agent under a verified parent — accepted.
	if err := rcpt(subdomainAgent); err != nil {
		t.Errorf("RCPT to subdomain agent %s returned %v; want nil (accepted)", subdomainAgent, err)
	}
	// (b) subdomain with no agent — rejected as unknown recipient (550).
	if err := rcpt("ghost@nope.team.mnexa.ai"); err == nil {
		t.Errorf("RCPT to unknown subdomain recipient was accepted; want rejection (550)")
	}
	// (e) exact-domain agent — still accepted (no regression).
	if err := rcpt(exactAgent); err != nil {
		t.Errorf("RCPT to exact-domain agent %s returned %v; want nil (accepted)", exactAgent, err)
	}
}

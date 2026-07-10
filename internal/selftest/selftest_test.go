package selftest_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/selftest"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestSelftestAll_AgainstRealServer boots a real in-process server (HTTP + SMTP
// + subscriber worker) and runs the full battery against it through a synthetic
// system-class probe agent — the same way the shipped prober runs against prod.
// A background ticker stands in for the always-on production outbox drain +
// River delivery workers.
func TestSelftestAll_AgainstRealServer(t *testing.T) {
	pool := testutil.TestDB(t)
	// In-process fake SMTP so the outbound_send scenario has a reachable relay
	// without depending on an external Mailpit — the unit/coverage jobs run this
	// test (no `integration` tag) but don't start Mailpit. The fake accepts the
	// message and returns a Message-ID, so the real outbound path + email.sent fire.
	fakeSMTP, _ := testutil.FakeSMTPServer(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP(fakeSMTP.Host, fakeSMTP.Port, "test.e2a.dev"))
	ctx := context.Background()

	// --- seed a system-class probe account + agent ---
	user, err := ts.Store.CreateOrGetUser(ctx, "probe@e2a-selftest.test", "Probe", "google-probe-selftest")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET account_class = 'system' WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("set system class: %v", err)
	}
	key, err := ts.Store.CreateAPIKey(ctx, user.ID, "probe-key", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	domain := "probe.e2a-selftest.test"
	if _, err := ts.Store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := ts.Store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	agentEmail := "agent@" + domain
	agent, err := ts.Store.CreateAgent(ctx, agentEmail, domain, "Probe Agent", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// --- sink + webhook pointing at it ---
	sink := selftest.NewHTTPSink()
	sinkSrv := httptest.NewServer(sink)
	defer sinkSrv.Close()
	// Subscribe to both event types the battery asserts on — email.received for
	// the inbound round-trip and email.sent for the outbound-send scenario —
	// mirroring what cmd/e2a-prober seed provisions.
	wh, err := ts.Store.CreateWebhook(ctx, user.ID, sinkSrv.URL+"/sink", "selftest",
		[]string{"email.received", "email.sent"}, identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}

	// --- stand in for the always-on outbox drain + River delivery workers ---
	tickCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		tk := time.NewTicker(50 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-tickCtx.Done():
				return
			case <-tk.C:
				ts.DrainAndDeliver(tickCtx)
			}
		}
	}()

	probe := &selftest.Probe{
		HTTPBaseURL:   ts.HTTPServer.URL,
		APIKey:        key.PlaintextKey,
		AgentEmail:    agent.EmailAddress(),
		SMTPAddr:      ts.SMTPAddr,
		WebhookSecret: wh.SigningSecret,
		Sink:          sink,
	}

	results := selftest.Run(ctx, probe, selftest.All, true /* smokeOnly */)
	if len(results) != len(selftest.All) {
		t.Fatalf("ran %d scenarios, want %d", len(results), len(selftest.All))
	}
	for _, r := range results {
		if r.Status != selftest.StatusPass {
			t.Errorf("scenario %q = %s: %s", r.Name, r.Status, r.Detail)
		}
	}
	if w := selftest.Worst(results); w != selftest.StatusPass {
		t.Errorf("Worst() = %s, want pass", w)
	}

	// The probe ran under a system-class account: no usage must have been
	// recorded despite the inbound round-trip + self-send.
	var events int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM usage_events WHERE user_id = $1`, user.ID).Scan(&events); err != nil {
		t.Fatalf("count usage_events: %v", err)
	}
	if events != 0 {
		t.Errorf("system probe account wrote %d usage_events, want 0", events)
	}

	// agent_lifecycle must be self-cleaning: after the battery only the original
	// probe agent remains — the ephemeral create→delete left no orphan.
	var agents int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_identities WHERE user_id = $1`, user.ID).Scan(&agents); err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if agents != 1 {
		t.Errorf("probe account has %d agents after battery, want 1 (agent_lifecycle left an orphan)", agents)
	}
}

// TestVerifyHMAC_RejectsBadSignature is a unit guard on the HMAC check used by
// the round-trip scenario.
func TestVerifyHMAC_unit(t *testing.T) {
	// A known-good signature is exercised end-to-end in the server test above;
	// here we assert the negative paths the round-trip relies on.
	body := []byte(`{"type":"email.received"}`)
	cases := []struct {
		name, header, secret string
	}{
		{"empty header", "", "sek"},
		{"empty secret", "t=1,v1=deadbeef", ""},
		{"no v1", "t=1", "sek"},
		{"no t", "v1=deadbeef", "sek"},
		{"bad hex sig", "t=1,v1=notamatch", "sek"},
	}
	for _, c := range cases {
		if selftest.VerifyHMACForTest(c.header, body, c.secret) {
			t.Errorf("%s: verifyHMAC returned true, want false", c.name)
		}
	}
}

// TestVerifyHMAC_positive covers the happy path and the rotation-grace branch
// (two v1= clauses, only the second matching) without needing a DB — the
// deliverer emits two signatures during the 24h secret-rotation window.
func TestVerifyHMAC_positive(t *testing.T) {
	const secret = "whsec_realone"
	body := []byte(`{"type":"email.received","subject":"e2a-selftest abc"}`)
	ts := int64(1_700_000_123)
	sign := func(sec string) string {
		mac := hmac.New(sha256.New, []byte(sec))
		fmt.Fprintf(mac, "%d.", ts)
		mac.Write(body)
		return hex.EncodeToString(mac.Sum(nil))
	}

	// Single valid signature.
	hdr := fmt.Sprintf("t=%d,v1=%s", ts, sign(secret))
	if !selftest.VerifyHMACForTest(hdr, body, secret) {
		t.Error("valid single signature should verify")
	}

	// Rotation grace: old (non-matching) + new (matching) — must accept.
	hdr2 := fmt.Sprintf("t=%d,v1=%s,v1=%s", ts, sign("whsec_oldsecret"), sign(secret))
	if !selftest.VerifyHMACForTest(hdr2, body, secret) {
		t.Error("rotation-grace second signature should verify")
	}

	// A tampered body must NOT verify against the original signature.
	if selftest.VerifyHMACForTest(hdr, []byte("tampered"), secret) {
		t.Error("tampered body must not verify")
	}
}

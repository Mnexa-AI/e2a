package selftest_test

import (
	"context"
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
// A background ticker stands in for the always-on production SubscriberRetryWorker.
func TestSelftestAll_AgainstRealServer(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
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
	wh, err := ts.Store.CreateWebhook(ctx, user.ID, sinkSrv.URL+"/sink", "selftest",
		[]string{"email.received"}, identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}

	// --- stand in for the always-on subscriber worker ---
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
				ts.SubscriberWorker.Tick(tickCtx)
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

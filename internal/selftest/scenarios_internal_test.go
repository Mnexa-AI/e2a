package selftest

// Internal failure-path tests: assert each scenario reports StatusFail when the
// thing it checks is broken. This is the monitor's whole job — a scenario that
// only ever returns pass on the happy path could mask a real outage. Driven by
// httptest mocks (no DB) so each failure mode is isolated.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func failProbe(baseURL, smtpAddr string, sink *HTTPSink) *Probe {
	return &Probe{
		HTTPBaseURL:   baseURL,
		APIKey:        "k",
		AgentEmail:    "agent@probe.test",
		SMTPAddr:      smtpAddr,
		WebhookSecret: "whsec_test",
		Sink:          sink,
		Timeout:       200 * time.Millisecond,
	}
}

func mustFail(t *testing.T, name string, r Result) {
	t.Helper()
	if r.Status != StatusFail {
		t.Errorf("%s: status = %s (%q), want fail", name, r.Status, r.Detail)
	}
}

func TestScenarioLiveness_Fail(t *testing.T) {
	// Non-200 health.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	mustFail(t, "health 500", scenarioLiveness(context.Background(), failProbe(srv.URL, "", nil)))

	// 200 but wrong body.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"degraded"}`))
	}))
	defer srv2.Close()
	mustFail(t, "health wrong body", scenarioLiveness(context.Background(), failProbe(srv2.URL, "", nil)))

	// Unreachable server.
	mustFail(t, "health unreachable", scenarioLiveness(context.Background(), failProbe("http://127.0.0.1:1", "", nil)))
}

func TestScenarioAuthRead_Fail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	mustFail(t, "auth 401", scenarioAuthRead(context.Background(), failProbe(srv.URL, "", nil)))
}

func TestScenarioSelfSendLoopback_Fail(t *testing.T) {
	// 200 but method != loopback → a real send would have egressed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"method":"smtp"}`))
	}))
	defer srv.Close()
	mustFail(t, "self-send smtp not loopback", scenarioSelfSendLoopback(context.Background(), failProbe(srv.URL, "", nil)))
}

func TestScenarioAgentLifecycle_Fail(t *testing.T) {
	// Create returns 500 → scenario fails before any cleanup is registered.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	mustFail(t, "create 500", scenarioAgentLifecycle(context.Background(), failProbe(srv.URL, "", nil)))
}

func TestScenarioInboundRoundTrip_Fail(t *testing.T) {
	// SMTP listener unreachable → send fails.
	mustFail(t, "smtp unreachable",
		scenarioInboundRoundTrip(context.Background(), failProbe("http://127.0.0.1:1", "127.0.0.1:1", NewHTTPSink())))

	// No sink configured → fail fast.
	mustFail(t, "no sink",
		scenarioInboundRoundTrip(context.Background(), failProbe("http://127.0.0.1:1", "127.0.0.1:1", nil)))
}

func TestSinkAwait_Timeout(t *testing.T) {
	// The round-trip relies on Await timing out (StatusFail) when no webhook
	// arrives. Assert that mechanism directly.
	sink := NewHTTPSink()
	_, err := sink.Await(context.Background(), func(Delivery) bool { return true }, 50*time.Millisecond)
	if err == nil {
		t.Fatal("Await returned nil error with no delivery, want timeout")
	}
}

func TestRunWorst_WithFailure(t *testing.T) {
	// Run aggregates a failing scenario; Worst reports fail.
	scenarios := []Scenario{
		{Name: "ok", SmokeSafe: true, Run: func(context.Context, *Probe) Result { return pass("ok") }},
		{Name: "bad", SmokeSafe: true, Run: func(context.Context, *Probe) Result { return fail("boom") }},
		{Name: "unsafe", SmokeSafe: false, Run: func(context.Context, *Probe) Result { return pass("skipme") }},
	}
	results := Run(context.Background(), failProbe("http://127.0.0.1:1", "", nil), scenarios, true /* smokeOnly */)
	if len(results) != 2 {
		t.Fatalf("ran %d scenarios, want 2 (unsafe one skipped under smokeOnly)", len(results))
	}
	if Worst(results) != StatusFail {
		t.Errorf("Worst = %s, want fail", Worst(results))
	}
	// Worst of empty is fail ("no checks ran" is not healthy).
	if Worst(nil) != StatusFail {
		t.Errorf("Worst(nil) = %s, want fail", Worst(nil))
	}
}

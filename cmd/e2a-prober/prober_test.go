package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/selftest"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func TestSeedProbe_ProvisionsSystemAccountIdempotently(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const agentEmail = "agent@probe.prober-test"
	const sinkURL = "http://prober:8090/sink"

	res, err := seedProbe(ctx, store, agentEmail, sinkURL)
	if err != nil {
		t.Fatalf("seedProbe: %v", err)
	}
	if res.APIKey == "" {
		t.Error("expected an API key on first seed")
	}
	if res.WebhookSecret == "" {
		t.Error("expected a webhook secret on first seed")
	}

	agent, err := store.GetAgentByID(ctx, agentEmail)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	var class string
	if err := pool.QueryRow(ctx, `SELECT account_class FROM users WHERE id = $1`, agent.UserID).Scan(&class); err != nil {
		t.Fatalf("read account_class: %v", err)
	}
	if class != "system" {
		t.Errorf("account_class = %q, want system", class)
	}

	// Re-run: idempotent. No duplicate agent; webhook reused (secret empty).
	res2, err := seedProbe(ctx, store, agentEmail, sinkURL)
	if err != nil {
		t.Fatalf("seedProbe re-run: %v", err)
	}
	if res2.WebhookSecret != "" {
		t.Error("re-seed should reuse the existing webhook (empty secret)")
	}
	if res2.APIKey != "" {
		t.Error("re-seed should not mint a new API key when one exists (empty key)")
	}
	whs, err := store.ListWebhooksByUser(ctx, agent.UserID)
	if err != nil {
		t.Fatalf("ListWebhooksByUser: %v", err)
	}
	count := 0
	for _, wh := range whs {
		if wh.URL == sinkURL {
			count++
		}
	}
	if count != 1 {
		t.Errorf("webhooks targeting sink = %d, want 1 (no duplicate on re-seed)", count)
	}
	var agents int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM agent_identities WHERE user_id = $1`, agent.UserID).Scan(&agents); err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if agents != 1 {
		t.Errorf("agent count = %d, want 1 (no duplicate on re-seed)", agents)
	}
}

func TestHandleStatus_ConsecutiveGreen(t *testing.T) {
	p := newProber(config{})
	base := time.Unix(1_700_000_000, 0)
	// oldest → newest: green, red, green, green  → tail consecutive_green = 2
	p.ring = []run{
		{At: base, OK: true},
		{At: base.Add(1 * time.Second), OK: false},
		{At: base.Add(2 * time.Second), OK: true},
		{At: base.Add(3 * time.Second), OK: true},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status?since=0", nil)
	p.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d", rec.Code)
	}
	var out struct {
		ConsecutiveGreen int  `json:"consecutive_green"`
		Healthy          bool `json:"healthy"`
		Runs             []struct {
			OK bool `json:"ok"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ConsecutiveGreen != 2 {
		t.Errorf("consecutive_green = %d, want 2", out.ConsecutiveGreen)
	}
	if !out.Healthy {
		t.Error("healthy = false, want true (last run green)")
	}
	if len(out.Runs) != 4 {
		t.Errorf("runs = %d, want 4", len(out.Runs))
	}

	// since filter excludes earlier runs.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/status?since="+itoa(base.Add(2*time.Second).Unix()-1), nil)
	p.handleStatus(rec2, req2)
	var out2 struct {
		Runs []struct{} `json:"runs"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &out2)
	if len(out2.Runs) != 2 {
		t.Errorf("since-filtered runs = %d, want 2", len(out2.Runs))
	}
}

func TestHandleMetrics(t *testing.T) {
	p := newProber(config{})
	p.ring = []run{{
		At: time.Unix(1_700_000_000, 0),
		OK: true,
		Results: []selftest.Result{
			{Name: "liveness", Status: selftest.StatusPass, DurationMS: 10},
			{Name: "inbound_round_trip", Status: selftest.StatusPass, DurationMS: 200},
		},
	}}
	rec := httptest.NewRecorder()
	p.handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"e2a_selftest_success 1",
		`e2a_selftest_scenario_success{scenario="liveness"} 1`,
		`e2a_selftest_scenario_success{scenario="inbound_round_trip"} 1`,
		"e2a_selftest_duration_seconds 0.210",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n---\n%s", want, body)
		}
	}
}

func TestHandleMetrics_NoRuns(t *testing.T) {
	p := newProber(config{})
	rec := httptest.NewRecorder()
	p.handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "e2a_selftest_success 0") {
		t.Errorf("expected success 0 with no runs, got:\n%s", rec.Body.String())
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

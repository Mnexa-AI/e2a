package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/limits"
)

// These tests verify the END-TO-END wiring between the enforcer and
// the HTTP handlers: that a user over cap actually receives HTTP 402
// from POST /api/v1/agents and POST /api/v1/domains, with the
// LimitErrorBody shape callers can read. The enforcer's Check methods
// have unit tests via fakes; this layer proves the handler chain
// (auth → check → 402) is wired correctly so a regression in a
// SetEnforcer caller or a re-ordering of guards in the handler is
// caught.
//
// They live in package agent_test (external) because they exercise
// the real HTTP surface.

// limitsForBlock returns a Limits where max_agents = 0 — blocks all
// agent creates regardless of the user's current count. Everything
// else is permissive so the only thing that can fire is the agent cap.
func limitsForBlock(planCode, upgradeURL string) limits.Limits {
	return limits.Limits{
		PlanCode:         planCode,
		MaxAgents:        0,
		MaxDomains:       1000,
		MaxMessagesMonth: 1_000_000,
		MaxStorageBytes:  1 << 40,
		UpgradeURL:       upgradeURL,
	}
}

func TestRegisterAgent_Returns402WhenAgentCapHit(t *testing.T) {
	server, store, pool, enf := setupAPIWithLimits(t, "")
	token := createTestUser(t, store, "enf-agent-blocked@test.com")

	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "enf-agent-blocked@test.com", "Test User", "google-enf-agent-blocked@test.com")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// Block: max_agents=0 means EVERY create attempt is over cap.
	lstore := limits.NewStore(pool)
	if err := lstore.Upsert(ctx, user.ID, limitsForBlock("free_test", "https://billing.example/upgrade")); err != nil {
		t.Fatalf("Upsert limits: %v", err)
	}
	enf.Invalidate(user.ID)

	// Slug-based registration so we don't need to set up a custom
	// domain. agents.e2a.dev is the shared domain seeded by testutil.
	body := `{"slug":"capblock","agent_mode":"local"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 402; body = %s", resp.StatusCode, string(buf))
	}

	// LimitErrorBody shape — dashboards + SDK clients depend on this
	// being stable. Tightening this assertion catches accidental field
	// renames.
	var lerr limits.LimitErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&lerr); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if lerr.Resource != "agents" {
		t.Errorf("Resource = %q, want agents", lerr.Resource)
	}
	if lerr.Limit != 0 {
		t.Errorf("Limit = %d, want 0", lerr.Limit)
	}
	if lerr.PlanCode != "free_test" {
		t.Errorf("PlanCode = %q, want free_test", lerr.PlanCode)
	}
	if lerr.UpgradeURL != "https://billing.example/upgrade" {
		t.Errorf("UpgradeURL = %q, want https://billing.example/upgrade", lerr.UpgradeURL)
	}
	if lerr.Error == "" {
		t.Errorf("Error message empty; want human-readable string for logs")
	}
}

func TestRegisterAgent_201WhenUnderCap(t *testing.T) {
	server, store, pool, enf := setupAPIWithLimits(t, "")
	token := createTestUser(t, store, "enf-agent-ok@test.com")

	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "enf-agent-ok@test.com", "Test User", "google-enf-agent-ok@test.com")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// Generous caps — happy path baseline. Confirms the enforcer
	// wiring doesn't accidentally block when it shouldn't.
	lstore := limits.NewStore(pool)
	if err := lstore.Upsert(ctx, user.ID, limits.Limits{
		PlanCode:         "pro_test",
		MaxAgents:        25,
		MaxDomains:       10,
		MaxMessagesMonth: 50_000,
		MaxStorageBytes:  10 << 30,
	}); err != nil {
		t.Fatalf("Upsert limits: %v", err)
	}
	enf.Invalidate(user.ID)

	body := `{"slug":"undercap","agent_mode":"local"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body = %s", resp.StatusCode, string(buf))
	}
}

func TestRegisterDomain_Returns402WhenDomainCapHit(t *testing.T) {
	server, store, pool, enf := setupAPIWithLimits(t, "")
	token := createTestUser(t, store, "enf-domain-blocked@test.com")

	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "enf-domain-blocked@test.com", "Test User", "google-enf-domain-blocked@test.com")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// max_domains=0 — every register attempt over cap.
	lstore := limits.NewStore(pool)
	if err := lstore.Upsert(ctx, user.ID, limits.Limits{
		PlanCode:         "free_test",
		MaxAgents:        1000,
		MaxDomains:       0,
		MaxMessagesMonth: 1_000_000,
		MaxStorageBytes:  1 << 40,
		UpgradeURL:       "https://billing.example/upgrade",
	}); err != nil {
		t.Fatalf("Upsert limits: %v", err)
	}
	enf.Invalidate(user.ID)

	body := `{"domain":"capblock.example.com"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/domains", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 402; body = %s", resp.StatusCode, string(buf))
	}

	var lerr limits.LimitErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&lerr); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if lerr.Resource != "domains" {
		t.Errorf("Resource = %q, want domains", lerr.Resource)
	}
}

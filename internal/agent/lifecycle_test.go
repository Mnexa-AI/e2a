package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/limits"
)

// TestUserLifecycle_DefaultThenProThenFree walks a single user through
// the three states a paid-SaaS user can be in:
//
//  1. Brand new — no row in account_limits → falls through to the
//     operator default (configured in config.yaml's `limits:` block).
//  2. Upgraded — sidecar wrote a Pro row → dashboard shows Pro caps.
//  3. Downgraded — sidecar wrote a Free row after Stripe cancellation
//     → dashboard shows Free caps with no upgrade_url (so it renders
//     "Upgrade" instead of "Manage billing").
//
// This is the test that proves the three slices (enforcer fallback,
// account_limits upsert, GET /me/limits read + cache invalidation)
// compose correctly. Failing here means at least one seam drifted.
func TestUserLifecycle_DefaultThenProThenFree(t *testing.T) {
	server, store, pool, enf := setupAPIWithLimits(t, "")
	token := createTestUser(t, store, "lifecycle@test.com")
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "lifecycle@test.com", "Test User", "google-lifecycle@test.com")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// --- State 1: brand new user, no account_limits row ---
	//
	// setupAPIWithLimits configured operator defaults of
	// 100/10/1_000_000/1<<40. That's what the dashboard should see.
	info := fetchLimits(t, server.URL, token)
	if info.PlanCode != "default" {
		t.Errorf("state=new: PlanCode = %q, want 'default'", info.PlanCode)
	}
	if info.Limits.MaxAgents != 100 {
		t.Errorf("state=new: MaxAgents = %d, want 100 (operator default)", info.Limits.MaxAgents)
	}
	if info.UpgradeURL != "" {
		t.Errorf("state=new: UpgradeURL = %q, want empty (no sub yet)", info.UpgradeURL)
	}

	// --- State 2: upgrade — sidecar wrote a Pro row ---
	//
	// We're simulating what the sidecar's accountlimits.Writer does
	// after Stripe's customer.subscription.created event lands. From
	// the OSS server's perspective the only thing that matters is
	// the row in account_limits + the cache invalidate; bypassing
	// the HTTP call between sidecar and OSS is fine because the
	// invalidate endpoint is unit-tested separately.
	lstore := limits.NewStore(pool)
	proRow := limits.Limits{
		PlanCode:         "pro",
		MaxAgents:        25,
		MaxDomains:       10,
		MaxMessagesMonth: 50_000,
		MaxStorageBytes:  10 << 30,
		UpgradeURL:       "https://billing.example/portal",
	}
	if err := lstore.Upsert(ctx, user.ID, proRow); err != nil {
		t.Fatalf("Upsert pro: %v", err)
	}
	enf.Invalidate(user.ID) // mimic the sidecar's invalidate ping

	info = fetchLimits(t, server.URL, token)
	if info.PlanCode != "pro" {
		t.Errorf("state=pro: PlanCode = %q, want 'pro'", info.PlanCode)
	}
	if info.Limits != (agent.LimitsCaps{MaxAgents: 25, MaxDomains: 10, MaxMessagesMonth: 50_000, MaxStorageBytes: 10 << 30}) {
		t.Errorf("state=pro: caps = %+v, want Pro shape", info.Limits)
	}
	if info.UpgradeURL != "https://billing.example/portal" {
		t.Errorf("state=pro: UpgradeURL = %q, want billing portal URL (Manage Billing button renders)", info.UpgradeURL)
	}

	// --- State 3: downgrade — sidecar wrote a Free row after cancel ---
	//
	// Stripe's customer.subscription.deleted event landed; the
	// sidecar wrote Free caps + cleared upgrade_url so the dashboard
	// renders the "Upgrade" CTA again (not "Manage Billing", which
	// would be confusing on a free account).
	freeRow := limits.Limits{
		PlanCode:         "free",
		MaxAgents:        3,
		MaxDomains:       1,
		MaxMessagesMonth: 3_000,
		MaxStorageBytes:  1 << 30,
		UpgradeURL:       "",
	}
	if err := lstore.Upsert(ctx, user.ID, freeRow); err != nil {
		t.Fatalf("Upsert free: %v", err)
	}
	enf.Invalidate(user.ID)

	info = fetchLimits(t, server.URL, token)
	if info.PlanCode != "free" {
		t.Errorf("state=free: PlanCode = %q, want 'free'", info.PlanCode)
	}
	if info.Limits != (agent.LimitsCaps{MaxAgents: 3, MaxDomains: 1, MaxMessagesMonth: 3_000, MaxStorageBytes: 1 << 30}) {
		t.Errorf("state=free: caps = %+v, want Free shape", info.Limits)
	}
	if info.UpgradeURL != "" {
		t.Errorf("state=free: UpgradeURL = %q, want empty (Upgrade button renders)", info.UpgradeURL)
	}

	// Sanity: re-fetch after a no-op invalidate should still show
	// Free. Catches a cache-coherence bug where a stale Pro entry
	// could survive into post-downgrade reads.
	enf.Invalidate(user.ID)
	info = fetchLimits(t, server.URL, token)
	if info.PlanCode != "free" {
		t.Errorf("state=free (re-read): PlanCode = %q, want 'free'", info.PlanCode)
	}
}

// fetchLimits hits GET /api/v1/users/me/limits with the given token and
// returns the parsed LimitsInfo. Centralized helper so the lifecycle
// test reads as a sequence of state transitions rather than HTTP boilerplate.
func fetchLimits(t *testing.T, baseURL, token string) agent.LimitsInfo {
	t.Helper()
	req, _ := http.NewRequest("GET", baseURL+"/api/v1/users/me/limits", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /me/limits status=%d body=%s", resp.StatusCode, string(body))
	}
	var info agent.LimitsInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return info
}

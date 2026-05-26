package agent_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
)

// setupAPIWithLimits returns a live test server plus the underlying
// pool/store/enforcer so callers can write directly to the DB the API
// is reading from. testutil.TestDB(t) creates a fresh pool AND
// truncates the schema on every call — so callers must NOT call it
// again from inside the test or they'll wipe their own setup.
func setupAPIWithLimits(t *testing.T, internalSecret string) (*httptest.Server, *identity.Store, *pgxpool.Pool, limits.Enforcer) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	userAuth := auth.NewUserAuth(&config.OAuthConfig{}, store, false)
	usageStore := usage.NewStore(pool)

	defaults := limits.Defaults{
		PlanCode: "default", MaxAgents: 100, MaxDomains: 10,
		MaxMessagesMonth: 1_000_000, MaxStorageBytes: 1 << 40,
	}
	enf := limits.NewEnforcer(limits.NewStore(pool), usageStore, defaults, 0)

	api := agent.NewAPI(store, sender, smtpRelay, userAuth, usage.NewNoopUsageTracker(), "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetEnforcer(enf)
	api.SetUsageStore(usageStore)
	api.SetInternalAPISecret(internalSecret)

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, store, pool, enf
}

func TestGetMyLimits_ReturnsDefaultsForNewUser(t *testing.T) {
	server, store, _, _ := setupAPIWithLimits(t, "")
	token := createTestUser(t, store, "limitsread@test.com")

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/users/me/limits", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
	var info agent.LimitsInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.PlanCode != "default" {
		t.Errorf("PlanCode = %q, want default", info.PlanCode)
	}
	if info.Limits.MaxAgents != 100 {
		t.Errorf("MaxAgents = %d, want 100", info.Limits.MaxAgents)
	}
	if info.Usage.Agents != 0 || info.Usage.Domains != 0 || info.Usage.MessagesMonth != 0 || info.Usage.StorageBytes != 0 {
		t.Errorf("Usage on brand-new user = %+v, want all zeros", info.Usage)
	}
	if info.UpgradeURL != "" {
		t.Errorf("UpgradeURL on default = %q, want empty", info.UpgradeURL)
	}
}

func TestGetMyLimits_ReflectsAccountLimitsRow(t *testing.T) {
	server, store, pool, enf := setupAPIWithLimits(t, "")
	token := createTestUser(t, store, "rowuser@test.com")

	// CreateOrGetUser with the same email returns the existing user
	// row (idempotent), which gives us the user_id we need to write
	// the account_limits FK target.
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "rowuser@test.com", "Test User", "google-rowuser@test.com")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// Write the row through the same pool the API reads from.
	ls := limits.NewStore(pool)
	if err := ls.Upsert(ctx, user.ID, limits.Limits{
		PlanCode:         "test_pro",
		MaxAgents:        50,
		MaxDomains:       10,
		MaxMessagesMonth: 50_000,
		MaxStorageBytes:  10 << 30,
		UpgradeURL:       "https://billing.example/portal",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Bust the cache so the API picks up the new row immediately.
	enf.Invalidate(user.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/users/me/limits", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
	var info agent.LimitsInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.PlanCode != "test_pro" {
		t.Errorf("PlanCode = %q, want test_pro", info.PlanCode)
	}
	if info.UpgradeURL != "https://billing.example/portal" {
		t.Errorf("UpgradeURL = %q, want billing.example/portal", info.UpgradeURL)
	}
	if info.Limits.MaxAgents != 50 {
		t.Errorf("MaxAgents = %d, want 50", info.Limits.MaxAgents)
	}
}

func TestInvalidateLimits_RejectsMissingSignature(t *testing.T) {
	server, _, _, _ := setupAPIWithLimits(t, "test-secret-1")

	body := []byte(`{"user_id":"u_x"}`)
	req, _ := http.NewRequest("POST", server.URL+"/api/internal/limits/invalidate", bytes.NewReader(body))
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing sig: status = %d, want 401", resp.StatusCode)
	}
}

func TestInvalidateLimits_RejectsWrongSignature(t *testing.T) {
	server, _, _, _ := setupAPIWithLimits(t, "test-secret-2")

	body := []byte(`{"user_id":"u_x"}`)
	req, _ := http.NewRequest("POST", server.URL+"/api/internal/limits/invalidate", bytes.NewReader(body))
	req.Header.Set("X-E2A-Internal-Signature", "deadbeef")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong sig: status = %d, want 401", resp.StatusCode)
	}
}

func TestInvalidateLimits_AcceptsCorrectSignature(t *testing.T) {
	secret := "test-secret-3"
	server, _, _, _ := setupAPIWithLimits(t, secret)

	body := []byte(`{"user_id":"u_xyz"}`)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	sig := hex.EncodeToString(h.Sum(nil))

	req, _ := http.NewRequest("POST", server.URL+"/api/internal/limits/invalidate", bytes.NewReader(body))
	req.Header.Set("X-E2A-Internal-Signature", sig)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		buf, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d body=%s, want 204", resp.StatusCode, string(buf))
	}
}

func TestInvalidateLimits_503WhenSecretUnset(t *testing.T) {
	server, _, _, _ := setupAPIWithLimits(t, "")

	body := []byte(`{"user_id":"u_x"}`)
	req, _ := http.NewRequest("POST", server.URL+"/api/internal/limits/invalidate", bytes.NewReader(body))
	req.Header.Set("X-E2A-Internal-Signature", "anything")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("no-secret config: status = %d, want 503", resp.StatusCode)
	}
}

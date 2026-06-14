package agent_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

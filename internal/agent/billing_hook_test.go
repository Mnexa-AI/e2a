package agent_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gorilla/mux"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
)

// These tests cover the user-delete → billing-hook escape valve.
// The hook is best-effort: a failure must NOT block account deletion
// (otherwise a billing-service outage holds users hostage to a
// subscription they can't cancel by deleting their account).

// recordedHookCall captures what the OSS server sent the hook.
type recordedHookCall struct {
	mu        sync.Mutex
	body      []byte
	signature string
	called    bool
}

// setupAPIWithBillingHook wires an API with a configured billing-hook
// URL pointing at the returned httptest server. The returned hookSrv
// behaves per `hookStatus` so individual tests can simulate success
// (204), transient outage (500), unreachable (close the server), etc.
func setupAPIWithBillingHook(t *testing.T, internalSecret string, hookStatus int) (*httptest.Server, *identity.Store, *recordedHookCall, *httptest.Server) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	userAuth := auth.NewUserAuth(&config.OAuthConfig{}, store, false)

	rec := &recordedHookCall{}
	hookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.body = body
		rec.signature = r.Header.Get("X-E2A-Internal-Signature")
		rec.called = true
		rec.mu.Unlock()
		w.WriteHeader(hookStatus)
	}))
	t.Cleanup(hookSrv.Close)

	api := agent.NewAPI(store, sender, smtpRelay, userAuth, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetInternalAPISecret(internalSecret)
	api.SetBillingHookURL(hookSrv.URL)

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	apiSrv := httptest.NewServer(router)
	t.Cleanup(apiSrv.Close)
	return apiSrv, store, rec, hookSrv
}

// expectedHMAC re-derives what the X-E2A-Internal-Signature header
// SHOULD be for the recorded body. Independent computation, so a bug
// in either side (sign or verify) shows up.
func expectedHMAC(secret string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// --- Happy path: hook fires with right user_id + signature ---

func TestDeleteUser_FiresBillingHook(t *testing.T) {
	secret := "test-internal-secret"
	apiSrv, store, rec, _ := setupAPIWithBillingHook(t, secret, http.StatusNoContent)
	token := createTestUser(t, store, "hook-fires@test.com")
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "hook-fires@test.com", "Test", "google-hook-fires@test.com")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	req, _ := http.NewRequest("DELETE", apiSrv.URL+"/api/v1/users/me?confirm=DELETE", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete status = %d body = %s", resp.StatusCode, string(buf))
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.called {
		t.Fatalf("billing hook was not called")
	}

	// Body shape: {"user_id":"<id>"}.
	var hookBody struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(rec.body, &hookBody); err != nil {
		t.Fatalf("hook body not JSON: %v (body=%q)", err, string(rec.body))
	}
	if hookBody.UserID != user.ID {
		t.Errorf("hook user_id = %q, want %q", hookBody.UserID, user.ID)
	}

	// Signature matches an independent HMAC over the exact body the
	// hook received. Catches both signing-side bugs (wrong key, wrong
	// algorithm) and body-bytes drift (whitespace, key order).
	if got, want := rec.signature, expectedHMAC(secret, rec.body); got != want {
		t.Errorf("signature mismatch:\n  got      %s\n  expected %s", got, want)
	}

	// User actually deleted (the cascade ran after the hook).
	if _, err := store.GetUserByID(ctx, user.ID); err == nil {
		t.Errorf("user still exists after delete; cascade should have removed them")
	}
}

// --- Best-effort: hook failures do NOT block deletion ---

func TestDeleteUser_HookFailureDoesNotBlockDeletion(t *testing.T) {
	// Hook returns 500 on every call — simulates billing-service outage.
	apiSrv, store, rec, _ := setupAPIWithBillingHook(t, "secret", http.StatusInternalServerError)
	token := createTestUser(t, store, "hook-fails@test.com")
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "hook-fails@test.com", "Test", "google-hook-fails@test.com")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	req, _ := http.NewRequest("DELETE", apiSrv.URL+"/api/v1/users/me?confirm=DELETE", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 despite hook failure", resp.StatusCode)
	}

	rec.mu.Lock()
	called := rec.called
	rec.mu.Unlock()
	if !called {
		t.Errorf("hook should have been attempted before deletion")
	}
	// User STILL deleted despite hook failure. This is the
	// load-bearing assertion: deletion is the user's right and a
	// billing-side outage must not hold their right hostage.
	if _, err := store.GetUserByID(ctx, user.ID); err == nil {
		t.Errorf("hook failed but user was not deleted; cascade must proceed regardless")
	}
}

// --- No hook configured: deletion still proceeds, no HTTP call ---

func TestDeleteUser_NoHookConfigured(t *testing.T) {
	// Standard setup without SetBillingHookURL — self-host pattern.
	server, store, _ := setupAPI(t)
	token := createTestUser(t, store, "no-hook@test.com")
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "no-hook@test.com", "Test", "google-no-hook@test.com")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	req, _ := http.NewRequest("DELETE", server.URL+"/api/v1/users/me?confirm=DELETE", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", resp.StatusCode)
	}
	if _, err := store.GetUserByID(ctx, user.ID); err == nil {
		t.Errorf("user still exists after delete")
	}
}

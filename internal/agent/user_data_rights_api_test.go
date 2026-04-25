package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// authedGet / authedDelete are local helpers — api_test.go in this
// package already has authedPost. Duplicating the pattern here keeps
// the new test file self-contained and doesn't require touching the
// existing api_test.go.
func authedGet(t *testing.T, url, apiKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func authedDelete(t *testing.T, url, apiKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestHandleExportUserData covers the right-of-access HTTP path: a
// logged-in user calling GET /api/v1/users/me/export gets a JSON dump
// of their own data and nothing else.
func TestHandleExportUserData(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "exporter@example.com")

	// Add a domain + agent so the export has something to enumerate.
	ctx := context.Background()
	user, _ := store.GetUserByAPIKey(ctx, apiKey)
	if _, err := store.ClaimOrCreateDomain(ctx, "exporter.example.com", user.ID); err != nil {
		t.Fatalf("claim domain: %v", err)
	}
	if err := store.VerifyDomain(ctx, "exporter.example.com", user.ID); err != nil {
		t.Fatalf("verify domain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, "bot@exporter.example.com", "exporter.example.com",
		"Bot", "https://exporter.example.com/hook", "cloud", user.ID); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	resp := authedGet(t, server.URL+"/api/v1/users/me/export", apiKey)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment header", got)
	}

	var dump identity.UserExport
	if err := json.NewDecoder(resp.Body).Decode(&dump); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dump.User.Email != "exporter@example.com" {
		t.Errorf("user.email = %q, want exporter@example.com", dump.User.Email)
	}
	if len(dump.Domains) != 1 {
		t.Errorf("domains: got %d, want 1", len(dump.Domains))
	}
	if len(dump.Agents) != 1 {
		t.Errorf("agents: got %d, want 1", len(dump.Agents))
	}
}

// TestHandleExportUserData_RequiresAuth proves an unauthenticated
// request can't dump anything.
func TestHandleExportUserData_RequiresAuth(t *testing.T) {
	server, _, _ := setupAPI(t)

	resp, err := http.Get(server.URL + "/api/v1/users/me/export")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestHandleDeleteUserData_RequiresConfirm — server-side guardrail
// against a stray DELETE landing without the explicit confirm flag.
func TestHandleDeleteUserData_RequiresConfirm(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "guard@example.com")

	// No ?confirm=DELETE — should 400.
	resp := authedDelete(t, server.URL+"/api/v1/users/me", apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status without confirm = %d, want 400", resp.StatusCode)
	}

	// Wrong confirm value — also 400.
	resp2 := authedDelete(t, server.URL+"/api/v1/users/me?confirm=yes", apiKey)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("status with wrong confirm = %d, want 400", resp2.StatusCode)
	}

	// User should still exist.
	user, err := store.GetUserByAPIKey(context.Background(), apiKey)
	if err != nil || user == nil {
		t.Errorf("user gone despite the guardrail blocking the delete: err=%v", err)
	}
}

// TestHandleDeleteUserData covers the happy path: with the right
// confirm flag, the user and all their data is gone, and a follow-up
// authed call with the same key fails (key cascade-deleted).
func TestHandleDeleteUserData(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "kaboom@example.com")

	resp := authedDelete(t, server.URL+"/api/v1/users/me?confirm=DELETE", apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var res identity.DeleteUserDataResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.UserDeleted {
		t.Error("user_deleted should be true")
	}
	if res.APIKeysDeleted != 1 {
		t.Errorf("api_keys_deleted = %d, want 1", res.APIKeysDeleted)
	}

	// Subsequent calls with the same key should fail — the key is gone
	// along with the user. This is also the natural session-revocation
	// behavior we want from the deletion flow.
	follow := authedGet(t, server.URL+"/api/v1/users/me/export", apiKey)
	defer follow.Body.Close()
	if follow.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-delete export status = %d, want 401 (key should be revoked)", follow.StatusCode)
	}
}

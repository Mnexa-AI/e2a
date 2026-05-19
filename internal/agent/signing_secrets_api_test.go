package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// All tests here exercise the /api/v1/users/me/signing-secrets routes.
// The setupAPI helper is shared with the other tests in this package.

type signingSecretSummary struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Secret       string  `json:"secret"`
	SecretPrefix string  `json:"secret_prefix"`
	CreatedAt    string  `json:"created_at"`
	LastSignedAt *string `json:"last_signed_at,omitempty"`
}

type listResp struct {
	Secrets []signingSecretSummary `json:"secrets"`
}

type createResp struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Secret       string `json:"secret"`
	SecretPrefix string `json:"secret_prefix"`
	CreatedAt    string `json:"created_at"`
}

func authedReq(t *testing.T, method, url, body, apiKey string) *http.Response {
	t.Helper()
	var bodyR io.Reader
	if body != "" {
		bodyR = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, url, bodyR)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// --- Auth ---

func TestSigningSecrets_Unauthenticated(t *testing.T) {
	server, _, _ := setupAPI(t)
	for _, c := range []struct {
		method, path string
	}{
		{"GET", "/api/v1/users/me/signing-secrets"},
		{"POST", "/api/v1/users/me/signing-secrets"},
		{"DELETE", "/api/v1/users/me/signing-secrets/wsec_anything"},
	} {
		req, _ := http.NewRequest(c.method, server.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", c.method, c.path, resp.StatusCode)
		}
	}
}

// --- List ---

// The list endpoint now exposes the plaintext `secret` so the
// dashboard can display the full value on demand. The 12-char
// `secret_prefix` is kept for clients that only want the preview and
// must equal the first 12 chars of the secret.
func TestSigningSecrets_List_IncludesPlaintext(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "list-includes@example.com")

	resp := authedReq(t, "GET", server.URL+"/api/v1/users/me/signing-secrets", "", apiKey)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var got listResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	// New users have one auto-created "default" secret.
	if len(got.Secrets) != 1 {
		t.Fatalf("expected 1 default secret, got %d", len(got.Secrets))
	}
	if got.Secrets[0].SecretPrefix == "" {
		t.Error("SecretPrefix should be populated")
	}
	if len(got.Secrets[0].Secret) != 64 {
		t.Errorf("Secret length = %d, want 64 hex chars on list", len(got.Secrets[0].Secret))
	}
	if got.Secrets[0].Secret[:12] != got.Secrets[0].SecretPrefix {
		t.Errorf("Secret[:12] = %q, SecretPrefix = %q", got.Secrets[0].Secret[:12], got.Secrets[0].SecretPrefix)
	}
}

// --- Create ---

func TestSigningSecrets_Create_ReturnsPlaintext(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "create-plaintext@example.com")

	resp := authedReq(t, "POST", server.URL+"/api/v1/users/me/signing-secrets",
		`{"name":"prod"}`, apiKey)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, body)
	}

	var got createResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.ID, "wsec_") {
		t.Errorf("ID = %q, should start with wsec_", got.ID)
	}
	if len(got.Secret) != 64 {
		t.Errorf("Secret length = %d, want 64 hex chars", len(got.Secret))
	}
	if got.Name != "prod" {
		t.Errorf("Name = %q, want prod", got.Name)
	}

	// Verify the secret really lives in the store and is readable.
	ctx := context.Background()
	user, _ := store.GetUserByAPIKey(ctx, apiKey)
	secrets, _ := store.GetUserSigningSecrets(ctx, user.ID)
	found := false
	for _, s := range secrets {
		if s.ID == got.ID && s.Secret == got.Secret {
			found = true
			break
		}
	}
	if !found {
		t.Error("created secret not found in store")
	}

	// Subsequent list must include the same plaintext (this is the
	// "view secret again" behavior the dashboard needs).
	listR := authedReq(t, "GET", server.URL+"/api/v1/users/me/signing-secrets", "", apiKey)
	defer listR.Body.Close()
	listBody, _ := io.ReadAll(listR.Body)
	if !strings.Contains(string(listBody), got.Secret) {
		t.Errorf("list missing plaintext after create:\n%s", listBody)
	}
}

func TestSigningSecrets_Create_EmptyNameDefaults(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "create-empty-name@example.com")

	resp := authedReq(t, "POST", server.URL+"/api/v1/users/me/signing-secrets",
		`{}`, apiKey)
	defer resp.Body.Close()

	var got createResp
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Name != "unnamed" {
		t.Errorf("Name should default to \"unnamed\", got %q", got.Name)
	}
}

func TestSigningSecrets_Create_RejectsCapWithBadRequest(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "create-cap@example.com")

	// Default already exists → cap headroom is MaxSigningSecretsPerUser-1.
	for i := 0; i < identity.MaxSigningSecretsPerUser-1; i++ {
		r := authedReq(t, "POST", server.URL+"/api/v1/users/me/signing-secrets",
			`{"name":"fill"}`, apiKey)
		r.Body.Close()
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("setup create %d: status %d", i, r.StatusCode)
		}
	}

	// One more must fail with 400 + the cap message.
	r := authedReq(t, "POST", server.URL+"/api/v1/users/me/signing-secrets",
		`{"name":"over"}`, apiKey)
	defer r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("over-cap status = %d, want 400", r.StatusCode)
	}
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), "at most") {
		t.Errorf("over-cap body should mention cap, got: %s", body)
	}
}

// --- Delete ---

func TestSigningSecrets_Delete_HappyPath(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "delete-happy@example.com")

	// Need ≥2 secrets so we can delete one.
	r := authedReq(t, "POST", server.URL+"/api/v1/users/me/signing-secrets",
		`{"name":"to-delete"}`, apiKey)
	var created createResp
	json.NewDecoder(r.Body).Decode(&created)
	r.Body.Close()

	delR := authedReq(t, "DELETE",
		server.URL+"/api/v1/users/me/signing-secrets/"+created.ID, "", apiKey)
	defer delR.Body.Close()
	if delR.StatusCode != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", delR.StatusCode)
	}
}

func TestSigningSecrets_Delete_RefusesLast(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "delete-last@example.com")

	// User starts with one default secret. Find its ID via list.
	listR := authedReq(t, "GET", server.URL+"/api/v1/users/me/signing-secrets", "", apiKey)
	var got listResp
	json.NewDecoder(listR.Body).Decode(&got)
	listR.Body.Close()
	if len(got.Secrets) != 1 {
		t.Fatalf("expected 1 default secret, got %d", len(got.Secrets))
	}

	delR := authedReq(t, "DELETE",
		server.URL+"/api/v1/users/me/signing-secrets/"+got.Secrets[0].ID, "", apiKey)
	defer delR.Body.Close()
	if delR.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete-last status = %d, want 400", delR.StatusCode)
	}
	body, _ := io.ReadAll(delR.Body)
	if !strings.Contains(string(body), "cannot delete the last") {
		t.Errorf("body should explain why: %s", body)
	}
}

func TestSigningSecrets_Delete_OtherUsersSecret_Returns404(t *testing.T) {
	// Cross-user isolation: user B must not be able to delete user A's secret.
	server, store, _ := setupAPI(t)
	keyA := createTestUser(t, store, "owner-a@example.com")
	keyB := createTestUser(t, store, "owner-b@example.com")

	// A creates an extra secret so it has ≥2 (otherwise even A's
	// delete would fail; we want to test ownership, not the floor).
	r := authedReq(t, "POST", server.URL+"/api/v1/users/me/signing-secrets",
		`{"name":"a-secret"}`, keyA)
	var aCreated createResp
	json.NewDecoder(r.Body).Decode(&aCreated)
	r.Body.Close()

	// B tries to delete A's secret by ID.
	delR := authedReq(t, "DELETE",
		server.URL+"/api/v1/users/me/signing-secrets/"+aCreated.ID, "", keyB)
	defer delR.Body.Close()
	if delR.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user delete status = %d, want 404", delR.StatusCode)
	}

	// A's secret should still exist after B's failed attempt.
	listR := authedReq(t, "GET", server.URL+"/api/v1/users/me/signing-secrets", "", keyA)
	defer listR.Body.Close()
	var aList listResp
	json.NewDecoder(listR.Body).Decode(&aList)
	found := false
	for _, s := range aList.Secrets {
		if s.ID == aCreated.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("A's secret was somehow deleted by B")
	}
}

// Regression test for the most-load-bearing property of this
// endpoint: now that List returns plaintext, a cross-user query must
// never see another user's secret. The scoping is implicit in the SQL
// `WHERE user_id = $1`, and this test exists to catch a future
// refactor that loosens it.
func TestSigningSecrets_List_DoesNotLeakOtherUsers(t *testing.T) {
	server, store, _ := setupAPI(t)
	keyA := createTestUser(t, store, "leak-a@example.com")
	keyB := createTestUser(t, store, "leak-b@example.com")

	// A creates a named secret so we have a specific plaintext to look for.
	r := authedReq(t, "POST", server.URL+"/api/v1/users/me/signing-secrets",
		`{"name":"a-only"}`, keyA)
	var aCreated createResp
	json.NewDecoder(r.Body).Decode(&aCreated)
	r.Body.Close()

	// B lists. The response body must not contain A's plaintext or ID.
	listR := authedReq(t, "GET", server.URL+"/api/v1/users/me/signing-secrets", "", keyB)
	defer listR.Body.Close()
	body, _ := io.ReadAll(listR.Body)
	if strings.Contains(string(body), aCreated.Secret) {
		t.Errorf("B's list leaked A's plaintext secret:\n%s", body)
	}
	if strings.Contains(string(body), aCreated.ID) {
		t.Errorf("B's list leaked A's secret ID:\n%s", body)
	}
}

// The list response carries live credentials, so it must instruct
// intermediaries not to cache it. Defense-in-depth — the
// Authorization header should already prevent shared caches from
// storing it, but we set Cache-Control: no-store explicitly.
func TestSigningSecrets_List_SetsNoStore(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "no-store@example.com")

	resp := authedReq(t, "GET", server.URL+"/api/v1/users/me/signing-secrets", "", apiKey)
	defer resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

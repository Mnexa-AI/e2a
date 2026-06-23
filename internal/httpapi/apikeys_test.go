package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

func TestListAPIKeys(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/account/api-keys", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("want 1 key, got %v", body)
	}
	k, _ := items[0].(map[string]any)
	if k["id"] != "apk_1" || k["scope"] != "account" {
		t.Fatalf("unexpected key: %v", k)
	}
	// List never leaks the plaintext secret.
	if _, leaked := k["key"]; leaked {
		t.Fatalf("list response must not include the plaintext key: %v", k)
	}
}

func TestCreateAccountAPIKey(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/account/api-keys", "good", map[string]any{"name": "ci"})
	if code != 201 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["scope"] != "account" {
		t.Fatalf("want account scope, got %v", body)
	}
	if body["key"] != "e2a_account_secret" {
		t.Fatalf("create must return the one-time plaintext key, got %v", body["key"])
	}
}

func TestCreateAgentScopedAPIKey(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/account/api-keys", "good", map[string]any{
		"name": "inbox-bot", "scope": "agent", "agent": "support@acme.com",
	})
	if code != 201 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["scope"] != "agent" || body["agent"] != "support@acme.com" {
		t.Fatalf("want agent scope bound to support@acme.com, got %v", body)
	}
	if body["key"] != "e2a_agent_secret" {
		t.Fatalf("unexpected key: %v", body["key"])
	}
}

func TestCreateAgentScopedAPIKeyRequiresAgent(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/account/api-keys", "good", map[string]any{"scope": "agent"})
	if code != 400 {
		t.Fatalf("want 400 when scope=agent without agent, got %d", code)
	}
}

func TestCreateAgentScopedAPIKeyUnknownAgent(t *testing.T) {
	srv := testServer(t)
	// resolveOwnedAgent rejects an agent the caller doesn't own (403).
	code, _ := postJSON(t, srv.URL+"/v1/account/api-keys", "good", map[string]any{
		"scope": "agent", "agent": "stranger@evil.com",
	})
	if code != 403 {
		t.Fatalf("want 403 for an unowned agent, got %d", code)
	}
}

func TestCreateAPIKeyRejectsBadScope(t *testing.T) {
	srv := testServer(t)
	// The `scope` enum is enforced by Huma request validation → 422.
	code, _ := postJSON(t, srv.URL+"/v1/account/api-keys", "good", map[string]any{"scope": "root"})
	if code != 422 {
		t.Fatalf("want 422 for invalid scope, got %d", code)
	}
}

func TestCreateAPIKeyRejectsBadExpiry(t *testing.T) {
	srv := testServer(t)
	// `expires_at` has format:date-time → malformed value fails Huma validation (422).
	code, _ := postJSON(t, srv.URL+"/v1/account/api-keys", "good", map[string]any{"expires_at": "not-a-date"})
	if code != 422 {
		t.Fatalf("want 422 for malformed expires_at, got %d", code)
	}
}

func TestCreateAPIKeyRejectsPastExpiry(t *testing.T) {
	srv := testServer(t)
	// A well-formed RFC 3339 timestamp in the past passes format validation
	// and is rejected by the handler's own future check (400).
	code, _ := postJSON(t, srv.URL+"/v1/account/api-keys", "good", map[string]any{"expires_at": "2000-01-01T00:00:00Z"})
	if code != 400 {
		t.Fatalf("want 400 for past expires_at, got %d", code)
	}
}

func TestDeleteAPIKey(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/account/api-keys/apk_1", "good", nil)
	if code != 204 {
		t.Fatalf("want 204, got %d", code)
	}
}

func TestDeleteAPIKeyNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/account/api-keys/apk_missing", "good", nil)
	if code != 404 {
		t.Fatalf("want 404 for unknown key, got %d", code)
	}
}

func TestAPIKeysUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/account/api-keys", "")
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}

// A non-sentinel store error (e.g. a DB failure) on delete must surface as
// 500 — NOT be masked as a 404 "not found", which would tell the caller a
// live key was already revoked. Regression guard for the error-mapping split.
func TestDeleteAPIKeyInternalErrorNotMaskedAs404(t *testing.T) {
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		DeleteAPIKey: func(ctx context.Context, keyID, userID string) error {
			return errors.New("db connection lost")
		},
	}))
	defer srv.Close()
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/account/api-keys/apk_x", "good", nil)
	if code != 500 {
		t.Fatalf("want 500 for a non-sentinel delete error, got %d", code)
	}
}

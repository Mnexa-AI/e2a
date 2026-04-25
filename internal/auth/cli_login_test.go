package auth_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"golang.org/x/oauth2"
)

func TestHandleLogin_EncodesCliParamsInOAuthState(t *testing.T) {
	ua, _, _ := setupUserAuth(t)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/auth/login?cli_callback=http://127.0.0.1:43123/callback&cli_state=cli_state_123",
		nil,
	)
	w := httptest.NewRecorder()

	ua.HandleLogin(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	res := w.Result()
	location := res.Header.Get("Location")
	if !strings.Contains(location, "accounts.google.com") {
		t.Fatalf("redirect location = %q, want Google OAuth URL", location)
	}

	// Parse the OAuth state parameter from the redirect URL
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	stateParam := u.Query().Get("state")
	if stateParam == "" {
		t.Fatal("state parameter not set in redirect URL")
	}

	// Decode and verify the state contains CLI params
	stateJSON, err := base64.URLEncoding.DecodeString(stateParam)
	if err != nil {
		t.Fatalf("decode state: %v", err)
	}
	var state struct {
		Nonce       string `json:"n"`
		CLICallback string `json:"cb"`
		CLIState    string `json:"cs"`
	}
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if state.Nonce == "" {
		t.Fatal("nonce not set in state")
	}
	if state.CLICallback != "http://127.0.0.1:43123/callback" {
		t.Fatalf("cli callback = %q, want %q", state.CLICallback, "http://127.0.0.1:43123/callback")
	}
	if state.CLIState != "cli_state_123" {
		t.Fatalf("cli state = %q, want %q", state.CLIState, "cli_state_123")
	}

	// Verify the nonce cookie is still set (for CSRF verification in callback)
	var sawNonceCookie bool
	for _, cookie := range res.Cookies() {
		if cookie.Name == "e2a_oauth_state" && cookie.Value == state.Nonce {
			sawNonceCookie = true
		}
	}
	if !sawNonceCookie {
		t.Fatal("oauth state nonce cookie not set")
	}
}

func TestHandleLogin_WebLoginOmitsCliParams(t *testing.T) {
	ua, _, _ := setupUserAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	w := httptest.NewRecorder()
	ua.HandleLogin(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	u, _ := url.Parse(w.Result().Header.Get("Location"))
	stateParam := u.Query().Get("state")
	stateJSON, _ := base64.URLEncoding.DecodeString(stateParam)
	var state struct {
		CLICallback string `json:"cb"`
		CLIState    string `json:"cs"`
	}
	json.Unmarshal(stateJSON, &state)
	if state.CLICallback != "" || state.CLIState != "" {
		t.Fatalf("web login should not contain CLI params, got cb=%q cs=%q", state.CLICallback, state.CLIState)
	}
}

func TestHandleLogin_RejectsInvalidCLICallback(t *testing.T) {
	ua, _, _ := setupUserAuth(t)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/auth/login?cli_callback=https://example.com/callback&cli_state=cli_state_123",
		nil,
	)
	w := httptest.NewRecorder()

	ua.HandleLogin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "loopback") && !strings.Contains(w.Body.String(), "http") {
		t.Fatalf("unexpected error body: %q", w.Body.String())
	}
}

// fakeGoogleOAuth starts a test server that mimics Google's token and userinfo
// endpoints. Returns the server and an oauth2.Config pointing at it.
func fakeGoogleOAuth(t *testing.T) (*httptest.Server, *oauth2.Config) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":            "google-sub-cli-test",
			"email":          "cliuser@test.com",
			"email_verified": true,
			"name":           "CLI User",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	oauthCfg := &oauth2.Config{
		ClientID:     "test",
		ClientSecret: "test",
		RedirectURL:  "http://localhost/api/auth/callback",
		Endpoint: oauth2.Endpoint{
			TokenURL: srv.URL + "/token",
			AuthURL:  srv.URL + "/authorize",
		},
		Scopes: []string{"openid", "email", "profile"},
	}
	return srv, oauthCfg
}

// setupUserAuthWithFakeOAuth creates a UserAuth backed by a fake Google OAuth
// server so we can test HandleCallback without hitting real Google.
func setupUserAuthWithFakeOAuth(t *testing.T) (*auth.UserAuth, *identity.Store, *httptest.Server) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	srv, oauthCfg := fakeGoogleOAuth(t)

	cfg := &config.OAuthConfig{
		GoogleClientID:     oauthCfg.ClientID,
		GoogleClientSecret: oauthCfg.ClientSecret,
		RedirectURL:        oauthCfg.RedirectURL,
	}
	ua := auth.NewUserAuthWithOAuthConfig(cfg, oauthCfg, store, false, srv.URL+"/userinfo")

	return ua, store, srv
}

func TestHandleCallback_CLILogin_HandsOffToCLI(t *testing.T) {
	ua, _, srv := setupUserAuthWithFakeOAuth(t)

	nonce := "test-nonce-123"
	state := auth.EncodeOAuthState(&auth.OAuthState{
		Nonce:       nonce,
		CLICallback: "http://127.0.0.1:43123/callback",
		CLIState:    "cli_state_abc",
	})

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/auth/callback?code=fake-code&state=%s", url.QueryEscape(state)),
		nil,
	)
	req.AddCookie(&http.Cookie{Name: "e2a_oauth_state", Value: nonce})
	_ = srv // keep server alive
	w := httptest.NewRecorder()

	ua.HandleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	body := w.Body.String()
	// The CLI handoff page should contain the callback URL and CLI state
	if !strings.Contains(body, "http://127.0.0.1:43123/callback") {
		t.Fatalf("handoff page missing callback URL, body: %s", body)
	}
	if !strings.Contains(body, "cli_state_abc") {
		t.Fatalf("handoff page missing cli_state, body: %s", body)
	}
	if !strings.Contains(body, "api_key") {
		t.Fatalf("handoff page missing api_key, body: %s", body)
	}
}

func TestHandleCallback_WebLogin_RedirectsToDashboard(t *testing.T) {
	ua, _, srv := setupUserAuthWithFakeOAuth(t)

	nonce := "test-nonce-456"
	state := auth.EncodeOAuthState(&auth.OAuthState{
		Nonce: nonce,
	})

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/auth/callback?code=fake-code&state=%s", url.QueryEscape(state)),
		nil,
	)
	req.AddCookie(&http.Cookie{Name: "e2a_oauth_state", Value: nonce})
	_ = srv
	w := httptest.NewRecorder()

	ua.HandleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusFound, w.Body.String())
	}

	location := w.Header().Get("Location")
	if !strings.Contains(location, "/dashboard") {
		t.Fatalf("expected redirect to dashboard, got %q", location)
	}
}

func TestHandleCallback_NonceMismatch_Rejected(t *testing.T) {
	ua, _, srv := setupUserAuthWithFakeOAuth(t)

	state := auth.EncodeOAuthState(&auth.OAuthState{
		Nonce:       "correct-nonce",
		CLICallback: "http://127.0.0.1:43123/callback",
		CLIState:    "cli_state_abc",
	})

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/auth/callback?code=fake-code&state=%s", url.QueryEscape(state)),
		nil,
	)
	req.AddCookie(&http.Cookie{Name: "e2a_oauth_state", Value: "wrong-nonce"})
	_ = srv
	w := httptest.NewRecorder()

	ua.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCallback_InvalidState_Rejected(t *testing.T) {
	ua, _, srv := setupUserAuthWithFakeOAuth(t)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/auth/callback?code=fake-code&state=not-valid-base64!!!",
		nil,
	)
	req.AddCookie(&http.Cookie{Name: "e2a_oauth_state", Value: "whatever"})
	_ = srv
	w := httptest.NewRecorder()

	ua.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

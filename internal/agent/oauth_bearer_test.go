package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"

	"github.com/gorilla/mux"
)

// mintTokensForFixture runs the full authorize → token flow on the
// consent fixture so we end up with a real fosite-issued access +
// refresh token pair ready to use as bearers in /api/v1/* calls. This
// is the same path a real MCP client takes; doing the inline flow
// keeps the bearer round-trip test scoped to slice 9 without
// requiring a live web/consent UI.
func mintTokensForFixture(t *testing.T, f *consentFixture) (accessToken, refreshToken string) {
	t.Helper()
	verifier, challenge := newPKCE(t)

	// /consent (which mints and stores the code).
	form := authorizeParams(challenge, f.clientID, "s1s1s1s1s1s1s1s1")
	form.Set("action", "allow")
	form.Set("agent_choice", "create_new")
	form.Set("new_agent_slug", "bearer-"+randHex8(t))
	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("mintTokens: no code in redirect: %s", resp.Header.Get("Location"))
	}

	// /token: exchange the code.
	tokForm := url.Values{}
	tokForm.Set("grant_type", "authorization_code")
	tokForm.Set("code", code)
	tokForm.Set("client_id", f.clientID)
	tokForm.Set("redirect_uri", "http://localhost:8765/callback")
	tokForm.Set("code_verifier", verifier)
	tokResp, err := http.Post(f.server.URL+"/api/oauth/token",
		"application/x-www-form-urlencoded", strings.NewReader(tokForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer tokResp.Body.Close()
	if tokResp.StatusCode != http.StatusOK {
		t.Fatalf("mintTokens: /token status = %d", tokResp.StatusCode)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(tokResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.AccessToken, body.RefreshToken
}

// callAPIWithBearer hits /api/v1/agents with the given Authorization
// value. Returns the status code and the WWW-Authenticate header.
// /api/v1/agents is the simplest authed endpoint that any user can hit.
func callAPIWithBearer(t *testing.T, serverURL, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", serverURL+"/api/v1/agents", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("WWW-Authenticate")
}

// ──────────────────────── Bearer dispatch tests ────────────────────────

// TestBearer_OAuthToken_Active is THE end-to-end milestone: a freshly-
// minted OAuth access token authenticates /api/v1/agents. Without
// bearer dispatch, the token from /token would be inert.
func TestBearer_OAuthToken_Active(t *testing.T) {
	f := newConsentFixture(t)
	access, _ := mintTokensForFixture(t, f)
	if !strings.HasPrefix(access, oauth.AccessTokenPrefix) {
		t.Fatalf("expected access token to have %q prefix; got %q", oauth.AccessTokenPrefix, access)
	}
	status, _ := callAPIWithBearer(t, f.server.URL, access)
	if status != http.StatusOK {
		t.Fatalf("active OAuth token should authenticate: got %d", status)
	}
}

// TestBearer_OAuthToken_Revoked: after a manual revocation of the
// access-token row, the same bearer returns 401 with the OAuth
// WWW-Authenticate challenge.
func TestBearer_OAuthToken_Revoked(t *testing.T) {
	f := newConsentFixture(t)
	access, _ := mintTokensForFixture(t, f)

	// Revoke by setting revoked_at on every access row issued for our
	// user. (No /revoke endpoint yet — that lands in slice 6.)
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE oauth_access_tokens SET revoked_at = NOW() WHERE user_id = $1`, f.userID); err != nil {
		t.Fatal(err)
	}

	status, wa := callAPIWithBearer(t, f.server.URL, access)
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked OAuth token should 401: got %d", status)
	}
	if wa == "" {
		t.Fatal("revoked OAuth bearer must carry WWW-Authenticate per RFC 6750 §3")
	}
	if !strings.Contains(wa, `error="invalid_token"`) {
		t.Errorf(`WWW-Authenticate should contain error="invalid_token": got %q`, wa)
	}
}

// TestBearer_OAuthToken_Unknown: a well-formed-looking but never-
// issued ate2a_ bearer is rejected with the OAuth challenge header.
func TestBearer_OAuthToken_Unknown(t *testing.T) {
	f := newConsentFixture(t)
	status, wa := callAPIWithBearer(t, f.server.URL, oauth.AccessTokenPrefix+"deadbeef.deadbeef")
	if status != http.StatusUnauthorized {
		t.Fatalf("unknown OAuth token should 401: got %d", status)
	}
	if wa == "" {
		t.Fatal("unknown OAuth bearer must carry WWW-Authenticate")
	}
}

// TestBearer_APIKey_StillWorks: regression guard. The legacy API-key
// path must continue to work after we added the OAuth branch.
func TestBearer_APIKey_StillWorks(t *testing.T) {
	f := newConsentFixture(t)
	store := identity.NewStore(f.pool)
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "apikey-"+randHex8(t)+"@example.com", "API Key User", "google-apikey-"+randHex8(t))
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, user.ID, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	status, wa := callAPIWithBearer(t, f.server.URL, key.PlaintextKey)
	if status != http.StatusOK {
		t.Fatalf("API key auth regressed: got %d", status)
	}
	if wa != "" {
		t.Errorf("API-key path must not emit WWW-Authenticate: got %q", wa)
	}
}

// TestBearer_APIKey_Bad: a bad API key 401s without the OAuth
// challenge. Distinguishing OAuth-bearer failure from API-key
// failure is the point of the WWW-Authenticate header.
func TestBearer_APIKey_Bad(t *testing.T) {
	f := newConsentFixture(t)
	status, wa := callAPIWithBearer(t, f.server.URL, "e2a_definitely_not_a_real_key")
	if status != http.StatusUnauthorized {
		t.Errorf("bad API key should 401: got %d", status)
	}
	if wa != "" {
		t.Errorf("API-key path must not emit WWW-Authenticate even on failure: got %q", wa)
	}
}

// TestBearer_NoAuth: no Authorization header and no session cookie
// returns 401 without any challenge (fallback path).
func TestBearer_NoAuth(t *testing.T) {
	f := newConsentFixture(t)
	status, wa := callAPIWithBearer(t, f.server.URL, "")
	if status != http.StatusUnauthorized {
		t.Errorf("no-auth should 401: got %d", status)
	}
	if wa != "" {
		t.Errorf("no-auth path should not emit OAuth challenge: got %q", wa)
	}
}

// TestBearer_OAuthToken_ProviderNotWired: if a deployment never calls
// SetOAuthProvider, ate2a_-prefixed tokens fail closed at the
// dispatch with the OAuth WWW-Authenticate challenge (the bearer
// looks like an OAuth token; we tell the client to re-OAuth rather
// than fall through to the API-key path).
func TestBearer_OAuthToken_ProviderNotWired(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	// API with NO OAuth wiring.
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	status, wa := callAPIWithBearer(t, server.URL, oauth.AccessTokenPrefix+"whatever.x")
	if status != http.StatusUnauthorized {
		t.Errorf("ate2a_ bearer without provider should 401: got %d", status)
	}
	if wa == "" {
		t.Error("ate2a_ bearer without provider should still emit OAuth challenge (token looked like OAuth)")
	}
}

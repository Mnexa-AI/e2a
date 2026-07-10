package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/apiserver"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/ws"

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
	tokResp, err := http.Post(f.server.URL+"/oauth2/token",
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

// callAPIWithBearer hits /v1/account with the given Authorization
// value. Returns the status code and the WWW-Authenticate header.
// /v1/account is the right validity probe because it authenticates with
// requirePrincipal — i.e. it returns 200 for ANY valid credential
// regardless of scope (account OR agent), and 401 with the RFC 6750
// challenge for an invalid one. (It deliberately is NOT /v1/agents, which
// is account-scope-gated and would 403 a valid agent-scoped token —
// conflating credential validity with authorization.)
func callAPIWithBearer(t *testing.T, serverURL, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", serverURL+"/v1/account", nil)
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
//
// We deliberately do NOT assert the error_description distinguishes
// "revoked" from "unknown" — that distinction would be a token-
// existence oracle. Revoked-and-unknown collapse to the same
// "invalid" description by design (see writeAuthError docstring).
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
	// Existence-oracle guard: revoked tokens and unknown tokens must
	// produce identical error_description ("the access token is
	// invalid"). The expired case below is the only exception.
	if !strings.Contains(wa, `error_description="the access token is invalid"`) {
		t.Errorf("revoked WWW-Authenticate should use generic invalid description: got %q", wa)
	}
}

// TestBearer_OAuthToken_Expired asserts that fosite's typed
// ErrTokenExpired surfaces as error_description="...has expired".
// Unlike revoked, "expired" is safe to distinguish — the signal
// comes from the HMAC strategy's expiry check, not from storage,
// so it doesn't reveal whether a token ever existed.
//
// To force a clearly-expired token we drive the storage directly:
// insert a row whose expires_at is in the past, then call the API
// with a bearer whose signature matches that row. This exercises
// fosite's expiry-check branch end-to-end.
func TestBearer_OAuthToken_Expired(t *testing.T) {
	f := newConsentFixture(t)
	access, _ := mintTokensForFixture(t, f)

	// Backdate the session's persisted ExpiresAt map. The strategy
	// reads expiry from the HYDRATED session, not from the column —
	// the session is stored as JSONB inside the request column. The
	// map is keyed by fosite.TokenType ("access_token"), so the
	// jsonb_set path is {session,expires_at,access_token}.
	if _, err := f.pool.Exec(context.Background(), `
		UPDATE oauth_access_tokens
		SET expires_at = NOW() - INTERVAL '1 hour',
		    request = jsonb_set(
		        request,
		        '{session,expires_at,access_token}',
		        '"2020-01-01T00:00:00Z"'::jsonb
		    )
		WHERE user_id = $1
	`, f.userID); err != nil {
		t.Fatal(err)
	}

	status, wa := callAPIWithBearer(t, f.server.URL, access)
	if status != http.StatusUnauthorized {
		t.Fatalf("expired OAuth token should 401: got %d", status)
	}
	if !strings.Contains(wa, `error="invalid_token"`) {
		t.Errorf(`WWW-Authenticate should contain error="invalid_token": got %q`, wa)
	}
	if !strings.Contains(wa, "expired") {
		t.Errorf("expired-token WWW-Authenticate should mention 'expired': got %q", wa)
	}
}

// TestBearer_OAuthToken_LowercaseBearer covers RFC 6750 §2.1 — the
// Bearer scheme name is case-insensitive. A client that sends
// `Authorization: bearer ate2a_…` must still authenticate. A
// case-sensitive TrimPrefix would have routed this to the API-key
// path and produced a misleading 401 without the OAuth challenge.
func TestBearer_OAuthToken_LowercaseBearer(t *testing.T) {
	f := newConsentFixture(t)
	access, _ := mintTokensForFixture(t, f)

	// /v1/account (requirePrincipal) is the validity probe — 200 for any valid
	// scope. (Not /v1/agents, which is account-gated and would 403 this valid
	// agent-scoped token, conflating scheme-parsing with authorization.)
	req, _ := http.NewRequest("GET", f.server.URL+"/v1/account", nil)
	req.Header.Set("Authorization", "bearer "+access) // lowercase scheme
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("lowercase-bearer should still authenticate (RFC 6750 §2.1): got %d", resp.StatusCode)
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
	key, err := store.CreateAPIKey(ctx, user.ID, "test-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	status, wa := callAPIWithBearer(t, f.server.URL, key.PlaintextKey)
	if status != http.StatusOK {
		t.Fatalf("API key auth regressed: got %d", status)
	}
	if wa != "" {
		t.Errorf("API-key path on a successful 200 must not emit WWW-Authenticate: got %q", wa)
	}
}

// TestBearer_APIKey_Bad: a bad API key 401s with the BARE Bearer
// challenge (no error param). RFC 6750 §3 says any 401 on an
// endpoint that accepts Bearer must advertise the scheme; but only
// OAuth-bearer failures get the `error="invalid_token"` extension
// per §3.1. This distinguishes "your API key is bad" (bare Bearer,
// no further action info) from "your OAuth token is bad — re-auth"
// (error=invalid_token).
func TestBearer_APIKey_Bad(t *testing.T) {
	f := newConsentFixture(t)
	status, wa := callAPIWithBearer(t, f.server.URL, "e2a_definitely_not_a_real_key")
	if status != http.StatusUnauthorized {
		t.Errorf("bad API key should 401: got %d", status)
	}
	if wa == "" {
		t.Error("401 must advertise the Bearer scheme per RFC 6750 §3")
	}
	if strings.Contains(wa, `error="invalid_token"`) {
		t.Errorf("API-key failure must not carry OAuth error params: got %q", wa)
	}
}

// TestBearer_NoAuth: no Authorization header and no session cookie
// returns 401 with the BARE Bearer challenge (no error param). The
// challenge tells the client our auth scheme; the absence of an
// `error` param distinguishes this from a failed-OAuth response.
func TestBearer_NoAuth(t *testing.T) {
	f := newConsentFixture(t)
	status, wa := callAPIWithBearer(t, f.server.URL, "")
	if status != http.StatusUnauthorized {
		t.Errorf("no-auth should 401: got %d", status)
	}
	if wa == "" {
		t.Error("401 must advertise the Bearer scheme per RFC 6750 §3 even with no credentials")
	}
	if strings.Contains(wa, `error="invalid_token"`) {
		t.Errorf("no-credentials path must not carry OAuth error params: got %q", wa)
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

	// Serve /v1/agents through apiserver (same as the consent fixture) but
	// WITHOUT calling SetOAuthProvider — that omission is the point of the
	// test: an ate2a_ bearer must fail closed at dispatch.
	usageStore := usage.NewStore(pool)
	enforcer := limits.NewEnforcer(limits.NewStore(pool), usageStore, limits.Defaults{
		PlanCode: "test", MaxAgents: 100000, MaxDomains: 100000,
		MaxMessagesMonth: 100000, MaxStorageBytes: 1 << 40,
	}, time.Minute)
	subscriberStore := webhook.NewSubscriberStore(pool)
	idempotencyStore := idempotency.NewStore(pool)
	api.SetIdempotencyStore(idempotencyStore)
	api.SetSubscriberStore(subscriberStore)
	api.SetEnforcer(enforcer)
	api.SetUsageStore(usageStore)
	wsHub := ws.NewHub()
	wsHandler := ws.NewHandler(wsHub, store)
	defer wsHub.Close()

	v1 := apiserver.New(apiserver.Params{
		API: api, Store: store, Enforcer: enforcer, UsageStore: usageStore,
		SubscriberStore: subscriberStore, Idempotency: idempotencyStore, Pool: pool,
		SMTPDomain: "test.e2a.dev", SharedDomain: "agents.e2a.dev",
		PublicURL: "https://test.e2a.dev", Production: false,
		Legacy: router, WSHandle: wsHandler.ServeWithEmail,
	})
	server := httptest.NewServer(v1)
	defer server.Close()

	status, wa := callAPIWithBearer(t, server.URL, oauth.AccessTokenPrefix+"whatever.x")
	if status != http.StatusUnauthorized {
		t.Errorf("ate2a_ bearer without provider should 401: got %d", status)
	}
	if wa == "" {
		t.Error("ate2a_ bearer without provider should still emit OAuth challenge (token looked like OAuth)")
	}
}

// ──────────────────── OAuth scope resolution (CRITICAL-1) ────────────────────

// getWithBearer GETs an arbitrary path with a bearer; returns status + body.
func getWithBearer(t *testing.T, serverURL, path, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", serverURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestBearer_OAuth_AgentScope_RejectedOnAccountOp is THE CRITICAL-1 regression.
// An OAuth/MCP access token that the consent flow granted as scope=agent
// must NOT be able to perform an
// account-admin operation. Before the fix, every ate2a_ token was hardcoded to
// ScopeAccount, so this returned 200 (full account admin); the granted agent
// scope was silently elevated. Now the scope is honored and the account-only
// GET /v1/agents (requireAccountScope) returns 403 forbidden.
func TestBearer_OAuth_AgentScope_RejectedOnAccountOp(t *testing.T) {
	f := newConsentFixture(t)
	access, _ := mintTokensForFixture(t, f) // scope=agent, bound to the consent agent
	if !strings.HasPrefix(access, oauth.AccessTokenPrefix) {
		t.Fatalf("expected ate2a_ token, got %q", access)
	}

	status, body := getWithBearer(t, f.server.URL, "/v1/agents", access)
	if status != http.StatusForbidden {
		t.Fatalf("agent-scoped OAuth token must be 403 on an account-admin op (CRITICAL-1); got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "forbidden") {
		t.Errorf("expected error.code=forbidden in the 403 body, got %s", body)
	}
}

// TestBearer_OAuth_AgentScope_AllowedOnOwnTier confirms the fix CONFINES rather
// than breaks: the same agent-scoped token authenticates on GET /v1/account
// (requirePrincipal — valid for any scope), returning 200. The agent token
// works for its own runtime tier; it's only barred from account administration.
func TestBearer_OAuth_AgentScope_AllowedOnOwnTier(t *testing.T) {
	f := newConsentFixture(t)
	access, _ := mintTokensForFixture(t, f)
	status, _ := callAPIWithBearer(t, f.server.URL, access) // GET /v1/account
	if status != http.StatusOK {
		t.Fatalf("agent-scoped OAuth token should authenticate on its own tier (/v1/account); got %d", status)
	}
}

// seedOAuthClient inserts a public PKCE OAuth client registered with the given
// scopes. Seeding directly lets us pin an arbitrary registered ceiling
// (e.g. account-only, or a retired/unrecognized scope) without driving the
// consent UI — this is how we simulate the console/confidential issuance
// path that the design reserves for account scope, and how we construct a
// token carrying a retired/unrecognized scope.
func seedOAuthClient(t *testing.T, f *consentFixture, clientID string, scopes []string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types,
		     response_types, scopes, audiences, token_endpoint_auth_method,
		     public, created_via)
		VALUES ($1, 'scoped test client',
		        ARRAY['http://localhost:8765/callback'],
		        ARRAY['authorization_code','refresh_token'], ARRAY['code'],
		        $2, ARRAY[]::TEXT[], 'none', TRUE, 'dcr')
		ON CONFLICT (client_id) DO NOTHING
	`, clientID, scopes); err != nil {
		t.Fatalf("seedOAuthClient(%s): %v", clientID, err)
	}
}

// mintAccessWithScope runs the authorize→consent→token flow for an explicit
// client + consent-grantable scope (agent|account). The consent screen is the
// scope authority, so it drives the grant via scope_choice (not the requested
// `scope` param). Returns the issued access token. To mint a token carrying a
// scope the consent screen does NOT offer (a retired/unknown scope), use
// mintAuthCode + a token exchange, which bypasses the consent gate.
func mintAccessWithScope(t *testing.T, f *consentFixture, clientID, scope string) string {
	t.Helper()
	verifier, challenge := newPKCE(t)

	form := authorizeParams(challenge, clientID, "s2s2s2s2s2s2s2s2")
	form.Set("scope", scope) // override the fixture's default "agent"
	form.Set("scope_choice", scope)
	form.Set("action", "allow")
	form.Set("agent_choice", "create_new")
	form.Set("new_agent_slug", "scoped-"+randHex8(t))
	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("mintAccessWithScope: no code in redirect: %s", resp.Header.Get("Location"))
	}

	tokForm := url.Values{}
	tokForm.Set("grant_type", "authorization_code")
	tokForm.Set("code", code)
	tokForm.Set("client_id", clientID)
	tokForm.Set("redirect_uri", "http://localhost:8765/callback")
	tokForm.Set("code_verifier", verifier)
	tokResp, err := http.Post(f.server.URL+"/oauth2/token",
		"application/x-www-form-urlencoded", strings.NewReader(tokForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer tokResp.Body.Close()
	if tokResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(tokResp.Body)
		t.Fatalf("mintAccessWithScope: /token status=%d body=%s", tokResp.StatusCode, b)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.AccessToken
}

// TestBearer_OAuth_AccountScope_AllowedOnAccountOp exercises the account branch
// of the scope switch: a token granted `account` (console/confidential
// issuance) resolves to ScopeAccount and CAN perform an account-admin op. The
// counterpart to the agent-confinement regression — proves the fix grants
// account power only to genuinely account-granted tokens.
func TestBearer_OAuth_AccountScope_AllowedOnAccountOp(t *testing.T) {
	f := newConsentFixture(t)
	seedOAuthClient(t, f, "acct_client", []string{"account"})
	access := mintAccessWithScope(t, f, "acct_client", "account")

	// GET /v1/agents is account-scope-gated (requireAccountScope).
	status, body := getWithBearer(t, f.server.URL, "/v1/agents", access)
	if status != http.StatusOK {
		t.Fatalf("account-scoped OAuth token should pass an account op, got %d (body=%s)", status, body)
	}
}

// TestBearer_OAuth_UnknownScope_Rejected exercises the fail-closed default: a
// token carrying a retired/unrecognized scope (legacy `mcp`) must be REJECTED
// (401), never silently elevated. Guards against the retired scope quietly
// regaining access if the switch's default branch ever regresses.
func TestBearer_OAuth_UnknownScope_Rejected(t *testing.T) {
	f := newConsentFixture(t)
	seedOAuthClient(t, f, "legacy_mcp_client", []string{"mcp"})
	// The consent screen no longer grants anything but agent/account, so mint
	// the unrecognized-scope token the only way one could still exist: directly
	// via the provider (mintAuthCode grants "mcp"), bypassing the consent gate.
	// This isolates the principal resolver's fail-closed default — a legacy or
	// out-of-band token carrying a retired scope must still be rejected.
	redirectURI := "http://localhost:8765/callback"
	verifier, challenge := newPKCE(t)
	code := mintAuthCode(t, f.provider, "legacy_mcp_client", f.userID, redirectURI, challenge)
	tokForm := url.Values{}
	tokForm.Set("grant_type", "authorization_code")
	tokForm.Set("code", code)
	tokForm.Set("client_id", "legacy_mcp_client")
	tokForm.Set("redirect_uri", redirectURI)
	tokForm.Set("code_verifier", verifier)
	tokResp, err := http.Post(f.server.URL+"/oauth2/token", "application/x-www-form-urlencoded", strings.NewReader(tokForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer tokResp.Body.Close()
	if tokResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(tokResp.Body)
		t.Fatalf("mint mcp token: /token status=%d body=%s", tokResp.StatusCode, b)
	}
	var tb struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(tokResp.Body).Decode(&tb)
	access := tb.AccessToken

	status, wa := callAPIWithBearer(t, f.server.URL, access) // GET /v1/account
	if status != http.StatusUnauthorized {
		t.Fatalf("unrecognized-scope (mcp) OAuth token must be rejected (401), got %d", status)
	}
	if !strings.Contains(wa, `error="invalid_token"`) {
		t.Errorf(`expected OAuth challenge error="invalid_token" for a rejected token, got %q`, wa)
	}
}

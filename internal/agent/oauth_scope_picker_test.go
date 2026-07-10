package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// exchangeCode swaps an authorization code for a token pair via /oauth2/token.
func (f *consentFixture) exchangeCode(t *testing.T, code, verifier, redirectURI string) (accessToken, refreshToken, scope string) {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", f.clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	resp, err := http.Post(f.server.URL+"/oauth2/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token exchange status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.AccessToken, body.RefreshToken, body.Scope
}

// whoami calls GET /v1/account with the bearer and returns (scope, agentAddress).
func (f *consentFixture) whoami(t *testing.T, bearer string) (scope, agentAddress string) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.server.URL+"/v1/account", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/v1/account status = %d, want 200 (bearer should resolve)", resp.StatusCode)
	}
	var body struct {
		Scope        string `json:"scope"`
		AgentAddress string `json:"agent_address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Scope, body.AgentAddress
}

// TestHTTP_Consent_Account_Loopback grants account scope when the user picks it
// on a loopback client, and the resulting token resolves to an account
// principal (no bound agent) — exactly like an e2a_acct_ key.
func TestHTTP_Consent_Account_Loopback(t *testing.T) {
	f := newConsentFixture(t)
	verifier, challenge := newPKCE(t)
	redirectURI := "http://localhost:8765/callback"

	form := authorizeParams(challenge, f.clientID, "s1s1s1s1s1s1s1s1")
	form.Set("action", "allow")
	form.Set("scope_choice", "account")
	// No agent_choice — account isn't inbox-bound.

	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (account on loopback is allowed)", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("expected code in redirect, got error=%q", loc.Query().Get("error"))
	}

	access, _, scope := f.exchangeCode(t, code, verifier, redirectURI)
	if !strings.Contains(scope, "account") {
		t.Errorf("token scope = %q, want it to contain account", scope)
	}
	gotScope, agentAddr := f.whoami(t, access)
	if gotScope != "account" {
		t.Errorf("whoami scope = %q, want account", gotScope)
	}
	if agentAddr != "" {
		t.Errorf("account principal must have no bound agent; got agent_address=%q", agentAddr)
	}
}

// TestHTTP_Consent_Account_CeilingLacksAccount_Rejected — defense-in-depth. An
// https redirect is now account-ELIGIBLE at the handler gate (see
// accountEligibleRedirect), but a client whose REGISTERED ceiling is agent-only
// is still rejected by fosite's ExactScopeStrategy (granted ⊆ registered). No
// code is issued.
func TestHTTP_Consent_Account_CeilingLacksAccount_Rejected(t *testing.T) {
	f := newConsentFixture(t)
	ctx := context.Background()
	// Seed a public client registered with an https (non-loopback) redirect.
	httpsClient := "mcp_hosted_test"
	httpsRedirect := "https://app.example.com/callback"
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types, response_types,
		     scopes, audiences, token_endpoint_auth_method, public, created_via)
		VALUES ($1, 'hosted test client', ARRAY[$2],
		        ARRAY['authorization_code','refresh_token'], ARRAY['code'],
		        ARRAY['agent'], ARRAY[]::TEXT[], 'none', TRUE, 'dcr')
		ON CONFLICT (client_id) DO NOTHING`, httpsClient, httpsRedirect); err != nil {
		t.Fatalf("seed https client: %v", err)
	}

	_, challenge := newPKCE(t)
	form := authorizeParams(challenge, httpsClient, "s1s1s1s1s1s1s1s1")
	form.Set("redirect_uri", httpsRedirect)
	form.Set("action", "allow")
	form.Set("scope_choice", "account")

	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	// fosite redirects errors to the (validated) redirect_uri with error params.
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("code") != "" {
		t.Fatalf("account must be rejected on a non-loopback redirect, but a code was issued")
	}
	if e := loc.Query().Get("error"); e != "invalid_scope" {
		t.Errorf("error = %q, want invalid_scope", e)
	}
}

// TestHTTP_Consent_Account_Https_Allowed proves the 2026-07-10 policy: a hosted
// (https) client whose ceiling includes account can now be granted account
// scope through consent. The loopback-only gate was intentionally relaxed so
// Claude Chat/Cowork (redirect https://claude.ai/api/mcp/auth_callback) can hold
// workspace-admin. The issued token resolves to an account principal.
func TestHTTP_Consent_Account_Https_Allowed(t *testing.T) {
	f := newConsentFixture(t)
	ctx := context.Background()
	clientID := "mcp_hosted_allowed"
	redirectURI := "https://claude.ai/api/mcp/auth_callback"
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types, response_types,
		     scopes, audiences, token_endpoint_auth_method, public, created_via)
		VALUES ($1, 'hosted allowed client', ARRAY[$2],
		        ARRAY['authorization_code','refresh_token'], ARRAY['code'],
		        ARRAY['agent','account'], ARRAY[]::TEXT[], 'none', TRUE, 'dcr')
		ON CONFLICT (client_id) DO NOTHING`, clientID, redirectURI); err != nil {
		t.Fatalf("seed hosted client: %v", err)
	}
	// exchangeCode() presents f.clientID at the token endpoint; the code is
	// bound to the seeded hosted client, so point the fixture at it.
	f.clientID = clientID

	verifier, challenge := newPKCE(t)
	form := authorizeParams(challenge, clientID, "s1s1s1s1s1s1s1s1")
	form.Set("redirect_uri", redirectURI)
	form.Set("action", "allow")
	form.Set("scope_choice", "account")

	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		loc, _ := url.Parse(resp.Header.Get("Location"))
		t.Fatalf("status = %d, want 303 (account on https is now allowed); error=%q",
			resp.StatusCode, loc.Query().Get("error"))
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("expected code, got error=%q", loc.Query().Get("error"))
	}
	access, _, scope := f.exchangeCode(t, code, verifier, redirectURI)
	if !strings.Contains(scope, "account") {
		t.Errorf("token scope = %q, want it to contain account", scope)
	}
	gotScope, agentAddr := f.whoami(t, access)
	if gotScope != "account" {
		t.Errorf("whoami scope = %q, want account", gotScope)
	}
	if agentAddr != "" {
		t.Errorf("account principal must have no bound agent; got agent_address=%q", agentAddr)
	}
}

// TestHTTP_Consent_Account_CustomScheme_RejectedByHandlerGate pins the consent
// handler's OWN fail-closed gate (accountEligibleRedirect), independent of
// fosite's ceiling check. The client is seeded WITH account in its ceiling and
// a reverse-domain custom-scheme redirect — which passes validateRedirectURI
// but is NOT account-eligible — so the handler gate rejects the account grant
// before fosite is even reached. (Post-relaxation the renamed https "rejected"
// test only reaches the fosite layer, so this restores direct coverage of the
// handler gate's reject branch.)
func TestHTTP_Consent_Account_CustomScheme_RejectedByHandlerGate(t *testing.T) {
	f := newConsentFixture(t)
	ctx := context.Background()
	clientID := "mcp_customscheme"
	redirectURI := "com.example.app:/oauth-callback"
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types, response_types,
		     scopes, audiences, token_endpoint_auth_method, public, created_via)
		VALUES ($1, 'custom scheme client', ARRAY[$2],
		        ARRAY['authorization_code','refresh_token'], ARRAY['code'],
		        ARRAY['agent','account'], ARRAY[]::TEXT[], 'none', TRUE, 'dcr')
		ON CONFLICT (client_id) DO NOTHING`, clientID, redirectURI); err != nil {
		t.Fatalf("seed custom-scheme client: %v", err)
	}

	_, challenge := newPKCE(t)
	form := authorizeParams(challenge, clientID, "s1s1s1s1s1s1s1s1")
	form.Set("redirect_uri", redirectURI)
	form.Set("action", "allow")
	form.Set("scope_choice", "account")

	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("code") != "" {
		t.Fatalf("account must be rejected on a custom-scheme redirect")
	}
	if e := loc.Query().Get("error"); e != "invalid_scope" {
		t.Errorf("error = %q, want invalid_scope", e)
	}
}

// TestHTTP_Consent_Account_MixedRedirect_RejectedByScopeCeiling proves the
// fosite scope-ceiling defense independent of the handler gate: this client is
// seeded with an agent-only ceiling and its inbound (loopback) redirect passes
// the handler gate, yet fosite's ExactScopeStrategy still rejects the account
// grant because account is not on the client row. (Under the 2026-07-10 policy a
// loopback+https client IS account-eligible at DCR, so the agent-only ceiling
// here is a deliberately seeded fixture, not what fresh DCR would write.)
func TestHTTP_Consent_Account_MixedRedirect_RejectedByScopeCeiling(t *testing.T) {
	f := newConsentFixture(t)
	ctx := context.Background()
	mixedClient := "mcp_mixed_test"
	loopbackRedirect := "http://localhost:8765/callback"
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types, response_types,
		     scopes, audiences, token_endpoint_auth_method, public, created_via)
		VALUES ($1, 'mixed redirect client', ARRAY[$2,$3],
		        ARRAY['authorization_code','refresh_token'], ARRAY['code'],
		        ARRAY['agent'], ARRAY[]::TEXT[], 'none', TRUE, 'dcr')
		ON CONFLICT (client_id) DO NOTHING`,
		mixedClient, loopbackRedirect, "https://app.example.com/callback"); err != nil {
		t.Fatalf("seed mixed client: %v", err)
	}

	_, challenge := newPKCE(t)
	form := authorizeParams(challenge, mixedClient, "s1s1s1s1s1s1s1s1")
	form.Set("redirect_uri", loopbackRedirect) // inbound redirect IS loopback → passes the handler gate
	form.Set("action", "allow")
	form.Set("scope_choice", "account")

	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("code") != "" {
		t.Fatalf("mixed-redirect client must not get account even on the loopback redirect")
	}
	if e := loc.Query().Get("error"); e != "invalid_scope" {
		t.Errorf("error = %q, want invalid_scope (fosite scope-ceiling rejection)", e)
	}
}

// TestHTTP_Consent_DefaultScope_IsAgent — a consent POST that omits scope_choice
// defaults to agent (the safe default) and binds to the chosen inbox.
func TestHTTP_Consent_DefaultScope_IsAgent(t *testing.T) {
	f := newConsentFixture(t)
	verifier, challenge := newPKCE(t)
	redirectURI := "http://localhost:8765/callback"

	form := authorizeParams(challenge, f.clientID, "s1s1s1s1s1s1s1s1")
	form.Set("action", "allow")
	form.Set("agent_choice", "create_new")
	form.Set("new_agent_slug", "defaultscopebot")
	// scope_choice intentionally omitted.

	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("expected code, got error=%q", loc.Query().Get("error"))
	}
	access, _, scope := f.exchangeCode(t, code, verifier, redirectURI)
	if scope != "agent" {
		t.Errorf("default token scope = %q, want agent", scope)
	}
	gotScope, agentAddr := f.whoami(t, access)
	if gotScope != "agent" {
		t.Errorf("whoami scope = %q, want agent", gotScope)
	}
	if agentAddr == "" {
		t.Error("agent principal must carry a bound agent_address")
	}
}

// TestHTTP_Consent_Account_RefreshKeepsScope — refreshing an account token keeps
// account scope; it must never silently change tier.
func TestHTTP_Consent_Account_RefreshKeepsScope(t *testing.T) {
	f := newConsentFixture(t)
	verifier, challenge := newPKCE(t)
	redirectURI := "http://localhost:8765/callback"

	form := authorizeParams(challenge, f.clientID, "s1s1s1s1s1s1s1s1")
	form.Set("action", "allow")
	form.Set("scope_choice", "account")
	resp := f.consentPOST(t, form)
	defer resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("expected code, got error=%q", loc.Query().Get("error"))
	}
	_, refresh, _ := f.exchangeCode(t, code, verifier, redirectURI)

	// Refresh exchange.
	rf := url.Values{}
	rf.Set("grant_type", "refresh_token")
	rf.Set("refresh_token", refresh)
	rf.Set("client_id", f.clientID)
	r2, err := http.Post(f.server.URL+"/oauth2/token", "application/x-www-form-urlencoded", strings.NewReader(rf.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", r2.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	json.NewDecoder(r2.Body).Decode(&body)
	if !strings.Contains(body.Scope, "account") {
		t.Errorf("refreshed token scope = %q, want account preserved", body.Scope)
	}
	if s, _ := f.whoami(t, body.AccessToken); s != "account" {
		t.Errorf("refreshed token whoami scope = %q, want account", s)
	}
}

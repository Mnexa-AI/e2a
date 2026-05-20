package agent_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// newDiscoveryServer builds a minimal API server for discovery tests.
// Variants control whether OAuth is enabled and what publicURL is set
// to — those are the two knobs that gate the endpoint's behavior.
func newDiscoveryServer(t *testing.T, oauthEnabled bool, publicURL string) *httptest.Server {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", publicURL, false)
	if oauthEnabled {
		api.SetOAuthStore(oauth.NewStore(pool))
	}
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

func TestOAuthDiscovery_Enabled(t *testing.T) {
	server := newDiscoveryServer(t, true, "https://e2a.dev")

	resp, err := http.Get(server.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("want application/json content-type, got %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc == "" {
		t.Error("expected Cache-Control header to be set (clients should cache discovery)")
	}

	var meta agent.OAuthAuthorizationServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Issuer + endpoint URLs must be absolute and derived from publicURL.
	cases := map[string]string{
		"issuer":                 meta.Issuer,
		"authorization_endpoint": meta.AuthorizationEndpoint,
		"token_endpoint":         meta.TokenEndpoint,
		"registration_endpoint":  meta.RegistrationEndpoint,
		"revocation_endpoint":    meta.RevocationEndpoint,
	}
	for field, val := range cases {
		if !strings.HasPrefix(val, "https://e2a.dev") {
			t.Errorf("%s should be absolute under publicURL: got %q", field, val)
		}
	}
	if meta.Issuer != "https://e2a.dev" {
		t.Errorf("issuer must equal publicURL exactly (no trailing slash, no path): got %q", meta.Issuer)
	}

	// Capability lists must match what the server actually implements.
	// Regression-grade: if someone adds plaintext PKCE without updating
	// this list, clients will silently negotiate the weaker method.
	wantResponseTypes := []string{"code"}
	if !equalStringSlice(meta.ResponseTypesSupported, wantResponseTypes) {
		t.Errorf("response_types_supported: want %v, got %v", wantResponseTypes, meta.ResponseTypesSupported)
	}
	wantGrants := []string{"authorization_code", "refresh_token"}
	if !equalStringSlice(meta.GrantTypesSupported, wantGrants) {
		t.Errorf("grant_types_supported: want %v, got %v", wantGrants, meta.GrantTypesSupported)
	}
	wantPKCE := []string{"S256"}
	if !equalStringSlice(meta.CodeChallengeMethodsSupported, wantPKCE) {
		t.Errorf("code_challenge_methods_supported: want %v (S256 only), got %v", wantPKCE, meta.CodeChallengeMethodsSupported)
	}
	wantScopes := []string{"e2a"}
	if !equalStringSlice(meta.ScopesSupported, wantScopes) {
		t.Errorf("scopes_supported: want %v, got %v", wantScopes, meta.ScopesSupported)
	}
	// v0.3 supports public clients only (PKCE). Asserting "none" only
	// keeps discovery and DCR consistent — if a future slice adds
	// confidential support, both files must change together.
	wantAuthMethods := []string{"none"}
	if !equalStringSlice(meta.TokenEndpointAuthMethodsSupported, wantAuthMethods) {
		t.Errorf("token_endpoint_auth_methods_supported: want %v, got %v", wantAuthMethods, meta.TokenEndpointAuthMethodsSupported)
	}
	// L7: revocation_endpoint_auth_methods_supported must also be
	// advertised so RFC 8414 §2 strict clients don't warn on missing
	// metadata for the revoke endpoint.
	if !equalStringSlice(meta.RevocationEndpointAuthMethodsSupported, wantAuthMethods) {
		t.Errorf("revocation_endpoint_auth_methods_supported: want %v, got %v", wantAuthMethods, meta.RevocationEndpointAuthMethodsSupported)
	}
	// RFC 9207 §3: advertising support tells clients to expect an
	// `iss` parameter on the authorization response. Mix-up-aware
	// clients (incl. MCP clients) use this to decide whether to
	// enforce the check.
	if !meta.AuthorizationResponseIssParameterSupported {
		t.Error("authorization_response_iss_parameter_supported should be advertised true (RFC 9207)")
	}
}

// TestOAuthDiscovery_NoOAuthStore_404 covers a deployment that hasn't
// wired in oauth.Store. Hiding the document (rather than returning a
// half-populated one) signals to clients that OAuth genuinely isn't
// available — they'll fall back to API-key auth instead of looping on
// a broken discovery doc.
func TestOAuthDiscovery_NoOAuthStore_404(t *testing.T) {
	server := newDiscoveryServer(t, false, "https://e2a.dev")
	resp, err := http.Get(server.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("OAuth not configured should 404: got %d", resp.StatusCode)
	}
}

// TestOAuthDiscovery_NoPublicURL_404 covers the misconfigured-operator
// case: oauth.Store is wired but http.public_url is empty. Returning a
// document with empty or request-derived URLs would let an attacker
// behind a proxy spoof the issuer; better to fail loudly.
func TestOAuthDiscovery_NoPublicURL_404(t *testing.T) {
	server := newDiscoveryServer(t, true, "")
	resp, err := http.Get(server.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing publicURL should 404: got %d", resp.StatusCode)
	}
}

// TestOAuthDiscovery_TrailingSlashNormalized guards against the
// publicURL having a trailing slash, which would otherwise produce
// "https://e2a.dev//api/oauth/token" — most clients tolerate it, but
// RFC 8414 says the issuer URL MUST NOT have a trailing slash for
// issuer-identifier matching to work.
func TestOAuthDiscovery_TrailingSlashNormalized(t *testing.T) {
	server := newDiscoveryServer(t, true, "https://e2a.dev/")
	resp, err := http.Get(server.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var meta agent.OAuthAuthorizationServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.Issuer != "https://e2a.dev" {
		t.Errorf("issuer must not have trailing slash: got %q", meta.Issuer)
	}
	if strings.Contains(meta.TokenEndpoint, "//api") {
		t.Errorf("endpoints should not have doubled slashes: %q", meta.TokenEndpoint)
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

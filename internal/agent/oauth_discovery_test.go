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

// TestHTTP_Discovery_Happy: full success path. Verifies the response
// shape against what real OAuth clients (Cursor, MCP Inspector) read.
func TestHTTP_Discovery_Happy(t *testing.T) {
	f := newConsentFixture(t) // includes OAuth wiring with publicURL=https://test.e2a.dev
	resp, err := http.Get(f.server.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
		t.Errorf("Cache-Control should advertise caching: got %q", cc)
	}

	var meta agent.OAuthMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.Issuer != "https://test.e2a.dev" {
		t.Errorf("issuer = %q, want https://test.e2a.dev", meta.Issuer)
	}
	if meta.AuthorizationEndpoint != "https://test.e2a.dev/api/oauth/authorize" {
		t.Errorf("authorization_endpoint = %q", meta.AuthorizationEndpoint)
	}
	if meta.TokenEndpoint != "https://test.e2a.dev/api/oauth/token" {
		t.Errorf("token_endpoint = %q", meta.TokenEndpoint)
	}
	if meta.RegistrationEndpoint != "https://test.e2a.dev/api/oauth/register" {
		t.Errorf("registration_endpoint = %q", meta.RegistrationEndpoint)
	}
	if meta.RevocationEndpoint != "https://test.e2a.dev/api/oauth/revoke" {
		t.Errorf("revocation_endpoint = %q", meta.RevocationEndpoint)
	}
	// Capability lists must match what the server actually implements
	// (regression-grade — if any of these drifts vs the compose call
	// in NewProvider, the discovery doc lies to clients).
	if len(meta.ResponseTypesSupported) != 1 || meta.ResponseTypesSupported[0] != "code" {
		t.Errorf("response_types_supported = %v, want [code]", meta.ResponseTypesSupported)
	}
	wantGrants := map[string]bool{"authorization_code": true, "refresh_token": true}
	for _, gt := range meta.GrantTypesSupported {
		delete(wantGrants, gt)
	}
	if len(wantGrants) != 0 {
		t.Errorf("grant_types_supported missing entries: %v", wantGrants)
	}
	if len(meta.CodeChallengeMethodsSupported) != 1 || meta.CodeChallengeMethodsSupported[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [S256]", meta.CodeChallengeMethodsSupported)
	}
	if !meta.AuthorizationResponseIssParameterSupported {
		t.Error("authorization_response_iss_parameter_supported should be true (RFC 9207)")
	}
}

// TestHTTP_Discovery_TrailingSlashStripped: a publicURL configured
// with a trailing slash must NOT produce double-slashed endpoint
// URLs. RFC 8414 §2 requires issuer be byte-for-byte stable.
func TestHTTP_Discovery_TrailingSlashStripped(t *testing.T) {
	srv := bareDiscoveryServer(t, "https://e2a.dev/")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var meta agent.OAuthMetadata
	json.NewDecoder(resp.Body).Decode(&meta)
	if meta.Issuer != "https://e2a.dev" {
		t.Errorf("issuer should strip trailing slash: got %q", meta.Issuer)
	}
	if strings.Contains(meta.TokenEndpoint, "//api/") {
		t.Errorf("token_endpoint has double slash: %q", meta.TokenEndpoint)
	}
}

// TestHTTP_Discovery_NoPublicURL_404: without publicURL we can't
// emit absolute URLs — fail loudly so operators notice the config gap.
func TestHTTP_Discovery_NoPublicURL_404(t *testing.T) {
	srv := bareDiscoveryServer(t, "")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("discovery without publicURL should 404: got %d", resp.StatusCode)
	}
}

// TestHTTP_Discovery_NotConfigured_404: same shape but for the
// SetOAuthProvider gate. Even with publicURL set, if OAuth isn't
// wired we 404 so the announced endpoints match actual behavior.
func TestHTTP_Discovery_NotConfigured_404(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	// No SetOAuthProvider call.
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("discovery without provider should 404: got %d", resp.StatusCode)
	}
}

// bareDiscoveryServer builds an httptest server with a provider wired
// at the given publicURL (which may be empty). Used by the discovery
// edge-case tests that need fine control over publicURL without
// pulling in the full consent fixture.
func bareDiscoveryServer(t *testing.T, publicURL string) *httptest.Server {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")

	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", publicURL, false)

	// Always wire the provider (even with empty publicURL — discovery
	// is gated separately on publicURL).
	storage := oauth.NewStorage(pool)
	secret := []byte("test-secret-test-secret-test-sec")
	provider, err := oauth.NewProvider(storage, publicURL, secret)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	api.SetOAuthProvider(provider)
	api.SetOAuthStorage(storage)

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	return srv
}

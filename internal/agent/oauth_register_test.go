package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// newDCRServer is a copy of newDiscoveryServer's setup tuned for DCR
// tests — we return both the server and the oauth store so tests can
// verify the row landed in the DB (not just the HTTP response shape).
func newDCRServer(t *testing.T) (*httptest.Server, *oauth.Store) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	oauthStore := oauth.NewStore(pool)
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(), "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://e2a.dev", false)
	api.SetOAuthStore(oauthStore)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, oauthStore
}

// postJSON sends a JSON body to the given path. Returns the response so
// the caller can assert on status + body shape.
func postJSON(t *testing.T, server *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestOAuthRegister_Success_DefaultsApplied(t *testing.T) {
	server, oauthStore := newDCRServer(t)

	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName:   "Test Client",
		RedirectURIs: []string{"https://example.com/callback"},
		// grant_types / response_types / token_endpoint_auth_method /
		// scope intentionally omitted — defaults should fill them in.
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("DCR responses should be no-store; got %q", cc)
	}

	var got agent.OAuthRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(got.ClientID, oauth.ClientIDPrefix) {
		t.Errorf("client_id should have %q prefix: got %q", oauth.ClientIDPrefix, got.ClientID)
	}
	if got.ClientIDIssuedAt == 0 {
		t.Error("client_id_issued_at must be a non-zero unix timestamp")
	}
	if got.ClientName != "Test Client" {
		t.Errorf("client_name echo: want %q, got %q", "Test Client", got.ClientName)
	}
	if len(got.RedirectURIs) != 1 || got.RedirectURIs[0] != "https://example.com/callback" {
		t.Errorf("redirect_uris echo wrong: got %v", got.RedirectURIs)
	}

	// Defaults applied:
	wantGrants := []string{"authorization_code", "refresh_token"}
	if !equalStringSlice(got.GrantTypes, wantGrants) {
		t.Errorf("grant_types default: want %v, got %v", wantGrants, got.GrantTypes)
	}
	if !equalStringSlice(got.ResponseTypes, []string{"code"}) {
		t.Errorf("response_types default: want [code], got %v", got.ResponseTypes)
	}
	if got.TokenEndpointAuthMethod != "none" {
		t.Errorf("token_endpoint_auth_method default: want none, got %q", got.TokenEndpointAuthMethod)
	}
	if got.Scope != "e2a" {
		t.Errorf("scope default: want e2a, got %q", got.Scope)
	}

	// Verify the row actually landed in the DB.
	row, err := oauthStore.GetClient(testCtx(), got.ClientID)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if row.ClientType != "public" {
		t.Errorf("DB row should be public client: got %q", row.ClientType)
	}
	if row.CreatedVia != "dcr" {
		t.Errorf("DB row created_via: want dcr, got %q", row.CreatedVia)
	}
	if row.ClientSecretHash != "" {
		t.Errorf("public client must not have a secret hash: got %q", row.ClientSecretHash)
	}
}

// TestOAuthRegister_NoOAuthStore_404 — symmetric with discovery: an
// operator who hasn't wired OAuth gets a clean 404, not a confusing
// 500 from a nil store deref.
func TestOAuthRegister_NoOAuthStore_404(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(), "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://e2a.dev", false)
	// no SetOAuthStore
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName:   "x",
		RedirectURIs: []string{"https://example.com/callback"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("OAuth not configured should 404: got %d", resp.StatusCode)
	}
}

func TestOAuthRegister_MissingClientName(t *testing.T) {
	server, _ := newDCRServer(t)
	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		RedirectURIs: []string{"https://example.com/callback"},
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

func TestOAuthRegister_MissingRedirectURIs(t *testing.T) {
	server, _ := newDCRServer(t)
	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName: "x",
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_redirect_uri")
}

// TestOAuthRegister_RedirectURI_VariousShapes table-drives the URI
// validator so a future change is visibly safe.
func TestOAuthRegister_RedirectURI_VariousShapes(t *testing.T) {
	server, _ := newDCRServer(t)
	cases := []struct {
		name        string
		uri         string
		wantStatus  int
		wantErrCode string
	}{
		{"https web", "https://example.com/callback", http.StatusCreated, ""},
		{"http loopback hostname", "http://localhost:8765/cb", http.StatusCreated, ""},
		{"http loopback ipv4", "http://127.0.0.1:8765/cb", http.StatusCreated, ""},
		{"http loopback ipv6", "http://[::1]:8765/cb", http.StatusCreated, ""},
		{"custom scheme native app", "myapp://oauth-callback", http.StatusCreated, ""},

		{"http non-loopback rejected", "http://example.com/cb", http.StatusBadRequest, "invalid_redirect_uri"},
		{"fragment rejected", "https://example.com/cb#frag", http.StatusBadRequest, "invalid_redirect_uri"},
		{"missing scheme rejected", "example.com/cb", http.StatusBadRequest, "invalid_redirect_uri"},
		{"empty string rejected", "", http.StatusBadRequest, "invalid_redirect_uri"},
		{"https without host rejected", "https:///cb", http.StatusBadRequest, "invalid_redirect_uri"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
				ClientName:   fmt.Sprintf("uri-test-%d", i),
				RedirectURIs: []string{c.uri},
			})
			defer resp.Body.Close()
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("uri=%q: want status %d, got %d", c.uri, c.wantStatus, resp.StatusCode)
			}
			if c.wantErrCode != "" {
				assertOAuthError(t, resp, c.wantStatus, c.wantErrCode)
			}
		})
	}
}

// TestOAuthRegister_RejectsConfidential — discovery advertises only
// "none" in token_endpoint_auth_methods_supported; this confirms DCR
// matches that contract. If a future slice adds confidential support,
// this test should be replaced (not deleted).
func TestOAuthRegister_RejectsConfidential(t *testing.T) {
	server, _ := newDCRServer(t)
	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName:              "confidential client",
		RedirectURIs:            []string{"https://example.com/cb"},
		TokenEndpointAuthMethod: "client_secret_basic",
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

func TestOAuthRegister_RejectsUnsupportedGrant(t *testing.T) {
	server, _ := newDCRServer(t)
	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName:   "x",
		RedirectURIs: []string{"https://example.com/cb"},
		GrantTypes:   []string{"client_credentials"},
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

func TestOAuthRegister_RejectsUnsupportedResponseType(t *testing.T) {
	server, _ := newDCRServer(t)
	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName:    "x",
		RedirectURIs:  []string{"https://example.com/cb"},
		ResponseTypes: []string{"token"}, // implicit flow — not supported
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

func TestOAuthRegister_RejectsUnsupportedScope(t *testing.T) {
	server, _ := newDCRServer(t)
	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName:   "x",
		RedirectURIs: []string{"https://example.com/cb"},
		Scope:        "admin",
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

func TestOAuthRegister_TooManyRedirectURIs(t *testing.T) {
	server, _ := newDCRServer(t)
	uris := make([]string, 11)
	for i := range uris {
		uris[i] = fmt.Sprintf("https://example.com/cb%d", i)
	}
	resp := postJSON(t, server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName:   "x",
		RedirectURIs: uris,
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_redirect_uri")
}

// TestOAuthRegister_InvalidJSON guards against panics on garbage input.
// readJSON rejects it before validation runs.
func TestOAuthRegister_InvalidJSON(t *testing.T) {
	server, _ := newDCRServer(t)
	resp, err := http.Post(server.URL+"/api/oauth/register", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

// assertOAuthError decodes the RFC 7591/6749 error body and asserts on
// status + error code. Centralized so each test reads as a one-liner.
func assertOAuthError(t *testing.T, resp *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Errorf("status: want %d, got %d", wantStatus, resp.StatusCode)
	}
	var e agent.OAuthError
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if e.Error != wantCode {
		t.Errorf("error code: want %q, got %q (description: %q)", wantCode, e.Error, e.ErrorDescription)
	}
}

// testCtx returns a background context — kept as a tiny helper because
// the test file is otherwise context-free and we don't want to pull in
// context.Background() at every callsite.
func testCtx() context.Context { return context.Background() }

// TestOAuthRegister_RateLimited asserts the per-IP cap kicks in. The
// httptest server uses 127.0.0.1 for every request, so all calls share
// the same rate-limit bucket — 10 succeed, the 11th 429s. Guard against
// a regression that disables the limiter or silently drops the IP key.
func TestOAuthRegister_RateLimited(t *testing.T) {
	server, _ := newDCRServer(t)

	good := func(i int) agent.OAuthRegisterRequest {
		return agent.OAuthRegisterRequest{
			ClientName:   fmt.Sprintf("ratelimit-client-%d", i),
			RedirectURIs: []string{"https://example.com/callback"},
		}
	}

	// First 10 should succeed (the configured cap).
	for i := 0; i < 10; i++ {
		resp := postJSON(t, server, "/api/oauth/register", good(i))
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("request %d should have succeeded under the cap: got %d: %s", i+1, resp.StatusCode, string(body))
		}
	}
	// 11th must 429.
	resp := postJSON(t, server, "/api/oauth/register", good(11))
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusTooManyRequests, "rate_limited")
}

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

// newDCRServer returns a server with OAuth wired (provider + storage).
// We don't reuse setupOAuthAPI because DCR runs anonymously — no need
// to pre-seed a client.
func newDCRServer(t *testing.T) *httptest.Server {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)
	storage := oauth.NewStorage(pool)
	provider, err := oauth.NewProvider(storage, "https://test.e2a.dev", []byte("test-secret-test-secret-test-sec"))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	api.SetOAuthProvider(provider)
	api.SetOAuthStorage(storage)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func postRegister(t *testing.T, srv *httptest.Server, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/oauth2/register", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func assertOAuthError(t *testing.T, resp *http.Response, wantStatus int, wantErrCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Errorf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	var body OAuthErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error != wantErrCode {
		t.Errorf("error code = %q, want %q (description: %q)", body.Error, wantErrCode, body.ErrorDescription)
	}
}

// TestHTTP_Register_Happy: minimal valid input → 201 with defaults
// applied. Also verifies the row actually lands in oauth_clients.
func TestHTTP_Register_Happy(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "Test Client",
		RedirectURIs: []string{"https://example.com/callback"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, string(body))
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control should contain no-store: got %q", cc)
	}

	var got agent.OAuthRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.ClientID, oauth.ClientIDPrefix) {
		t.Errorf("client_id should have %q prefix: got %q", oauth.ClientIDPrefix, got.ClientID)
	}
	if got.ClientIDIssuedAt == 0 {
		t.Error("client_id_issued_at must be non-zero")
	}
	wantGrants := []string{"authorization_code", "refresh_token"}
	if !equalStringSlice(got.GrantTypes, wantGrants) {
		t.Errorf("grant_types default: want %v, got %v", wantGrants, got.GrantTypes)
	}
	if got.TokenEndpointAuthMethod != "none" {
		t.Errorf("token_endpoint_auth_method = %q, want none", got.TokenEndpointAuthMethod)
	}
	// An https redirect is account-eligible (see accountEligibleRedirect), so
	// DCR registers the full ceiling and echoes it back in `scope`.
	if got.Scope != "agent account" {
		t.Errorf("scope = %q, want %q", got.Scope, "agent account")
	}
}

// TestHTTP_Register_RedirectURI_VariousShapes: table-drives the URI
// validator across the shapes we accept and reject.
//
// Loopback covers "localhost", 127.0.0.1, ::1 — every mainstream
// native MCP client (Claude Code included) registers with "localhost",
// so accepting it is non-negotiable. Custom schemes must be in
// reverse-domain form (RFC 8252 §7.1 / RFC 7595 §3.8).
func TestHTTP_Register_RedirectURI_VariousShapes(t *testing.T) {
	srv := newDCRServer(t)
	cases := []struct {
		name       string
		uri        string
		wantStatus int
		wantCode   string
	}{
		{"https web", "https://example.com/callback", http.StatusCreated, ""},
		{"http loopback localhost", "http://localhost:8765/cb", http.StatusCreated, ""},
		{"http loopback ipv4", "http://127.0.0.1:8765/cb", http.StatusCreated, ""},
		{"http loopback ipv6", "http://[::1]:8765/cb", http.StatusCreated, ""},
		{"reverse-domain custom scheme", "com.example.app:/oauth-callback", http.StatusCreated, ""},

		{"http non-loopback", "http://example.com/cb", http.StatusBadRequest, "invalid_redirect_uri"},
		{"single-label custom scheme", "myapp://oauth-callback", http.StatusBadRequest, "invalid_redirect_uri"},
		{"fragment", "https://example.com/cb#frag", http.StatusBadRequest, "invalid_redirect_uri"},
		{"empty", "", http.StatusBadRequest, "invalid_redirect_uri"},
		{"https without host", "https:///cb", http.StatusBadRequest, "invalid_redirect_uri"},
		// userinfo and dangerous-scheme cases live in their own tests
		// below: this table has 10 cases and DCR rate-limits to
		// 10/IP/hr; an 11th would 429 here instead of 400.
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postRegister(t, srv, agent.OAuthRegisterRequest{
				ClientName:   fmt.Sprintf("uri-test-%d", i),
				RedirectURIs: []string{c.uri},
			})
			defer resp.Body.Close()
			if c.wantCode == "" {
				if resp.StatusCode != c.wantStatus {
					body, _ := io.ReadAll(resp.Body)
					t.Fatalf("uri=%q: status=%d want %d; body=%s", c.uri, resp.StatusCode, c.wantStatus, string(body))
				}
				return
			}
			// Error cases: status + RFC 7591 §3.2.2 error-code shape.
			assertOAuthError(t, resp, c.wantStatus, c.wantCode)
		})
	}
}

// TestHTTP_Register_RejectsDangerousSchemes — split out because the
// per-IP DCR rate limit (10/hr) caps the main table. Schemes that
// would smuggle the auth code into JS/HTML execution contexts via
// http.Redirect Location must be rejected at registration.
func TestHTTP_Register_RejectsDangerousSchemes(t *testing.T) {
	srv := newDCRServer(t)
	dangerous := []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"vbscript:msgbox(1)",
		"blob:https://example.com/abc",
		"about:blank",
	}
	for i, uri := range dangerous {
		t.Run(uri, func(t *testing.T) {
			resp := postRegister(t, srv, agent.OAuthRegisterRequest{
				ClientName:   fmt.Sprintf("danger-%d", i),
				RedirectURIs: []string{uri},
			})
			defer resp.Body.Close()
			assertOAuthError(t, resp, http.StatusBadRequest, "invalid_redirect_uri")
		})
	}
}

// TestHTTP_Register_RejectsUserinfoInRedirect — split out of the
// table to keep the per-server case-count under the DCR rate-limit
// cap (10/IP/hr). URIs like "https://anything@evil.com/cb" parse
// with host=evil.com but read as legit to humans.
func TestHTTP_Register_RejectsUserinfoInRedirect(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "userinfo rejection",
		RedirectURIs: []string{"https://anything@evil.com/cb"},
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_redirect_uri")
}

// TestHTTP_Register_DedupRedirectURIs: dup entries in the request
// collapse to one in the response and in the DB row.
func TestHTTP_Register_DedupRedirectURIs(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName: "dedup test",
		RedirectURIs: []string{
			"https://example.com/cb",
			"https://example.com/cb",
			"https://example.com/cb",
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got agent.OAuthRegisterResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.RedirectURIs) != 1 {
		t.Errorf("redirect_uris response should be deduped: got %v", got.RedirectURIs)
	}
}

// TestHTTP_Register_MissingClientName.
func TestHTTP_Register_MissingClientName(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		RedirectURIs: []string{"https://example.com/cb"},
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

// TestHTTP_Register_MissingRedirectURIs.
func TestHTTP_Register_MissingRedirectURIs(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName: "no uris",
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_redirect_uri")
}

// TestHTTP_Register_RejectsConfidentialAuthMethod: discovery only
// advertises "none"; DCR enforces it.
func TestHTTP_Register_RejectsConfidentialAuthMethod(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:              "confidential attempt",
		RedirectURIs:            []string{"https://example.com/cb"},
		TokenEndpointAuthMethod: "client_secret_basic",
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

// TestHTTP_Register_RejectsUnsupportedGrant.
func TestHTTP_Register_RejectsUnsupportedGrant(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "x",
		RedirectURIs: []string{"https://example.com/cb"},
		GrantTypes:   []string{"client_credentials"},
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

// TestHTTP_Register_DropsUnknownScope: DCR narrows rather than rejects
// (RFC 7591 §3.2.1). A spec-compliant MCP client requests the whole
// scopes_supported menu (plus offline_access) whenever the 401 challenge
// carries no scope param, so a hard 400 on any unrecognized scope would
// fail every such client ("couldn't register with e2a's sign-in service").
// Instead the bogus scope is dropped and the eligible ceiling is registered.
func TestHTTP_Register_DropsUnknownScope(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "x",
		RedirectURIs: []string{"https://example.com/cb"},
		Scope:        "admin",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201 (unknown scope dropped, not rejected); body=%s", resp.StatusCode, string(body))
	}
	var got agent.OAuthRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Scope, "admin") {
		t.Errorf("unknown scope must not be reflected back: got %q", got.Scope)
	}
	// The client made an explicit request ("admin") that names no eligible
	// scope, so it's honored at the agent floor — we don't widen it to account
	// just because the redirect would allow it.
	if got.Scope != "agent" {
		t.Errorf("scope = %q, want %q", got.Scope, "agent")
	}
}

// TestHTTP_Register_HonorsExplicitAgentOnly: an account-eligible (https)
// client that EXPLICITLY requests only agent is registered agent-only — we
// never widen a caller's own least-privilege request to account just because
// the redirect would allow it. account_eligible is then reported false so the
// consent screen won't offer/default account for it.
func TestHTTP_Register_HonorsExplicitAgentOnly(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "least privilege",
		RedirectURIs: []string{"https://app.example.com/cb"},
		Scope:        "agent",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, string(body))
	}
	var got agent.OAuthRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Scope != "agent" {
		t.Errorf("scope = %q, want agent (explicit request honored, not widened)", got.Scope)
	}

	// And the public metadata must report the client account-INELIGIBLE, so
	// the consent screen won't default a user into account.
	mResp, err := http.Get(srv.URL + "/oauth2/clients/" + got.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	defer mResp.Body.Close()
	var meta agent.OAuthClientPublicMetadata
	if err := json.NewDecoder(mResp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.AccountEligible {
		t.Error("account_eligible should be false for an agent-only client")
	}
}

// TestHTTP_Register_ExplicitMultiScope: the shape a spec-compliant MCP client
// actually sends — an explicit scope string echoing scopes_supported, possibly
// with offline_access. account is honored (redirect is https-eligible), and
// unknown scopes like offline_access are dropped from the persisted ceiling so
// they never leak into the response `scope` or account_eligible.
func TestHTTP_Register_ExplicitMultiScope(t *testing.T) {
	srv := newDCRServer(t)
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "Claude",
		RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
		Scope:        "agent account offline_access",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, string(body))
	}
	var got agent.OAuthRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Scope != "agent account" {
		t.Errorf("scope = %q, want %q (account honored, offline_access dropped)", got.Scope, "agent account")
	}

	mResp, err := http.Get(srv.URL + "/oauth2/clients/" + got.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	defer mResp.Body.Close()
	var meta agent.OAuthClientPublicMetadata
	if err := json.NewDecoder(mResp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if !meta.AccountEligible {
		t.Error("account_eligible should be true (account is on the ceiling)")
	}
	for _, s := range meta.Scopes {
		if s == "offline_access" {
			t.Errorf("offline_access must not persist in the registered ceiling: %v", meta.Scopes)
		}
	}
}

// TestHTTP_Register_TooManyRedirectURIs.
func TestHTTP_Register_TooManyRedirectURIs(t *testing.T) {
	srv := newDCRServer(t)
	uris := make([]string, 11)
	for i := range uris {
		uris[i] = fmt.Sprintf("https://example.com/cb%d", i)
	}
	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "too many",
		RedirectURIs: uris,
	})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_redirect_uri")
}

// TestHTTP_Register_InvalidJSON.
func TestHTTP_Register_InvalidJSON(t *testing.T) {
	srv := newDCRServer(t)
	resp, err := http.Post(srv.URL+"/oauth2/register", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client_metadata")
}

// TestHTTP_Register_RateLimited: 11th request from the same IP gets
// 429. Per-IP keying uses dcrSourceIP — CF-Connecting-IP preferred,
// then RemoteAddr (loopback in httptest, all 11 share one bucket).
func TestHTTP_Register_RateLimited(t *testing.T) {
	srv := newDCRServer(t)
	good := func(i int) agent.OAuthRegisterRequest {
		return agent.OAuthRegisterRequest{
			ClientName:   fmt.Sprintf("rate-%d", i),
			RedirectURIs: []string{"https://example.com/cb"},
		}
	}
	for i := 0; i < 10; i++ {
		resp := postRegister(t, srv, good(i))
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("burn-in #%d: status %d, want 201", i+1, resp.StatusCode)
		}
	}
	resp := postRegister(t, srv, good(11))
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusTooManyRequests, "rate_limited")
}

// TestHTTP_Register_XFFCannotBypass: rotating X-Forwarded-For must
// NOT defeat the cap (we use dcrSourceIP which prefers
// CF-Connecting-IP, then RemoteAddr — XFF is ignored).
func TestHTTP_Register_XFFCannotBypass(t *testing.T) {
	srv := newDCRServer(t)
	postWithXFF := func(i int, xff string) *http.Response {
		buf, _ := json.Marshal(agent.OAuthRegisterRequest{
			ClientName:   fmt.Sprintf("xff-%d", i),
			RedirectURIs: []string{"https://example.com/cb"},
		})
		req, _ := http.NewRequest("POST", srv.URL+"/oauth2/register", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", xff)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for i := 0; i < 10; i++ {
		resp := postWithXFF(i, fmt.Sprintf("198.51.100.%d", i+1))
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("burn-in #%d: status %d", i+1, resp.StatusCode)
		}
	}
	resp := postWithXFF(11, "203.0.113.99")
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusTooManyRequests, "rate_limited")
}

// TestHTTP_Register_NotConfigured: 404 when SetOAuthStorage wasn't
// called.
func TestHTTP_Register_NotConfigured(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "x",
		RedirectURIs: []string{"https://example.com/cb"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DCR without storage should 404: got %d", resp.StatusCode)
	}
}

// Use context.Background helper without polluting the test surface.
var _ = context.Background

// TestHTTP_GetClient_PublicMetadata covers the GET /oauth2/clients/{id}
// lookup the consent UI calls to render the friendly client_name.
// Asserts: 200 + correct fields on a real client, 404 on unknown, 404
// when OAuth is not wired, no secret fields in the JSON body, and the
// caching header.
func TestHTTP_GetClient_PublicMetadata(t *testing.T) {
	srv := newDCRServer(t)

	// Seed via the DCR endpoint to exercise the real registration path.
	regResp := postRegister(t, srv, agent.OAuthRegisterRequest{
		ClientName:   "Consent UI Test Client",
		RedirectURIs: []string{"https://app.example.com/oauth/cb"},
	})
	defer regResp.Body.Close()
	var reg agent.OAuthRegisterResponse
	if err := json.NewDecoder(regResp.Body).Decode(&reg); err != nil {
		t.Fatalf("decode DCR response: %v", err)
	}

	t.Run("happy", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/oauth2/clients/" + reg.ClientID)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
		}
		if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
			t.Errorf("Cache-Control should advertise caching: got %q", cc)
		}
		var got agent.OAuthClientPublicMetadata
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.ClientID != reg.ClientID {
			t.Errorf("client_id = %q, want %q", got.ClientID, reg.ClientID)
		}
		if got.ClientName != "Consent UI Test Client" {
			t.Errorf("client_name = %q, want Consent UI Test Client", got.ClientName)
		}
		if !equalStringSlice(got.RedirectURIs, []string{"https://app.example.com/oauth/cb"}) {
			t.Errorf("redirect_uris = %v", got.RedirectURIs)
		}
		if !equalStringSlice(got.Scopes, []string{"agent", "account"}) {
			t.Errorf("scopes = %v, want [agent account]", got.Scopes)
		}
		if !got.AccountEligible {
			t.Error("account_eligible should be true for an https redirect")
		}
		if got.ClientIDIssuedAt == 0 {
			t.Error("client_id_issued_at must be non-zero")
		}
	})

	t.Run("no secret fields", func(t *testing.T) {
		// Defensive: even though OAuthClientPublicMetadata's Go type
		// doesn't have a secret field, the JSON body could in theory
		// carry one via misconfigured serialization. Grep the raw body.
		resp, _ := http.Get(srv.URL + "/oauth2/clients/" + reg.ClientID)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		raw := strings.ToLower(string(body))
		for _, forbidden := range []string{"secret", "secret_hash", "audience", "created_by_user"} {
			if strings.Contains(raw, forbidden) {
				t.Errorf("response body must not contain %q: %s", forbidden, body)
			}
		}
	})

	t.Run("unknown client_id 404", func(t *testing.T) {
		resp, _ := http.Get(srv.URL + "/oauth2/clients/mcp_does_not_exist")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("unknown client status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("empty client_id 404", func(t *testing.T) {
		// gorilla/mux requires the path segment to be present, so a
		// trailing slash or empty segment falls through to the default
		// router 404.
		resp, _ := http.Get(srv.URL + "/oauth2/clients/")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("empty client_id status = %d, want 404", resp.StatusCode)
		}
	})
}

// TestHTTP_GetClient_NotConfigured: when SetOAuthStorage hasn't been
// called the endpoint must 404, matching the discovery/DCR pattern.
// We share the bare scaffolding from oauth_discovery_test.go.
func TestHTTP_GetClient_NotConfigured(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)
	// No SetOAuthStorage call.
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/oauth2/clients/mcp_anything")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestHTTP_GetClient_RateLimited: handleOAuthGetClient must enforce
// the same per-IP rate limit as DCR (10/hr today). Without this,
// anonymous high-QPS requests against a path that http.NotFound
// doesn't tag with Cache-Control would create a DB-pressure DOS
// primitive that CDNs can't absorb.
func TestHTTP_GetClient_RateLimited(t *testing.T) {
	srv := newDCRServer(t)
	// Each DCR-server has its own rate limiter at 10/IP/hr. Burn through
	// the budget with GET /oauth2/clients/<random>; the 11th call
	// from the same IP must 429.
	for i := 0; i < 10; i++ {
		resp, err := http.Get(srv.URL + "/oauth2/clients/mcp_doesnotexist_" + fmt.Sprintf("%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// Each of the first 10 returns 404 (unknown client) or 200
		// (if one happens to exist). Either way NOT 429.
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("hit rate limit early on call %d", i+1)
		}
	}
	resp, err := http.Get(srv.URL + "/oauth2/clients/mcp_doesnotexist_11")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("11th call status = %d, want 429 (rate-limit)", resp.StatusCode)
	}
}

// equalStringSlice — order-independent comparison would be safer for
// some assertions, but for our defaults the order is stable. Plain
// equality is fine.
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

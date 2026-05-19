package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/gorilla/mux"
)

// authzFixture bundles the state every authorize/consent test needs:
// a running server, the OAuth store, the identity store, a session
// token, a user, and a registered client whose redirect_uri the tests
// can reuse without re-registering.
type authzFixture struct {
	server       *httptest.Server
	identStore   *identity.Store
	oauthStore   *oauth.Store
	user         *identity.User
	sessionToken string
	client       *oauth.Client
}

const (
	// Reusable PKCE code_challenge — 43-char base64url-encoded SHA-256
	// digest of a deterministic verifier ("dummy-verifier"). The
	// authorize endpoint only checks shape; PKCE verification happens
	// at /token (slice 7). The string lengths match RFC 7636 §4.2.
	testPKCEChallenge = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLM1234"
	testRedirectURI   = "https://client.example.com/cb"
)

func newAuthzFixture(t *testing.T) *authzFixture {
	t.Helper()
	pool := testutil.TestDB(t)
	identStore := identity.NewStore(pool)
	oauthStore := oauth.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()

	ua := auth.NewUserAuth(&config.OAuthConfig{
		GoogleClientID:     "test-id",
		GoogleClientSecret: "test-secret",
		RedirectURL:        "http://localhost/api/auth/callback",
	}, identStore, false)

	// publicURL is critical — the authorize endpoint 503s without it.
	api := agent.NewAPI(identStore, sender, smtpRelay, ua, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://e2a.dev", false)
	api.SetOAuthStore(oauthStore)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	// Need a shared-domain row for slug-based agent creation.
	if err := identStore.EnsureSharedDomain(context.Background(), "agents.e2a.dev"); err != nil {
		t.Fatalf("EnsureSharedDomain: %v", err)
	}

	// Seed user + session.
	user, err := identStore.CreateOrGetUser(context.Background(), "authz-user@example.com", "User", "google-authz")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	token, err := identStore.CreateUserSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}

	// Seed an OAuth client with the test redirect_uri pre-registered.
	client := &oauth.Client{
		ClientID:     oauth.NewClientID(),
		ClientName:   "Test Client",
		RedirectURIs: []string{testRedirectURI},
		ClientType:   "public",
		CreatedVia:   "dcr",
	}
	if err := oauthStore.RegisterClient(context.Background(), client); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}

	return &authzFixture{
		server:       server,
		identStore:   identStore,
		oauthStore:   oauthStore,
		user:         user,
		sessionToken: token,
		client:       client,
	}
}

// authorizeURL builds /api/oauth/authorize?<all params> with the
// fixture's client_id pre-filled. Override individual fields via opts
// — useful for "with malformed X" tests.
func (f *authzFixture) authorizeURL(overrides map[string]string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", f.client.ClientID)
	q.Set("redirect_uri", testRedirectURI)
	q.Set("code_challenge", testPKCEChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", "e2a")
	q.Set("state", "test-state")
	for k, v := range overrides {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	return f.server.URL + "/api/oauth/authorize?" + q.Encode()
}

// doGET sends a GET that does NOT follow redirects. Tests usually want
// to inspect the Location header rather than chase it.
func doGET(t *testing.T, urlStr string, sessionToken string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionToken})
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// doPOSTForm sends a POST with form body, no redirect following.
func doPOSTForm(t *testing.T, urlStr, sessionToken string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionToken})
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// parseLocation parses the Location header off a redirect response.
// Fails the test if absent or unparseable.
func parseLocation(t *testing.T, resp *http.Response) *url.URL {
	t.Helper()
	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatalf("expected Location header on %d response", resp.StatusCode)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("Location header not parseable: %q", loc)
	}
	return u
}

// ──────────────────────── GET /api/oauth/authorize ────────────────────────

func TestAuthorize_NoOAuthStore_404(t *testing.T) {
	pool := testutil.TestDB(t)
	identStore := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(identStore, sender, smtpRelay, nil, usage.NewNoopUsageTracker(), "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://e2a.dev", false)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	resp := doGET(t, server.URL+"/api/oauth/authorize?response_type=code&client_id=x&redirect_uri=https://example.com/cb&code_challenge="+testPKCEChallenge+"&code_challenge_method=S256&scope=e2a", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 without oauth store, got %d", resp.StatusCode)
	}
}

func TestAuthorize_NoPublicURL_503(t *testing.T) {
	pool := testutil.TestDB(t)
	identStore := identity.NewStore(pool)
	oauthStore := oauth.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	// empty publicURL
	api := agent.NewAPI(identStore, sender, smtpRelay, nil, usage.NewNoopUsageTracker(), "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetOAuthStore(oauthStore)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	resp := doGET(t, server.URL+"/api/oauth/authorize", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 without publicURL, got %d", resp.StatusCode)
	}
}

func TestAuthorize_MissingClientID_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doGET(t, f.authorizeURL(map[string]string{"client_id": ""}), f.sessionToken)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestAuthorize_BadRedirectURI_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doGET(t, f.authorizeURL(map[string]string{"redirect_uri": "https://example.com/cb#frag"}), f.sessionToken)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestAuthorize_UnknownClient_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doGET(t, f.authorizeURL(map[string]string{"client_id": "mcp_deadbeefcafe"}), f.sessionToken)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestAuthorize_RedirectURI_NotRegistered_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doGET(t, f.authorizeURL(map[string]string{"redirect_uri": "https://attacker.example.com/cb"}), f.sessionToken)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

// TestAuthorize_UnsupportedResponseType_RedirectError verifies that
// once redirect_uri is verified, soft errors come back via redirect
// per RFC 6749 §4.1.2.1 — not direct HTTP 400.
func TestAuthorize_UnsupportedResponseType_RedirectError(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doGET(t, f.authorizeURL(map[string]string{"response_type": "token"}), f.sessionToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	loc := parseLocation(t, resp)
	if loc.Query().Get("error") != "unsupported_response_type" {
		t.Errorf("expected error=unsupported_response_type, got %q", loc.Query().Get("error"))
	}
	if loc.Query().Get("state") != "test-state" {
		t.Errorf("state must round-trip: got %q", loc.Query().Get("state"))
	}
}

func TestAuthorize_MissingPKCE_RedirectError(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doGET(t, f.authorizeURL(map[string]string{"code_challenge": ""}), f.sessionToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	if parseLocation(t, resp).Query().Get("error") != "invalid_request" {
		t.Error("expected invalid_request error in redirect")
	}
}

func TestAuthorize_NoSession_RedirectsToLogin(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doGET(t, f.authorizeURL(nil), "") // no session cookie
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect to login, got %d", resp.StatusCode)
	}
	loc := parseLocation(t, resp)
	if !strings.HasSuffix(loc.Path, "/api/auth/login") {
		t.Errorf("expected redirect to /api/auth/login, got %q", loc.String())
	}
}

func TestAuthorize_WithSession_RedirectsToConsentUI(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doGET(t, f.authorizeURL(nil), f.sessionToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 to consent UI, got %d", resp.StatusCode)
	}
	loc := parseLocation(t, resp)
	if !strings.HasSuffix(loc.Path, "/oauth/consent") {
		t.Errorf("expected /oauth/consent path, got %q", loc.Path)
	}
	q := loc.Query()
	if q.Get("client_id") != f.client.ClientID {
		t.Errorf("client_id must round-trip: got %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != testRedirectURI {
		t.Errorf("redirect_uri must round-trip: got %q", q.Get("redirect_uri"))
	}
	if q.Get("code_challenge") != testPKCEChallenge {
		t.Errorf("code_challenge must round-trip: got %q", q.Get("code_challenge"))
	}
	if q.Get("state") != "test-state" {
		t.Errorf("state must round-trip: got %q", q.Get("state"))
	}
}

// ──────────────────────── POST /api/oauth/consent ────────────────────────

// consentForm builds the standard "happy path" form for the fixture.
// Tests override individual fields with the overrides map.
func (f *authzFixture) consentForm(overrides map[string]string) url.Values {
	form := url.Values{}
	form.Set("action", "allow")
	form.Set("agent_choice", "create_new")
	form.Set("new_agent_slug", "") // default
	form.Set("response_type", "code")
	form.Set("client_id", f.client.ClientID)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("code_challenge", testPKCEChallenge)
	form.Set("code_challenge_method", "S256")
	form.Set("scope", "e2a")
	form.Set("state", "test-state")
	for k, v := range overrides {
		if v == "" && k != "new_agent_slug" {
			form.Del(k)
		} else {
			form.Set(k, v)
		}
	}
	return form
}

func TestConsent_NoSession_401(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", "", f.consentForm(nil))
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusUnauthorized, "access_denied")
}

func TestConsent_MissingClientID_400(t *testing.T) {
	f := newAuthzFixture(t)
	form := f.consentForm(map[string]string{"client_id": ""})
	form.Del("client_id")
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestConsent_UnknownClient_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken,
		f.consentForm(map[string]string{"client_id": "mcp_unknownclientx"}))
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestConsent_TamperedRedirectURI_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken,
		f.consentForm(map[string]string{"redirect_uri": "https://attacker.example.com/cb"}))
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestConsent_Deny_RedirectsWithError(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken,
		f.consentForm(map[string]string{"action": "deny"}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	loc := parseLocation(t, resp)
	if !strings.HasPrefix(loc.String(), testRedirectURI) {
		t.Errorf("must redirect back to client redirect_uri: got %q", loc.String())
	}
	if loc.Query().Get("error") != "access_denied" {
		t.Errorf("expected error=access_denied, got %q", loc.Query().Get("error"))
	}
	if loc.Query().Get("state") != "test-state" {
		t.Errorf("state must round-trip, got %q", loc.Query().Get("state"))
	}
}

func TestConsent_InvalidAction_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken,
		f.consentForm(map[string]string{"action": "shenanigan"}))
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestConsent_Allow_CreateNew_DefaultSlug(t *testing.T) {
	f := newAuthzFixture(t)
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, f.consentForm(nil))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 with code, got %d", resp.StatusCode)
	}
	loc := parseLocation(t, resp)
	if !strings.HasPrefix(loc.String(), testRedirectURI) {
		t.Errorf("must redirect back to client: got %q", loc.String())
	}
	code := loc.Query().Get("code")
	if !strings.HasPrefix(code, oauth.AuthCodePrefix) {
		t.Errorf("returned code should have %q prefix: got %q", oauth.AuthCodePrefix, code)
	}
	if loc.Query().Get("state") != "test-state" {
		t.Errorf("state must round-trip, got %q", loc.Query().Get("state"))
	}

	// The auth code row should be present and bound to a new agent on
	// the shared domain — the default slug should start with the
	// slugified client name ("test-client-…").
	gotCode, state, err := f.oauthStore.AtomicConsumeCode(context.Background(), code)
	if err != nil {
		t.Fatalf("AtomicConsumeCode: %v", err)
	}
	if state != oauth.ConsumeFresh {
		t.Errorf("expected ConsumeFresh, got state=%v", state)
	}
	if gotCode.UserID != f.user.ID {
		t.Errorf("code.UserID = %q, want %q", gotCode.UserID, f.user.ID)
	}
	if !strings.HasSuffix(gotCode.AgentEmail, "@agents.e2a.dev") {
		t.Errorf("agent should be on shared domain: got %q", gotCode.AgentEmail)
	}
	if !strings.HasPrefix(gotCode.AgentEmail, "test-client-") {
		t.Errorf("default slug should start with slugified client name: got %q", gotCode.AgentEmail)
	}
	if gotCode.CodeChallenge != testPKCEChallenge {
		t.Errorf("PKCE challenge must be preserved: got %q", gotCode.CodeChallenge)
	}
}

func TestConsent_Allow_CreateNew_UserOverrideSlug(t *testing.T) {
	f := newAuthzFixture(t)
	form := f.consentForm(nil)
	form.Set("new_agent_slug", "my-custom-bot")
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	loc := parseLocation(t, resp)
	code := loc.Query().Get("code")
	gotCode, state, err := f.oauthStore.AtomicConsumeCode(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if state != oauth.ConsumeFresh {
		t.Errorf("expected ConsumeFresh, got %v", state)
	}
	if gotCode.AgentEmail != "my-custom-bot@agents.e2a.dev" {
		t.Errorf("user-overridden slug must be used verbatim: got %q", gotCode.AgentEmail)
	}
}

func TestConsent_Allow_CreateNew_InvalidSlug_400(t *testing.T) {
	f := newAuthzFixture(t)
	form := f.consentForm(nil)
	form.Set("new_agent_slug", "BAD SLUG WITH SPACES")
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestConsent_Allow_CreateNew_SlugCollision_409(t *testing.T) {
	f := newAuthzFixture(t)
	// Pre-create the agent so the consent attempt collides.
	if _, err := f.identStore.CreateAgent(context.Background(), "taken-slug@agents.e2a.dev", "agents.e2a.dev", "", "", "local", f.user.ID); err != nil {
		t.Fatal(err)
	}
	form := f.consentForm(nil)
	form.Set("new_agent_slug", "taken-slug")
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusConflict, "invalid_request")
}

func TestConsent_Allow_ExistingAgent_OK(t *testing.T) {
	f := newAuthzFixture(t)
	// User must own an agent first.
	agentEmail := "mine@agents.e2a.dev"
	if _, err := f.identStore.CreateAgent(context.Background(), agentEmail, "agents.e2a.dev", "", "", "local", f.user.ID); err != nil {
		t.Fatal(err)
	}
	form := f.consentForm(map[string]string{"agent_choice": "existing:" + agentEmail})
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 with code, got %d", resp.StatusCode)
	}
	loc := parseLocation(t, resp)
	code := loc.Query().Get("code")
	gotCode, state, err := f.oauthStore.AtomicConsumeCode(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if state != oauth.ConsumeFresh {
		t.Errorf("expected ConsumeFresh, got %v", state)
	}
	if gotCode.AgentEmail != agentEmail {
		t.Errorf("agent_email on code should reflect choice: got %q", gotCode.AgentEmail)
	}
}

func TestConsent_Allow_ExistingAgent_NotOwned_403(t *testing.T) {
	f := newAuthzFixture(t)
	// Another user owns "victim-agent".
	other, err := f.identStore.CreateOrGetUser(context.Background(), "other@example.com", "Other", "google-other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.identStore.CreateAgent(context.Background(), "victim-agent@agents.e2a.dev", "agents.e2a.dev", "", "", "local", other.ID); err != nil {
		t.Fatal(err)
	}
	form := f.consentForm(map[string]string{"agent_choice": "existing:victim-agent@agents.e2a.dev"})
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusForbidden, "access_denied")
}

func TestConsent_Allow_ExistingAgent_NotFound_400(t *testing.T) {
	f := newAuthzFixture(t)
	form := f.consentForm(map[string]string{"agent_choice": "existing:does-not-exist@agents.e2a.dev"})
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestConsent_BadAgentChoice_400(t *testing.T) {
	f := newAuthzFixture(t)
	form := f.consentForm(map[string]string{"agent_choice": "weird-value"})
	resp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

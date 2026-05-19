package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mnexa-AI/e2a/internal/config"
)

// setupAPIWithOAuth wires the API with OAuth enabled. Pairs with the
// existing setupAPI helper but adds the oauth.Store so ate2a_-prefixed
// bearer tokens are dispatched correctly.
func setupAPIWithOAuth(t *testing.T) (*httptest.Server, *identity.Store, *oauth.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	oauthStore := oauth.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetOAuthStore(oauthStore)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, store, oauthStore, pool
}

// seedUserAndAPIKey creates a user and an API key, returns both.
func seedUserAndAPIKey(t *testing.T, store *identity.Store, email string) (*identity.User, string) {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, email, "Test User", "google-"+email)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, user.ID, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	return user, key.PlaintextKey
}

// seedOAuthClient creates a public OAuth client and returns its id.
func seedOAuthClient(t *testing.T, s *oauth.Store) string {
	t.Helper()
	c := &oauth.Client{
		ClientID:     oauth.NewClientID(),
		ClientName:   "test-client",
		RedirectURIs: []string{"http://127.0.0.1:54321/callback"},
		ClientType:   "public",
		CreatedVia:   "dcr",
	}
	if err := s.RegisterClient(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c.ClientID
}

// issueOAuthToken inserts an oauth_tokens row directly — we don't
// need the /api/oauth/token endpoint for these tests (that's a future
// slice); we just need the store-level row so the dispatch path can
// validate against it.
func issueOAuthToken(t *testing.T, s *oauth.Store, clientID, userID string, opts ...func(*oauth.Token)) *oauth.Token {
	t.Helper()
	now := time.Now()
	refreshExp := now.Add(oauth.RefreshTokenLifetime)
	tok := &oauth.Token{
		AccessToken:      oauth.NewAccessToken(),
		RefreshToken:     oauth.NewRefreshToken(),
		RefreshChainID:   oauth.NewChainID(),
		ClientID:         clientID,
		UserID:           userID,
		Scope:            "e2a",
		ExpiresAt:        now.Add(oauth.AccessTokenLifetime),
		RefreshExpiresAt: &refreshExp,
	}
	for _, opt := range opts {
		opt(tok)
	}
	if err := s.IssueToken(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	return tok
}

// getWithBearer sends an authenticated GET to /api/v1/agents (the
// simplest authenticated endpoint).
func getWithBearer(t *testing.T, server *httptest.Server, bearer string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", server.URL+"/api/v1/agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ──────────────────────── regression: API key still works ────────────────────────

func TestAuth_APIKeyStillWorks_PostOAuth(t *testing.T) {
	server, store, _, _ := setupAPIWithOAuth(t)
	_, apiKey := seedUserAndAPIKey(t, store, "apikey-user@example.com")

	resp := getWithBearer(t, server, apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API key auth regressed: got %d, want 200", resp.StatusCode)
	}
}

// ──────────────────────── OAuth happy path ────────────────────────

func TestAuth_OAuthToken_Active(t *testing.T) {
	server, store, oauthStore, _ := setupAPIWithOAuth(t)
	user, _ := seedUserAndAPIKey(t, store, "oauth-user@example.com")
	clientID := seedOAuthClient(t, oauthStore)
	tok := issueOAuthToken(t, oauthStore, clientID, user.ID)

	resp := getWithBearer(t, server, tok.AccessToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("active OAuth token should authenticate: got %d", resp.StatusCode)
	}
}

// ──────────────────────── OAuth rejection paths ────────────────────────

func TestAuth_OAuthToken_Revoked(t *testing.T) {
	server, store, oauthStore, _ := setupAPIWithOAuth(t)
	user, _ := seedUserAndAPIKey(t, store, "revoked-user@example.com")
	clientID := seedOAuthClient(t, oauthStore)
	tok := issueOAuthToken(t, oauthStore, clientID, user.ID)

	// Revoke the token.
	if err := oauthStore.RevokeToken(context.Background(), tok.AccessToken); err != nil {
		t.Fatal(err)
	}

	resp := getWithBearer(t, server, tok.AccessToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked OAuth token should be rejected: got %d", resp.StatusCode)
	}
}

func TestAuth_OAuthToken_Expired(t *testing.T) {
	server, store, oauthStore, _ := setupAPIWithOAuth(t)
	user, _ := seedUserAndAPIKey(t, store, "expired-user@example.com")
	clientID := seedOAuthClient(t, oauthStore)
	tok := issueOAuthToken(t, oauthStore, clientID, user.ID, func(t *oauth.Token) {
		t.ExpiresAt = time.Now().Add(-1 * time.Minute) // already expired
	})

	resp := getWithBearer(t, server, tok.AccessToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired OAuth token should be rejected: got %d", resp.StatusCode)
	}
}

func TestAuth_OAuthToken_Unknown(t *testing.T) {
	server, _, _, _ := setupAPIWithOAuth(t)

	// Well-formed-looking but nonexistent token.
	resp := getWithBearer(t, server, oauth.AccessTokenPrefix+"deadbeefdeadbeefdeadbeefdeadbeef")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown OAuth token should be rejected: got %d", resp.StatusCode)
	}
}

// TestAuth_OAuthPrefix_WithoutStore_FailsClosed ensures a deployment
// that hasn't called SetOAuthStore rejects ate2a_ tokens cleanly
// (rather than e.g. falling back to GetUserByAPIKey which would
// return ErrAPIKeyNotFound and surface as 401 anyway — but with a
// less informative error). Same 401, more debuggable.
func TestAuth_OAuthPrefix_WithoutStore_FailsClosed(t *testing.T) {
	// Manually set up an API *without* the OAuth store.
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	// Note: no SetOAuthStore call.
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	resp := getWithBearer(t, server, oauth.AccessTokenPrefix+"00112233445566778899aabbccddeeff")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("OAuth bearer without configured store should 401: got %d", resp.StatusCode)
	}
}

// TestAuth_MalformedBearer covers the gap between API-key prefix
// (e2a_) and OAuth prefix (ate2a_): some arbitrary garbage bearer
// must not authenticate. The current dispatch treats anything not
// starting with ate2a_ as an API-key candidate, so this exercises
// the GetUserByAPIKey not-found path.
func TestAuth_MalformedBearer(t *testing.T) {
	server, _, _, _ := setupAPIWithOAuth(t)
	resp := getWithBearer(t, server, "garbage-not-an-api-key")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("garbage bearer should 401: got %d", resp.StatusCode)
	}
}

// TestAuth_NoBearer ensures an unauthenticated request still 401s.
// Regression guard for "did I accidentally let the OAuth path through
// when no Authorization header is present."
func TestAuth_NoBearer(t *testing.T) {
	server, _, _, _ := setupAPIWithOAuth(t)
	resp := getWithBearer(t, server, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no bearer should 401: got %d", resp.StatusCode)
	}
}

package agent_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/fosite"
)

// setupOAuthAPI builds the API with a fosite provider wired in, against
// a fresh test DB. Returns the running httptest server, the provider
// (for inline authorize flow seeding), the underlying pool, and
// seeded client + user IDs ready for /token exchanges.
func setupOAuthAPI(t *testing.T) (server *httptest.Server, provider fosite.OAuth2Provider, pool *pgxpool.Pool, clientID, userID string) {
	t.Helper()
	pool = testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")

	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)

	secret := []byte("test-secret-test-secret-test-sec")
	storage := oauth.NewStorage(pool)
	provider = oauth.NewProvider(storage, "https://test.e2a.dev", secret)
	api.SetOAuthProvider(provider)

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server = httptest.NewServer(router)
	t.Cleanup(server.Close)

	// Seed user + client.
	userID = "usr_" + randHex8(t)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, email, name, google_subject, created_at)
		VALUES ($1, $2, 'Test User', $3, NOW())
		ON CONFLICT (id) DO NOTHING
	`, userID, userID+"@example.com", "google-"+userID); err != nil {
		t.Fatal(err)
	}

	clientID = "mcp_http_test"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types,
		     response_types, scopes, audiences, token_endpoint_auth_method,
		     public, created_via)
		VALUES ($1, 'test client',
		        ARRAY['http://localhost:8765/callback'],
		        ARRAY['authorization_code','refresh_token'],
		        ARRAY['code'],
		        ARRAY['mcp'],
		        ARRAY[]::TEXT[],
		        'none', TRUE, 'dcr')
		ON CONFLICT (client_id) DO NOTHING
	`, clientID); err != nil {
		t.Fatal(err)
	}
	return server, provider, pool, clientID, userID
}

func randHex8(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 8)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

func newPKCE(t *testing.T) (verifier, challenge string) {
	t.Helper()
	v := make([]byte, 32)
	if _, err := rand.Read(v); err != nil {
		t.Fatal(err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(v)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

// mintAuthCode drives fosite's authorize flow inline to produce a code
// for the given user/client. This is what the consent handler will do
// in slice 5; for slice 4's HTTP test we just need a code to exchange
// at /token.
func mintAuthCode(t *testing.T, provider fosite.OAuth2Provider, clientID, userID, redirectURI, challenge string) string {
	t.Helper()
	ctx := context.Background()
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "mcp")
	q.Set("state", "abc123abc123abc123")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")

	authReq, _ := http.NewRequest("GET", "https://test.e2a.dev/api/oauth/authorize?"+q.Encode(), nil)
	ar, err := provider.NewAuthorizeRequest(ctx, authReq)
	if err != nil {
		t.Fatalf("NewAuthorizeRequest: %v", err)
	}
	ar.SetSession(&oauth.Session{UserID: userID, AgentEmail: "agent@example.com", Subject: userID})
	ar.GrantScope("mcp")

	resp, err := provider.NewAuthorizeResponse(ctx, ar, ar.GetSession())
	if err != nil {
		t.Fatalf("NewAuthorizeResponse: %v", err)
	}
	rec := httptest.NewRecorder()
	provider.WriteAuthorizeResponse(ctx, rec, ar, resp)
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %s", rec.Header().Get("Location"))
	}
	return code
}

// TestHTTP_Token_AuthCode covers the happy path: POST /api/oauth/token
// with an auth_code grant + PKCE verifier yields an access+refresh
// token JSON envelope with the right fields and the no-store header.
func TestHTTP_Token_AuthCode(t *testing.T) {
	server, provider, _, clientID, userID := setupOAuthAPI(t)
	redirectURI := "http://localhost:8765/callback"
	verifier, challenge := newPKCE(t)
	code := mintAuthCode(t, provider, clientID, userID, redirectURI, challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)

	resp, err := http.Post(server.URL+"/api/oauth/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// RFC 6749 §5.1: token response MUST include Cache-Control: no-store.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body.AccessToken, oauth.AccessTokenPrefix) {
		t.Errorf("access_token missing %q prefix: %q", oauth.AccessTokenPrefix, body.AccessToken)
	}
	if !strings.HasPrefix(body.RefreshToken, oauth.RefreshTokenPrefix) {
		t.Errorf("refresh_token missing %q prefix: %q", oauth.RefreshTokenPrefix, body.RefreshToken)
	}
	if body.TokenType != "bearer" {
		t.Errorf("token_type = %q, want bearer", body.TokenType)
	}
	if body.ExpiresIn <= 0 {
		t.Errorf("expires_in = %d, want > 0", body.ExpiresIn)
	}
}

// TestHTTP_Token_RefreshGrant: exchange auth_code, then exchange the
// refresh token for a new pair. Verifies refresh rotation works through
// the HTTP layer.
func TestHTTP_Token_RefreshGrant(t *testing.T) {
	server, provider, _, clientID, userID := setupOAuthAPI(t)
	redirectURI := "http://localhost:8765/callback"
	verifier, challenge := newPKCE(t)
	code := mintAuthCode(t, provider, clientID, userID, redirectURI, challenge)

	// Code exchange.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	resp, err := http.Post(server.URL+"/api/oauth/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	json.NewDecoder(resp.Body).Decode(&first)
	resp.Body.Close()

	// Refresh exchange.
	form = url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", first.RefreshToken)
	form.Set("client_id", clientID)
	resp2, err := http.Post(server.URL+"/api/oauth/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("refresh exchange status = %d, want 200", resp2.StatusCode)
	}
	var second struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if second.AccessToken == first.AccessToken {
		t.Error("refresh should have rotated the access token")
	}
	if second.RefreshToken == first.RefreshToken {
		t.Error("refresh should have rotated the refresh token (single-use)")
	}
}

// TestHTTP_Token_BadPKCE confirms fosite rejects a wrong verifier at
// the HTTP boundary with invalid_grant per RFC 6749 §5.2.
func TestHTTP_Token_BadPKCE(t *testing.T) {
	server, provider, _, clientID, userID := setupOAuthAPI(t)
	redirectURI := "http://localhost:8765/callback"
	_, challenge := newPKCE(t)
	code := mintAuthCode(t, provider, clientID, userID, redirectURI, challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", "not-the-right-verifier-not-the-right-verifier")

	resp, err := http.Post(server.URL+"/api/oauth/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 invalid_grant", resp.StatusCode)
	}
	var body struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Error != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", body.Error)
	}
}

// TestHTTP_Token_CodeReplay drives the code-reuse path: exchange
// once (success), then re-present the same code (rejection + the
// originally issued tokens get revoked per RFC 6749 §10.5).
func TestHTTP_Token_CodeReplay(t *testing.T) {
	server, provider, pool, clientID, userID := setupOAuthAPI(t)
	redirectURI := "http://localhost:8765/callback"
	verifier, challenge := newPKCE(t)
	code := mintAuthCode(t, provider, clientID, userID, redirectURI, challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)

	// First exchange wins.
	resp, err := http.Post(server.URL+"/api/oauth/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first exchange: status = %d, want 200", resp.StatusCode)
	}
	var first struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&first)
	resp.Body.Close()

	// Replay: same code, same verifier. Must fail.
	resp2, err := http.Post(server.URL+"/api/oauth/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("replay: status = %d, want 400 invalid_grant", resp2.StatusCode)
	}

	// And the tokens from the first exchange must be revoked (the
	// §10.5 reuse defense fosite drives via RevokeAccessToken).
	var revoked bool
	err = pool.QueryRow(context.Background(), `
		SELECT revoked_at IS NOT NULL FROM oauth_access_tokens
		WHERE request_id IN (
		    SELECT request_id FROM oauth_auth_codes WHERE active = FALSE
		)
		LIMIT 1
	`).Scan(&revoked)
	if err != nil {
		t.Fatalf("query revoked status: %v", err)
	}
	if !revoked {
		t.Error("expected the originally-issued access token to be revoked after code replay")
	}
}

// TestHTTP_Token_NotConfigured covers the 404 path when an operator
// hasn't wired the provider (SetOAuthProvider never called).
func TestHTTP_Token_NotConfigured(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)
	// no SetOAuthProvider call

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/oauth/token",
		"application/x-www-form-urlencoded", strings.NewReader("grant_type=authorization_code"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when provider not wired", resp.StatusCode)
	}
}

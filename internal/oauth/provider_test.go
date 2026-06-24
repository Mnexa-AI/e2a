package oauth_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/testutil"

	"github.com/ory/fosite"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newProviderFixture wires a fosite OAuth2Provider over the same
// Postgres-backed Storage we built in slice 3, with a deterministic
// HMAC secret so test runs are reproducible. The seed is identity-
// specific because some FK paths (oauth_clients.created_by_user_id)
// resolve against the users table.
func newProviderFixture(t *testing.T) (fosite.OAuth2Provider, *oauth.Storage, *pgxpool.Pool, string, string) {
	t.Helper()
	pool := testutil.TestDB(t)

	// 32-byte fixed secret; fosite refuses to generate with a shorter one.
	secret := []byte("test-secret-test-secret-test-sec")

	storage := oauth.NewStorage(pool)
	provider, err := oauth.NewProvider(storage, "https://test.e2a.dev", secret)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// Seed a public DCR client + a user. The same seeders the storage
	// tests use; we re-walk them here so a future test that imports
	// only this file doesn't depend on storage_test.go.
	clientID := "mcp_test_provider"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types,
		     response_types, scopes, audiences, token_endpoint_auth_method,
		     public, created_via)
		VALUES ($1, 'test client',
		        ARRAY['http://localhost:8765/callback'],
		        ARRAY['authorization_code','refresh_token'],
		        ARRAY['code'],
		        ARRAY['agent'],
		        ARRAY[]::TEXT[],
		        'none', TRUE, 'dcr')
		ON CONFLICT (client_id) DO NOTHING
	`, clientID); err != nil {
		t.Fatalf("seed client: %v", err)
	}

	userID := "usr_" + randHex(t, 8)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, email, name, google_subject, created_at)
		VALUES ($1, $2, 'Test User', $3, NOW())
		ON CONFLICT (id) DO NOTHING
	`, userID, userID+"@example.com", "google-"+userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	return provider, storage, pool, userID, clientID
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	// hex-ish without importing encoding/hex (we just need uniqueness)
	const hex = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

// pkcePair returns a PKCE (verifier, challenge) pair for S256.
func pkcePair() (verifier, challenge string) {
	v := make([]byte, 32)
	_, _ = rand.Read(v)
	verifier = base64.RawURLEncoding.EncodeToString(v)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

// TestProvider_AuthCode_RoundTrip drives the full authorize-code flow
// through fosite without an HTTP server: build an AuthorizeRequest,
// have fosite issue a code (stored via our Storage), then exchange
// the code for tokens. Verifies the prefix wrapping, PKCE-S256, the
// session round-trip, and the storage adapter all line up under
// fosite's expected call graph.
func TestProvider_AuthCode_RoundTrip(t *testing.T) {
	provider, _, _, userID, clientID := newProviderFixture(t)
	ctx := context.Background()
	verifier, challenge := pkcePair()
	redirectURI := "http://localhost:8765/callback"

	// ---- 1. Build a fake /authorize HTTP request and have fosite
	// validate it (NewAuthorizeRequest). The handler we'll write in
	// slice 5 does this same call before deciding whether to show
	// the consent UI; for now we drive it inline.
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "agent")
	q.Set("state", "abc123abc123abc123")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")

	authReq, err := http.NewRequest("GET", "https://test.e2a.dev/api/oauth/authorize?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}

	ar, err := provider.NewAuthorizeRequest(ctx, authReq)
	if err != nil {
		t.Fatalf("NewAuthorizeRequest: %v", err)
	}

	// Set our session — this is what the consent handler will do
	// after the user clicks Allow. fosite serializes this onto the
	// auth-code row; on token exchange we get it back.
	sess := &oauth.Session{
		UserID:     userID,
		AgentEmail: "agent@example.com",
		Subject:    userID,
	}
	ar.SetSession(sess)
	// Grant the requested scope. Without this fosite would drop the
	// scope between authorize and the issued tokens.
	ar.GrantScope("agent")

	// Have fosite write the response — this is what materializes the
	// authorize code (and stores it via our Storage). We use a
	// httptest.ResponseRecorder so we can inspect the redirect.
	rec := httptest.NewRecorder()
	resp, err := provider.NewAuthorizeResponse(ctx, ar, sess)
	if err != nil {
		t.Fatalf("NewAuthorizeResponse: %v", err)
	}
	provider.WriteAuthorizeResponse(ctx, rec, ar, resp)

	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("WriteAuthorizeResponse: want 302/303, got %d body=%q", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location parse: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("authorize redirect missing code: %s", rec.Header().Get("Location"))
	}
	if !strings.HasPrefix(code, oauth.AuthCodePrefix) {
		t.Errorf("code missing %q prefix: %q", oauth.AuthCodePrefix, code)
	}
	if got := loc.Query().Get("state"); got != "abc123abc123abc123" {
		t.Errorf("state round-trip: want abc123abc123abc123, got %q", got)
	}
	// Note on RFC 9207 `iss`: fosite v0.49.0 doesn't emit it natively
	// (added in later versions). The consent handler (slice 5) will
	// append it before redirecting back to the client. Not asserted
	// here — this test is scoped to the protocol layer only.

	// ---- 2. Exchange the code for tokens via /token.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	tokReq, err := http.NewRequest("POST", "https://test.e2a.dev/api/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("http.NewRequest token: %v", err)
	}
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	accessSession := &oauth.Session{}
	accessReq, err := provider.NewAccessRequest(ctx, tokReq, accessSession)
	if err != nil {
		t.Fatalf("NewAccessRequest: %v", err)
	}

	accessResp, err := provider.NewAccessResponse(ctx, accessReq)
	if err != nil {
		t.Fatalf("NewAccessResponse: %v", err)
	}

	access := accessResp.GetAccessToken()
	refresh := accessResp.ToMap()["refresh_token"]
	if !strings.HasPrefix(access, oauth.AccessTokenPrefix) {
		t.Errorf("access token missing %q prefix: %q", oauth.AccessTokenPrefix, access)
	}
	refreshStr, _ := refresh.(string)
	if !strings.HasPrefix(refreshStr, oauth.RefreshTokenPrefix) {
		t.Errorf("refresh token missing %q prefix: %q", oauth.RefreshTokenPrefix, refreshStr)
	}

	// Session round-trip: the AgentEmail we set at consent time must
	// be readable on the issued access token.
	if accessSession.AgentEmail != "agent@example.com" {
		t.Errorf("session AgentEmail at exchange: want agent@example.com, got %q", accessSession.AgentEmail)
	}
	if accessSession.UserID != userID {
		t.Errorf("session UserID at exchange: want %q, got %q", userID, accessSession.UserID)
	}

	// ---- 3. Refresh exchange. Verifies rotation works and the
	// session carries through a second time.
	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("refresh_token", refreshStr)
	refreshForm.Set("client_id", clientID)
	rfReq, err := http.NewRequest("POST", "https://test.e2a.dev/api/oauth/token",
		strings.NewReader(refreshForm.Encode()))
	if err != nil {
		t.Fatalf("http.NewRequest refresh: %v", err)
	}
	rfReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rfSession := &oauth.Session{}
	rfAccessReq, err := provider.NewAccessRequest(ctx, rfReq, rfSession)
	if err != nil {
		t.Fatalf("NewAccessRequest refresh: %v", err)
	}
	rfResp, err := provider.NewAccessResponse(ctx, rfAccessReq)
	if err != nil {
		t.Fatalf("NewAccessResponse refresh: %v", err)
	}

	newAccess := rfResp.GetAccessToken()
	if newAccess == access {
		t.Error("refresh should rotate the access token")
	}
	if !strings.HasPrefix(newAccess, oauth.AccessTokenPrefix) {
		t.Errorf("rotated access token missing prefix: %q", newAccess)
	}
	if rfSession.AgentEmail != "agent@example.com" {
		t.Errorf("session AgentEmail after refresh: want agent@example.com, got %q", rfSession.AgentEmail)
	}
}

// TestProvider_AuthCode_BadPKCE confirms fosite rejects a wrong
// code_verifier at /token. The provider should refuse without
// touching our storage's reuse-defense path (the code is consumed
// only on successful exchange — failed PKCE is a no-op).
func TestProvider_AuthCode_BadPKCE(t *testing.T) {
	provider, _, _, userID, clientID := newProviderFixture(t)
	ctx := context.Background()
	_, challenge := pkcePair()
	redirectURI := "http://localhost:8765/callback"

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "agent")
	q.Set("state", "abc123abc123abc123")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authReq, _ := http.NewRequest("GET", "https://test.e2a.dev/api/oauth/authorize?"+q.Encode(), nil)
	ar, err := provider.NewAuthorizeRequest(ctx, authReq)
	if err != nil {
		t.Fatalf("NewAuthorizeRequest: %v", err)
	}
	ar.SetSession(&oauth.Session{UserID: userID, AgentEmail: "a@b.c", Subject: userID})
	ar.GrantScope("agent")
	resp, err := provider.NewAuthorizeResponse(ctx, ar, ar.GetSession())
	if err != nil {
		t.Fatalf("NewAuthorizeResponse: %v", err)
	}
	rec := httptest.NewRecorder()
	provider.WriteAuthorizeResponse(ctx, rec, ar, resp)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	code := loc.Query().Get("code")

	// Exchange with the WRONG verifier.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", "not-the-real-verifier-not-the-real-verifier")
	tokReq, _ := http.NewRequest("POST", "https://test.e2a.dev/api/oauth/token",
		strings.NewReader(form.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	_, err = provider.NewAccessRequest(ctx, tokReq, &oauth.Session{})
	if err == nil {
		t.Fatal("expected NewAccessRequest to reject wrong PKCE verifier")
	}
}

// TestProvider_PKCEPlainRejected confirms code_challenge_method=plain
// is rejected. The PKCE handler checks the method during the response
// phase (NewAuthorizeResponse), not the request-parsing phase — so we
// drive through to that step here.
func TestProvider_PKCEPlainRejected(t *testing.T) {
	provider, _, _, userID, clientID := newProviderFixture(t)
	ctx := context.Background()
	_, challenge := pkcePair()
	redirectURI := "http://localhost:8765/callback"

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "agent")
	q.Set("state", "abc123abc123abc123")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "plain")
	authReq, _ := http.NewRequest("GET", "https://test.e2a.dev/api/oauth/authorize?"+q.Encode(), nil)

	ar, err := provider.NewAuthorizeRequest(ctx, authReq)
	if err != nil {
		t.Fatalf("NewAuthorizeRequest unexpectedly rejected the request before PKCE check: %v", err)
	}
	ar.SetSession(&oauth.Session{UserID: userID, AgentEmail: "a@b.c", Subject: userID})
	ar.GrantScope("agent")

	_, err = provider.NewAuthorizeResponse(ctx, ar, ar.GetSession())
	if err == nil {
		t.Fatal("expected NewAuthorizeResponse to reject code_challenge_method=plain")
	}
}

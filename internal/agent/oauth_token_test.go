package agent_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/oauth"
)

// pkceVerifierKnown / pkceChallengeKnown is a verifier/challenge pair
// pre-computed at test-construction time so callers can mix them with
// stored codes without recomputing. The math:
//
//	verifier  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOP123" (43 chars)
//	challenge = base64url-nopad(sha256(verifier))
//
// Helper below derives the challenge — tests don't need to recompute.
const pkceVerifierKnown = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOP1"

func pkceChallengeFor(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// issueCodeForTest manually inserts a fresh authorization code (the
// same row /consent would create) so token-endpoint tests don't have
// to run the consent flow first.
func issueCodeForTest(t *testing.T, f *authzFixture, agentEmail, challenge string) string {
	t.Helper()
	code := oauth.NewAuthCode()
	if err := f.oauthStore.IssueCode(context.Background(), &oauth.AuthorizationCode{
		Code:                code,
		ClientID:            f.client.ClientID,
		UserID:              f.user.ID,
		AgentEmail:          agentEmail,
		RedirectURI:         testRedirectURI,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		Scope:               "e2a",
		ExpiresAt:           time.Now().Add(oauth.AuthCodeLifetime),
	}); err != nil {
		t.Fatal(err)
	}
	return code
}

// postToken posts a form to /api/oauth/token. Returns the response.
func postToken(t *testing.T, server string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.Post(server+"/api/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// decodeToken decodes a /token success body and asserts the basic
// invariants every successful response must satisfy.
func decodeToken(t *testing.T, resp *http.Response) agent.OAuthTokenResponse {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, string(body))
	}
	// RFC 6749 §5.1 requires both Cache-Control: no-store AND
	// Pragma: no-cache so old HTTP/1.0 caches don't persist tokens.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control must contain no-store per RFC 6749 §5.1: got %q", cc)
	}
	if pr := resp.Header.Get("Pragma"); pr != "no-cache" {
		t.Errorf("Pragma must be no-cache per RFC 6749 §5.1: got %q", pr)
	}
	var tr agent.OAuthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(tr.AccessToken, oauth.AccessTokenPrefix) {
		t.Errorf("access_token must have %q prefix: got %q", oauth.AccessTokenPrefix, tr.AccessToken)
	}
	if tr.TokenType != "Bearer" {
		t.Errorf("token_type must be Bearer: got %q", tr.TokenType)
	}
	// Exact match — a future regression that changed unit (ms vs s)
	// or introduced rounding would pass a > 0 check.
	wantExpiresIn := int(oauth.AccessTokenLifetime / time.Second)
	if tr.ExpiresIn != wantExpiresIn {
		t.Errorf("expires_in must equal AccessTokenLifetime in seconds: want %d, got %d", wantExpiresIn, tr.ExpiresIn)
	}
	return tr
}

// ──────────────────────── authorization_code grant ────────────────────────

func TestToken_AuthCode_Happy(t *testing.T) {
	f := newAuthzFixture(t)
	challenge := pkceChallengeFor(pkceVerifierKnown)
	code := issueCodeForTest(t, f, "ag1@agents.e2a.dev", challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", f.client.ClientID)
	form.Set("code_verifier", pkceVerifierKnown)

	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	tr := decodeToken(t, resp)

	if !strings.HasPrefix(tr.RefreshToken, oauth.RefreshTokenPrefix) {
		t.Errorf("refresh_token must have %q prefix: got %q", oauth.RefreshTokenPrefix, tr.RefreshToken)
	}
	if tr.Scope != "e2a" {
		t.Errorf("scope echo: got %q", tr.Scope)
	}
}

// TestToken_AuthCode_NoOAuthStore_404 — symmetry with discovery / DCR.
func TestToken_AuthCode_NoOAuthStore_404(t *testing.T) {
	// Build a server without an oauth store.
	server := newDiscoveryServer(t, false, "https://e2a.dev")
	resp := postToken(t, server.URL, url.Values{"grant_type": {"authorization_code"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 without oauth store, got %d", resp.StatusCode)
	}
}

func TestToken_NoGrantType_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := postToken(t, f.server.URL, url.Values{})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestToken_UnsupportedGrantType_400(t *testing.T) {
	f := newAuthzFixture(t)
	resp := postToken(t, f.server.URL, url.Values{"grant_type": {"client_credentials"}})
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "unsupported_grant_type")
}

func TestToken_AuthCode_MissingFields_400(t *testing.T) {
	f := newAuthzFixture(t)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", "oace_doesntmatter")
	// missing redirect_uri, client_id, code_verifier
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestToken_AuthCode_UnknownCode(t *testing.T) {
	f := newAuthzFixture(t)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", "oace_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", f.client.ClientID)
	form.Set("code_verifier", pkceVerifierKnown)
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_grant")
}

// TestToken_AuthCode_Reuse_RevokesTokens — RFC 6749 §10.5: replaying a
// consumed code must (a) be rejected and (b) burn all tokens the
// original code minted, since we have to assume the attacker now holds
// them.
func TestToken_AuthCode_Reuse_RevokesTokens(t *testing.T) {
	f := newAuthzFixture(t)
	challenge := pkceChallengeFor(pkceVerifierKnown)
	code := issueCodeForTest(t, f, "ag-reuse@agents.e2a.dev", challenge)

	baseForm := func() url.Values {
		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("code", code)
		form.Set("redirect_uri", testRedirectURI)
		form.Set("client_id", f.client.ClientID)
		form.Set("code_verifier", pkceVerifierKnown)
		return form
	}

	// First call: fresh.
	resp1 := postToken(t, f.server.URL, baseForm())
	tr1 := decodeToken(t, resp1)
	resp1.Body.Close()

	// Second call: must fail.
	resp2 := postToken(t, f.server.URL, baseForm())
	defer resp2.Body.Close()
	assertOAuthError(t, resp2, http.StatusBadRequest, "invalid_grant")

	// The originally-issued access token must be revoked.
	got, err := f.oauthStore.LookupTokenByAccess(context.Background(), tr1.AccessToken)
	if err != nil {
		t.Fatalf("LookupTokenByAccess: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("reuse defense: expected the first-issued token to be revoked, but it is not")
	}
}

func TestToken_AuthCode_PKCEMismatch(t *testing.T) {
	f := newAuthzFixture(t)
	challenge := pkceChallengeFor(pkceVerifierKnown)
	code := issueCodeForTest(t, f, "ag@agents.e2a.dev", challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", f.client.ClientID)
	form.Set("code_verifier", "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX") // 43 chars, wrong
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_grant")
}

func TestToken_AuthCode_ClientIDMismatch(t *testing.T) {
	f := newAuthzFixture(t)
	challenge := pkceChallengeFor(pkceVerifierKnown)
	code := issueCodeForTest(t, f, "ag@agents.e2a.dev", challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", "mcp_anotherclientx") // not f.client.ClientID
	form.Set("code_verifier", pkceVerifierKnown)
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_grant")
}

func TestToken_AuthCode_RedirectURIMismatch(t *testing.T) {
	f := newAuthzFixture(t)
	challenge := pkceChallengeFor(pkceVerifierKnown)
	code := issueCodeForTest(t, f, "ag@agents.e2a.dev", challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "https://attacker.example.com/cb")
	form.Set("client_id", f.client.ClientID)
	form.Set("code_verifier", pkceVerifierKnown)
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_grant")
}

// TestToken_AuthCode_ExpiredCode — IssueCode lets us set expires_at
// directly; we age it 2 minutes (past the 60s lifetime).
func TestToken_AuthCode_ExpiredCode(t *testing.T) {
	f := newAuthzFixture(t)
	challenge := pkceChallengeFor(pkceVerifierKnown)
	code := oauth.NewAuthCode()
	if err := f.oauthStore.IssueCode(context.Background(), &oauth.AuthorizationCode{
		Code:                code,
		ClientID:            f.client.ClientID,
		UserID:              f.user.ID,
		AgentEmail:          "ag@agents.e2a.dev",
		RedirectURI:         testRedirectURI,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		Scope:               "e2a",
		ExpiresAt:           time.Now().Add(-2 * time.Minute), // already expired
	}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", f.client.ClientID)
	form.Set("code_verifier", pkceVerifierKnown)
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_grant")
}

// ──────────────────────── refresh_token grant ────────────────────────

// mintInitialTokens runs through the auth_code path to get a fresh
// (access, refresh) pair for refresh-grant tests.
func mintInitialTokens(t *testing.T, f *authzFixture) agent.OAuthTokenResponse {
	t.Helper()
	challenge := pkceChallengeFor(pkceVerifierKnown)
	code := issueCodeForTest(t, f, "ag-refresh@agents.e2a.dev", challenge)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", testRedirectURI)
	form.Set("client_id", f.client.ClientID)
	form.Set("code_verifier", pkceVerifierKnown)
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	return decodeToken(t, resp)
}

func TestToken_Refresh_Happy(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", tr.RefreshToken)
	form.Set("client_id", f.client.ClientID)
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	newTr := decodeToken(t, resp)

	if newTr.AccessToken == tr.AccessToken {
		t.Error("access token should rotate")
	}
	if newTr.RefreshToken == tr.RefreshToken {
		t.Error("refresh token must rotate (single-use)")
	}
}

// TestToken_Refresh_Reuse_RevokesChain — RFC 6749 §10.4 reuse defense.
// After a successful rotation, the old refresh is gone (NULLed). A
// second attempt to reuse it should be rejected.
func TestToken_Refresh_Reuse_OldFails(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	// Rotate once.
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", tr.RefreshToken)
	form.Set("client_id", f.client.ClientID)
	resp := postToken(t, f.server.URL, form)
	_ = decodeToken(t, resp)
	resp.Body.Close()

	// Try to reuse the original refresh.
	resp2 := postToken(t, f.server.URL, form)
	defer resp2.Body.Close()
	assertOAuthError(t, resp2, http.StatusBadRequest, "invalid_grant")
}

func TestToken_Refresh_ClientIDMismatch_RevokesChain(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	// Register a SECOND client, then try to use refresh issued under
	// the first one. That's the "credential confusion / theft" case;
	// the original chain should be burned.
	other := &oauth.Client{
		ClientID:     oauth.NewClientID(),
		ClientName:   "Other",
		RedirectURIs: []string{"https://elsewhere.example.com/cb"},
		ClientType:   "public",
		CreatedVia:   "dcr",
	}
	if err := f.oauthStore.RegisterClient(context.Background(), other); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", tr.RefreshToken)
	form.Set("client_id", other.ClientID)
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_grant")

	// The originally-issued access token must be revoked because chain
	// got nuked.
	got, err := f.oauthStore.LookupTokenByAccess(context.Background(), tr.AccessToken)
	if err != nil {
		t.Fatalf("LookupTokenByAccess: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("client_id mismatch on refresh should revoke the chain — original access token still active")
	}
}

func TestToken_Refresh_UnknownToken(t *testing.T) {
	f := newAuthzFixture(t)
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", "rte2a_doesnotexistxxxxxxxxxxxxxxxxxxxxxx")
	form.Set("client_id", f.client.ClientID)
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_grant")
}

func TestToken_Refresh_MissingFields_400(t *testing.T) {
	f := newAuthzFixture(t)
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	// missing refresh_token + client_id
	resp := postToken(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

// ──────────────────────── PKCE helper sanity ────────────────────────

// Smoke test for the constant — verifier+challenge round-trip cleanly.
// If this fails, every other auth_code test fails noisily, so isolate
// the math here for fast diagnosis.
func TestPKCE_ChallengeDerivation_Sanity(t *testing.T) {
	chal := pkceChallengeFor(pkceVerifierKnown)
	if len(chal) != 43 {
		t.Errorf("S256 challenge must be 43 chars: got %d (%q)", len(chal), chal)
	}
	if strings.ContainsAny(chal, "=+/") {
		t.Errorf("base64url-nopad must not produce =, +, or /: got %q", chal)
	}
}

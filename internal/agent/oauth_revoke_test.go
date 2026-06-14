package agent_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// postRevoke is a small wrapper for the form-encoded POST shape RFC
// 7009 §2 requires.
func postRevoke(t *testing.T, serverURL string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.Post(serverURL+"/api/oauth/revoke",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// probeBearer reports whether the given bearer is still usable, plus
// the WWW-Authenticate challenge the API advertises when it isn't. It
// replaces the removed /api/v1/agents probe the revoke tests used to
// rely on.
//
// Two still-registered endpoints are combined because no single kept
// route both returns 200 on success AND emits the RFC 6750 §3.1 OAuth
// challenge on failure:
//
//   - GET /api/v1/pending decides the status: 200 for a valid bearer,
//     401 for a revoked/invalid one (clean 200-vs-401 signal).
//   - PATCH /api/v1/agents/{email}/messages/{id} routes its 401 through
//     writeAuthError, which sets `error="invalid_token"` for ate2a_
//     bearers — so we read the WWW-Authenticate header from there. (A
//     valid bearer 404s on the nonexistent message, carrying no
//     challenge header, which is exactly what we want: an empty string
//     for a still-valid token.)
func probeBearer(t *testing.T, serverURL, bearer string) (status int, wwwAuth string) {
	t.Helper()

	req, _ := http.NewRequest("GET", serverURL+"/api/v1/pending", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	status = resp.StatusCode
	resp.Body.Close()

	// Read the OAuth challenge header from an endpoint that emits it.
	chReq, _ := http.NewRequest("PATCH",
		serverURL+"/api/v1/agents/probe@example.com/messages/msg_probe", strings.NewReader("{}"))
	if bearer != "" {
		chReq.Header.Set("Authorization", "Bearer "+bearer)
	}
	chReq.Header.Set("Content-Type", "application/json")
	chResp, err := http.DefaultClient.Do(chReq)
	if err != nil {
		t.Fatal(err)
	}
	wwwAuth = chResp.Header.Get("WWW-Authenticate")
	chResp.Body.Close()

	return status, wwwAuth
}

// TestHTTP_Revoke_AccessToken: revoke a freshly-minted access token,
// then confirm a subsequent bearer call to /api/v1/agents 401s.
// End-to-end proof that /revoke actually invalidates the token.
func TestHTTP_Revoke_AccessToken(t *testing.T) {
	f := newConsentFixture(t)
	access, _ := mintTokensForFixture(t, f)

	// Sanity: token works before revoke.
	if status, _ := probeBearer(t, f.server.URL, access); status != http.StatusOK {
		t.Fatalf("pre-revoke bearer call should 200, got %d", status)
	}

	form := url.Values{}
	form.Set("token", access)
	form.Set("token_type_hint", "access_token")
	form.Set("client_id", f.clientID)
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", resp.StatusCode)
	}

	// Token now rejected, with the OAuth challenge header.
	status, wa := probeBearer(t, f.server.URL, access)
	if status != http.StatusUnauthorized {
		t.Errorf("post-revoke bearer call: status = %d, want 401", status)
	}
	if !strings.Contains(wa, `error="invalid_token"`) {
		t.Errorf("post-revoke WWW-Authenticate: got %q", wa)
	}
}

// TestHTTP_Revoke_RefreshToken: revoking the refresh cascades to the
// whole request_id family (every access issued from the same grant).
// fosite's RevokeRefreshToken + our storage's request_id index do the
// work — this test verifies the contract.
func TestHTTP_Revoke_RefreshToken(t *testing.T) {
	f := newConsentFixture(t)
	access, refresh := mintTokensForFixture(t, f)

	form := url.Values{}
	form.Set("token", refresh)
	form.Set("token_type_hint", "refresh_token")
	form.Set("client_id", f.clientID)
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh revoke status = %d, want 200", resp.StatusCode)
	}

	// The paired access token (same request_id) is now revoked too.
	status, _ := probeBearer(t, f.server.URL, access)
	if status != http.StatusUnauthorized {
		t.Errorf("refresh-revoke should cascade to access token; got %d", status)
	}
}

// TestHTTP_Revoke_UnknownToken: RFC 7009 §2.2 says the server MUST
// respond 200 to revoke of unknown tokens — to avoid revealing
// whether tokens exist.
func TestHTTP_Revoke_UnknownToken(t *testing.T) {
	f := newConsentFixture(t)
	form := url.Values{}
	form.Set("token", "ate2a_does_not_exist_xyz")
	form.Set("client_id", f.clientID)
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("unknown-token revoke must 200 per RFC 7009 §2.2: got %d", resp.StatusCode)
	}
}

// TestHTTP_Revoke_NotConfigured covered alongside the other
// "endpoint 404s when provider unwired" cases in
// oauth_discovery_test.go's TestHTTP_Discovery_NotConfigured. No need
// to repeat the bare-server scaffolding here.

// TestHTTP_Revoke_WrongClient: a different client_id cannot actually
// revoke our token, even though the HTTP response is 200. Per RFC
// 7009 §2.2, the server SHOULD NOT reveal whether the revoke
// succeeded — fosite returns 200 on cross-client attempts as a
// no-information-leak posture, but the revocation handler short-
// circuits before touching storage (handler/oauth2/revocation.go
// checks ar.GetClient().GetID() against the authenticated client and
// bails with ErrUnauthorizedClient, which WriteRevocationResponse
// then maps to 200). The real security property — the token stays
// usable — is what we assert here.
func TestHTTP_Revoke_WrongClient(t *testing.T) {
	f := newConsentFixture(t)
	access, _ := mintTokensForFixture(t, f)

	// Seed a second client.
	otherClientID := "mcp_other_revoke"
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types,
		     response_types, scopes, audiences, token_endpoint_auth_method,
		     public, created_via)
		VALUES ($1, 'other', ARRAY['http://localhost:8765/cb'],
		        ARRAY['authorization_code','refresh_token'], ARRAY['code'],
		        ARRAY['mcp'], ARRAY[]::TEXT[], 'none', TRUE, 'dcr')
		ON CONFLICT (client_id) DO NOTHING
	`, otherClientID); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("token", access)
	form.Set("client_id", otherClientID)
	resp := postRevoke(t, f.server.URL, form)
	resp.Body.Close()

	// Critical security assertion: the token must remain valid.
	// fosite's 200 vs not-200 is a UX/spec detail; what matters is
	// that a hostile client_id can't kill a token it doesn't own.
	status, _ := probeBearer(t, f.server.URL, access)
	if status != http.StatusOK {
		t.Errorf("token must remain valid after wrong-client revoke attempt: got %d", status)
	}
}

// OAuthErrorBody mirrors the RFC 6749 §5.2 error JSON shape for
// asserting error bodies. Renamed to avoid clashing with the
// production OAuthError type in oauth_handlers.go.
type OAuthErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

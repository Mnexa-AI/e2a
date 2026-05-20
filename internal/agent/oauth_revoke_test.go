package agent_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/oauth"
)

// postRevoke is a tiny convenience for the standard application/x-www-form
// shape RFC 7009 §2 requires.
func postRevoke(t *testing.T, server string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.Post(server+"/api/oauth/revoke", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestRevoke_AccessToken_Happy mints a token via auth_code grant, then
// revokes it, then verifies the access-token row is marked revoked.
func TestRevoke_AccessToken_Happy(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	form := url.Values{}
	form.Set("token", tr.AccessToken)
	form.Set("client_id", f.client.ClientID)
	form.Set("token_type_hint", "access_token")
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	got, err := f.oauthStore.LookupTokenByAccess(context.Background(), tr.AccessToken)
	if err != nil {
		t.Fatalf("LookupTokenByAccess: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("expected access token to be revoked, but RevokedAt is nil")
	}
}

// TestRevoke_RefreshToken_RevokesChain — refresh-token revocation
// must cascade to all access tokens in the chain (RFC 7009 §2 SHOULD).
func TestRevoke_RefreshToken_RevokesChain(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	// Rotate once so the chain has two members.
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", tr.RefreshToken)
	form.Set("client_id", f.client.ClientID)
	resp := postToken(t, f.server.URL, form)
	tr2 := decodeToken(t, resp)
	resp.Body.Close()

	// Revoke the newer refresh — both rows should die.
	form = url.Values{}
	form.Set("token", tr2.RefreshToken)
	form.Set("client_id", f.client.ClientID)
	form.Set("token_type_hint", "refresh_token")
	rresp := postRevoke(t, f.server.URL, form)
	defer rresp.Body.Close()
	if rresp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", rresp.StatusCode)
	}

	// Original access token (older chain member) — revoked.
	got, err := f.oauthStore.LookupTokenByAccess(context.Background(), tr.AccessToken)
	if err != nil {
		t.Fatalf("LookupTokenByAccess(old): %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("chain revoke should kill the older chain member; old access token still active")
	}
	// New access token also revoked.
	got2, err := f.oauthStore.LookupTokenByAccess(context.Background(), tr2.AccessToken)
	if err != nil {
		t.Fatalf("LookupTokenByAccess(new): %v", err)
	}
	if got2.RevokedAt == nil {
		t.Error("chain revoke should kill the newer chain member; new access token still active")
	}
}

// TestRevoke_HintWrong_StillWorks — hint is advisory. Sending
// hint=refresh_token but passing an access_token must still revoke.
func TestRevoke_HintWrong_StillWorks(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	form := url.Values{}
	form.Set("token", tr.AccessToken)
	form.Set("client_id", f.client.ClientID)
	form.Set("token_type_hint", "refresh_token") // wrong hint
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 despite wrong hint, got %d", resp.StatusCode)
	}
}

// TestRevoke_NoHint_StillWorks — many clients omit the hint entirely.
func TestRevoke_NoHint_StillWorks(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	form := url.Values{}
	form.Set("token", tr.AccessToken)
	form.Set("client_id", f.client.ClientID)
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

// TestRevoke_UnknownToken_200 — silently 200 on unknown token per
// RFC 7009 §2.2 (no leaking which tokens exist).
func TestRevoke_UnknownToken_200(t *testing.T) {
	f := newAuthzFixture(t)
	form := url.Values{}
	form.Set("token", "ate2a_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	form.Set("client_id", f.client.ClientID)
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown token must 200 (no existence leak), got %d", resp.StatusCode)
	}
}

// TestRevoke_AlreadyRevoked_200 — idempotent. Some clients call
// revoke unconditionally on logout.
func TestRevoke_AlreadyRevoked_200(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	form := url.Values{}
	form.Set("token", tr.AccessToken)
	form.Set("client_id", f.client.ClientID)

	resp1 := postRevoke(t, f.server.URL, form)
	resp1.Body.Close()
	resp2 := postRevoke(t, f.server.URL, form)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second revoke must also 200 (idempotent), got %d", resp2.StatusCode)
	}
}

// TestRevoke_ClientIDMismatch_400 — a different client must not be
// able to revoke this client's tokens. invalid_client per §2.1.
func TestRevoke_ClientIDMismatch_400(t *testing.T) {
	f := newAuthzFixture(t)
	tr := mintInitialTokens(t, f)

	other := &oauth.Client{
		ClientID:     oauth.NewClientID(),
		ClientName:   "Other",
		RedirectURIs: []string{"https://other.example.com/cb"},
		ClientType:   "public",
		CreatedVia:   "dcr",
	}
	if err := f.oauthStore.RegisterClient(context.Background(), other); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("token", tr.AccessToken)
	form.Set("client_id", other.ClientID)
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_client")

	// And the token must still be active.
	got, err := f.oauthStore.LookupTokenByAccess(context.Background(), tr.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt != nil {
		t.Error("foreign-client revoke attempt must NOT touch the token")
	}
}

func TestRevoke_MissingToken_400(t *testing.T) {
	f := newAuthzFixture(t)
	form := url.Values{}
	form.Set("client_id", f.client.ClientID)
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestRevoke_MissingClientID_400(t *testing.T) {
	f := newAuthzFixture(t)
	form := url.Values{}
	form.Set("token", "ate2a_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	resp := postRevoke(t, f.server.URL, form)
	defer resp.Body.Close()
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_request")
}

func TestRevoke_NoOAuthStore_404(t *testing.T) {
	server := newDiscoveryServer(t, false, "https://e2a.dev")
	form := url.Values{}
	form.Set("token", "ate2a_xxx")
	form.Set("client_id", "mcp_xxx")
	resp := postRevoke(t, server.URL, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 without oauth store, got %d", resp.StatusCode)
	}
}

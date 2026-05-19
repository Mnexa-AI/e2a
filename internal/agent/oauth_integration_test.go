package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/oauth"
)

// TestOAuthFlow_FullRoundtrip chains every endpoint in this PR:
// register → consent → token → authed call → refresh → revoke →
// authed call fails. The point isn't to re-test what unit tests
// already cover; it's to catch contract drift between stages — the
// kind of regression where each endpoint passes its own tests but the
// payload one stage produces no longer matches what the next stage
// expects (agent_email plumbing, scope echoing, prefix changes, etc.).
func TestOAuthFlow_FullRoundtrip(t *testing.T) {
	f := newAuthzFixture(t)
	ctx := context.Background()

	const (
		// Distinct from the fixture's preregistered redirect_uri so
		// we know we're using the DCR-issued client, not the fixture's.
		integRedirectURI = "https://client.example.com/integration-cb"
	)

	// ── stage 1: DCR ───────────────────────────────────────────────
	dcrResp := postJSON(t, f.server, "/api/oauth/register", agent.OAuthRegisterRequest{
		ClientName:   "Integration Test Client",
		RedirectURIs: []string{integRedirectURI},
	})
	if dcrResp.StatusCode != http.StatusCreated {
		t.Fatalf("DCR failed: %d", dcrResp.StatusCode)
	}
	var registered agent.OAuthRegisterResponse
	if err := json.NewDecoder(dcrResp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	dcrResp.Body.Close()

	// ── stage 2: consent (allow + create_new) ──────────────────────
	pkceVerifier := pkceVerifierKnown
	pkceChallenge := pkceChallengeFor(pkceVerifier)

	consentForm := url.Values{}
	consentForm.Set("action", "allow")
	consentForm.Set("agent_choice", "create_new")
	consentForm.Set("new_agent_slug", "integ-flow-bot")
	consentForm.Set("response_type", "code")
	consentForm.Set("client_id", registered.ClientID)
	consentForm.Set("redirect_uri", integRedirectURI)
	consentForm.Set("code_challenge", pkceChallenge)
	consentForm.Set("code_challenge_method", "S256")
	consentForm.Set("scope", "e2a")
	consentForm.Set("state", "integ-state")

	consentResp := doPOSTForm(t, f.server.URL+"/api/oauth/consent", f.sessionToken, consentForm)
	if consentResp.StatusCode != http.StatusFound {
		t.Fatalf("consent should redirect with code: got %d", consentResp.StatusCode)
	}
	consentLoc := parseLocation(t, consentResp)
	consentResp.Body.Close()
	if !strings.HasPrefix(consentLoc.String(), integRedirectURI) {
		t.Fatalf("consent must redirect to client redirect_uri: got %q", consentLoc.String())
	}
	authCodeStr := consentLoc.Query().Get("code")
	if !strings.HasPrefix(authCodeStr, oauth.AuthCodePrefix) {
		t.Fatalf("expected oace_-prefixed code in redirect: got %q", authCodeStr)
	}
	if consentLoc.Query().Get("state") != "integ-state" {
		t.Errorf("state should round-trip through consent: got %q", consentLoc.Query().Get("state"))
	}

	// Sanity: the agent was actually created on the shared domain
	// (this confirms M1's tx commit, not just the code insert).
	agentEmail := "integ-flow-bot@agents.e2a.dev"
	gotAgent, err := f.identStore.GetAgentByEmail(ctx, agentEmail)
	if err != nil {
		t.Fatalf("consent should have created the agent atomically with the code: %v", err)
	}
	if gotAgent.UserID != f.user.ID {
		t.Errorf("created agent owner: want %q, got %q", f.user.ID, gotAgent.UserID)
	}

	// ── stage 3: token exchange ────────────────────────────────────
	tokForm := url.Values{}
	tokForm.Set("grant_type", "authorization_code")
	tokForm.Set("code", authCodeStr)
	tokForm.Set("redirect_uri", integRedirectURI)
	tokForm.Set("client_id", registered.ClientID)
	tokForm.Set("code_verifier", pkceVerifier)

	tokResp := postToken(t, f.server.URL, tokForm)
	initial := decodeToken(t, tokResp)
	tokResp.Body.Close()

	// Token's agent_email should match what consent created — this is
	// the contract drift M2 most cares about. We don't surface
	// agent_email in the /token response (it's an internal field on
	// oauth_tokens), so verify via store lookup.
	storedTok, err := f.oauthStore.LookupTokenByAccess(ctx, initial.AccessToken)
	if err != nil {
		t.Fatalf("LookupTokenByAccess: %v", err)
	}
	if storedTok.AgentEmail != agentEmail {
		t.Errorf("agent_email contract drift between code and token: code=%q token=%q", agentEmail, storedTok.AgentEmail)
	}

	// ── stage 4: authed call with bearer ───────────────────────────
	if status := getAgentsWithBearer(t, f.server.URL, initial.AccessToken); status != http.StatusOK {
		t.Fatalf("bearer call with fresh access_token should 200: got %d", status)
	}

	// ── stage 5: refresh rotation ──────────────────────────────────
	refForm := url.Values{}
	refForm.Set("grant_type", "refresh_token")
	refForm.Set("refresh_token", initial.RefreshToken)
	refForm.Set("client_id", registered.ClientID)
	refResp := postToken(t, f.server.URL, refForm)
	rotated := decodeToken(t, refResp)
	refResp.Body.Close()
	if rotated.AccessToken == initial.AccessToken {
		t.Error("refresh must rotate the access token")
	}
	if rotated.RefreshToken == initial.RefreshToken {
		t.Error("refresh must rotate the refresh token (single-use)")
	}
	// Rotated tokens still authenticate.
	if status := getAgentsWithBearer(t, f.server.URL, rotated.AccessToken); status != http.StatusOK {
		t.Fatalf("bearer call with rotated access_token should 200: got %d", status)
	}

	// ── stage 6: revoke (refresh — should burn the whole chain) ────
	revForm := url.Values{}
	revForm.Set("token", rotated.RefreshToken)
	revForm.Set("client_id", registered.ClientID)
	revForm.Set("token_type_hint", "refresh_token")
	revResp := postRevoke(t, f.server.URL, revForm)
	if revResp.StatusCode != http.StatusOK {
		t.Fatalf("revoke should 200: got %d", revResp.StatusCode)
	}
	revResp.Body.Close()

	// ── stage 7: authed call now 401 (both chain members revoked) ──
	if status := getAgentsWithBearer(t, f.server.URL, initial.AccessToken); status != http.StatusUnauthorized {
		t.Errorf("original access token must be 401 after chain revoke: got %d", status)
	}
	if status := getAgentsWithBearer(t, f.server.URL, rotated.AccessToken); status != http.StatusUnauthorized {
		t.Errorf("rotated access token must be 401 after chain revoke: got %d", status)
	}
}

// getAgentsWithBearer is a thin wrapper that hits /api/v1/agents with
// a Bearer token (no body, simplest authed endpoint). Returns only the
// status — callers don't care about the response shape, just the
// auth outcome.
func getAgentsWithBearer(t *testing.T, serverURL, bearer string) int {
	t.Helper()
	req, err := http.NewRequest("GET", serverURL+"/api/v1/agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// (auth import is used implicitly via session token cookie name in
// helpers from other test files; keep it referenced.)
var _ = auth.SessionCookieName

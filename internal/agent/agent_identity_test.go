package agent_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/agentauth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Slice 5b-2 — the autonomous agent-identity flow, DB-backed end to end:
//   e2a_agt_ key → POST /agent/identity → identity_assertion
//                → POST /oauth2/token (jwt-bearer) → access_token
//                → access_token resolves to the agent principal.

type agentIDFixture struct {
	srv    *httptest.Server
	store  *identity.Store
	pool   *pgxpool.Pool
	api    *agent.API
	agent  *identity.AgentIdentity
	user   *identity.User
	apiKey string // plaintext e2a_agt_ key bound to the agent
}

func newAgentIDFixture(t *testing.T) *agentIDFixture {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)

	// Real RS256 signer — the agent-identity surface requires it.
	pemKey := testRSAPEM(t)
	signer, err := agentauth.NewSigner(pemKey, "v1")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	api.SetSigner(signer)

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner@authflow.example.com", "Owner", "google-authflow")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "authflow.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	ag, err := store.CreateAgent(ctx, "bot@authflow.example.com", "authflow.example.com", "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	key, err := store.CreateScopedAPIKey(ctx, user.ID, "runtime", identity.ScopeAgent, ag.ID, nil)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey: %v", err)
	}
	return &agentIDFixture{srv: srv, store: store, pool: pool, api: api, agent: ag, user: user, apiKey: key.PlaintextKey}
}

// TestAgentIdentity_FullFlow drives the whole autonomous path over HTTP.
func TestAgentIdentity_FullFlow(t *testing.T) {
	f := newAgentIDFixture(t)

	// 1. Bootstrap: present the e2a_agt_ key → identity_assertion.
	req, _ := http.NewRequest("POST", f.srv.URL+"/agent/identity", nil)
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("/agent/identity status %d", resp.StatusCode)
	}
	var idResp struct {
		IdentityAssertion string `json:"identity_assertion"`
		Subject           string `json:"sub"`
	}
	json.NewDecoder(resp.Body).Decode(&idResp)
	resp.Body.Close()
	if idResp.IdentityAssertion == "" || idResp.Subject != "bot@authflow.example.com" {
		t.Fatalf("bad identity response: %+v", idResp)
	}

	// 2. Exchange the assertion for an access_token (jwt-bearer).
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {idResp.IdentityAssertion}}
	resp2, err := http.PostForm(f.srv.URL+"/oauth2/token", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != 200 {
		t.Fatalf("/oauth2/token status %d", resp2.StatusCode)
	}
	var tokResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	json.NewDecoder(resp2.Body).Decode(&tokResp)
	resp2.Body.Close()
	if tokResp.AccessToken == "" || tokResp.TokenType != "Bearer" || tokResp.Scope != "agent" {
		t.Fatalf("bad token response: %+v", tokResp)
	}
	if tokResp.ExpiresIn < 60 || tokResp.ExpiresIn > 16*60 {
		t.Errorf("expires_in = %d, want ~900s", tokResp.ExpiresIn)
	}

	// 3. The access_token resolves to the bound agent principal.
	areq, _ := http.NewRequest("GET", "/", nil)
	areq.Header.Set("Authorization", "Bearer "+tokResp.AccessToken)
	p, err := f.api.AuthenticatePrincipal(areq)
	if err != nil {
		t.Fatalf("AuthenticatePrincipal(access_token): %v", err)
	}
	if p.Scope != identity.ScopeAgent || p.AgentID != "bot@authflow.example.com" || p.User.ID != f.user.ID {
		t.Errorf("principal = %+v, want agent-scoped bound to bot@authflow.example.com", p)
	}
}

// TestAgentIdentity_AccountKeyRejected: an account-scoped key cannot mint an
// identity_assertion (no single agent to assert for).
func TestAgentIdentity_AccountKeyRejected(t *testing.T) {
	f := newAgentIDFixture(t)
	acctKey, err := f.store.CreateAPIKey(context.Background(), f.user.ID, "acct", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	req, _ := http.NewRequest("POST", f.srv.URL+"/agent/identity", nil)
	req.Header.Set("Authorization", "Bearer "+acctKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("account key on /agent/identity: status %d, want 403", resp.StatusCode)
	}
}

// TestAgentIdentity_StaleAssertionRejected: bumping assertion_version (the kill
// switch) invalidates an already-issued identity_assertion at exchange time.
func TestAgentIdentity_StaleAssertionRejected(t *testing.T) {
	f := newAgentIDFixture(t)

	req, _ := http.NewRequest("POST", f.srv.URL+"/agent/identity", nil)
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	resp, _ := http.DefaultClient.Do(req)
	var idResp struct {
		IdentityAssertion string `json:"identity_assertion"`
	}
	json.NewDecoder(resp.Body).Decode(&idResp)
	resp.Body.Close()

	// Kill switch: bump the agent's assertion_version out from under the token.
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE agent_identities SET assertion_version = assertion_version + 1 WHERE id = $1`, f.agent.ID); err != nil {
		t.Fatalf("bump assertion_version: %v", err)
	}

	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {idResp.IdentityAssertion}}
	resp2, _ := http.PostForm(f.srv.URL+"/oauth2/token", form)
	defer resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Fatalf("stale assertion exchange: status %d, want 400", resp2.StatusCode)
	}
	var errResp struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp2.Body).Decode(&errResp)
	if errResp.Error != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", errResp.Error)
	}
}

// TestAgentIdentity_AccessTokenRevokedOnVersionBump: bumping assertion_version
// invalidates an already-minted access_token at the resource server on the very
// next request — revocation is instant, not bounded by the 15-min TTL (review
// fix; the agent row is loaded during resolution, so the check is free).
func TestAgentIdentity_AccessTokenRevokedOnVersionBump(t *testing.T) {
	f := newAgentIDFixture(t)

	// Mint assertion → access_token.
	req, _ := http.NewRequest("POST", f.srv.URL+"/agent/identity", nil)
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	resp, _ := http.DefaultClient.Do(req)
	var idResp struct {
		IdentityAssertion string `json:"identity_assertion"`
	}
	json.NewDecoder(resp.Body).Decode(&idResp)
	resp.Body.Close()
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {idResp.IdentityAssertion}}
	resp2, _ := http.PostForm(f.srv.URL+"/oauth2/token", form)
	var tokResp struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp2.Body).Decode(&tokResp)
	resp2.Body.Close()

	// The token works before revocation.
	areq, _ := http.NewRequest("GET", "/", nil)
	areq.Header.Set("Authorization", "Bearer "+tokResp.AccessToken)
	if _, err := f.api.AuthenticatePrincipal(areq); err != nil {
		t.Fatalf("access token should be valid pre-revocation: %v", err)
	}

	// Kill switch: bump assertion_version.
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE agent_identities SET assertion_version = assertion_version + 1 WHERE id = $1`, f.agent.ID); err != nil {
		t.Fatalf("bump: %v", err)
	}

	// The already-minted access token is now rejected immediately.
	areq2, _ := http.NewRequest("GET", "/", nil)
	areq2.Header.Set("Authorization", "Bearer "+tokResp.AccessToken)
	if _, err := f.api.AuthenticatePrincipal(areq2); err == nil {
		t.Error("access token must be revoked instantly on assertion_version bump")
	}
}

// TestAgentIdentity_TamperedAccessTokenRejected: a JWT we didn't sign (or a
// mangled one) is rejected at the resource server, not treated as an API key.
func TestAgentIdentity_TamperedAccessTokenRejected(t *testing.T) {
	f := newAgentIDFixture(t)
	// A JWT-shaped but bogus bearer.
	bogus := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhdHRhY2tlckBldmlsLmNvbSJ9.bad-signature"
	areq, _ := http.NewRequest("GET", "/", nil)
	areq.Header.Set("Authorization", "Bearer "+bogus)
	if _, err := f.api.AuthenticatePrincipal(areq); err == nil {
		t.Error("tampered JWT must be rejected, not accepted")
	}
}

// testRSAPEM generates a throwaway 2048-bit RSA key in PKCS#1 PEM.
func testRSAPEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

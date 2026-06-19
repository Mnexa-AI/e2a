package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// Slice 5a — the hard scope ceiling. This exercises the 403 matrix over the
// real v1 handlers: account-only routes reject agent-scoped credentials;
// per-agent routes pin an agent-scoped credential to its bound agent; an
// account-scoped credential passes everywhere it owns.

func scopeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	u := &identity.User{ID: "u_1", Email: "owner@acme.com"}
	deps := Deps{
		// Scope-aware auth keyed by bearer:
		//   acct        → account scope
		//   agtSupport  → agent scope bound to support@acme.com
		//   agtOther    → agent scope bound to other@acme.com
		PrincipalAuthenticator: func(r *http.Request) (*identity.Principal, error) {
			switch r.Header.Get("Authorization") {
			case "Bearer acct":
				return &identity.Principal{User: u, Scope: identity.ScopeAccount}, nil
			case "Bearer agtSupport":
				return &identity.Principal{User: u, Scope: identity.ScopeAgent, AgentID: "support@acme.com"}, nil
			case "Bearer agtOther":
				return &identity.Principal{User: u, Scope: identity.ScopeAgent, AgentID: "other@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		ListAgents: func(ctx context.Context, userID string) ([]identity.AgentIdentity, error) {
			return []identity.AgentIdentity{sampleAgent()}, nil
		},
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			switch address {
			case "support@acme.com":
				a := sampleAgent()
				return &a, nil
			case "other@acme.com":
				a := sampleAgent()
				a.ID = "other@acme.com"
				return &a, nil
			}
			return nil, errors.New("not found")
		},
		UpdateAgentHITL: func(ctx context.Context, agentID, userID string, enabled bool, ttl int, action string) error {
			return nil
		},
		DeleteAgent: func(ctx context.Context, agentID, userID string) error { return nil },
		Legacy:      http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)
	return srv
}

func TestScope_AccountOnlyRoutesRejectAgentKeys(t *testing.T) {
	srv := scopeTestServer(t)

	cases := []struct {
		name, method, path, bearer string
		wantStatus                 int
	}{
		// List agents is account-admin discovery.
		{"list/account-ok", "GET", "/v1/agents", "acct", 200},
		{"list/agent-403", "GET", "/v1/agents", "agtSupport", 403},
		// Delete agent is admin even on the bound agent.
		{"delete/account-ok", "DELETE", "/v1/agents/support%40acme.com", "acct", 204},
		{"delete/agent-bound-403", "DELETE", "/v1/agents/support%40acme.com", "agtSupport", 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := sendJSON(t, c.method, srv.URL+c.path, c.bearer, nil)
			if code != c.wantStatus {
				t.Fatalf("%s %s as %s: status %d, want %d (body %v)", c.method, c.path, c.bearer, code, c.wantStatus, body)
			}
			if c.wantStatus == 403 && errCode(body) != "forbidden" {
				t.Errorf("expected error code 'forbidden', got %v", body)
			}
		})
	}
}

// TestScope_UpdateAgentIsAccountOnly: mutating agent config is barred for an
// agent-scoped credential even on its own bound agent.
func TestScope_UpdateAgentIsAccountOnly(t *testing.T) {
	srv := scopeTestServer(t)
	body := map[string]any{"hitl_enabled": true, "hitl_ttl_seconds": 3600, "hitl_expiration_action": "reject"}

	code, _ := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com", "acct", body)
	if code != 200 {
		t.Errorf("account key PATCH agent: status %d, want 200", code)
	}
	code, resp := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com", "agtSupport", body)
	if code != 403 || errCode(resp) != "forbidden" {
		t.Errorf("agent key PATCH own agent: status %d body %v, want 403 forbidden", code, resp)
	}
}

// TestScope_AgentKeyPinnedToBoundAgent: a per-agent runtime route lets an
// agent-scoped credential act as its bound agent but 403s on any other agent;
// an account-scoped credential reaches both.
func TestScope_AgentKeyPinnedToBoundAgent(t *testing.T) {
	srv := scopeTestServer(t)

	cases := []struct {
		name, path, bearer string
		wantStatus         int
	}{
		{"bound-agent-ok", "/v1/agents/support%40acme.com", "agtSupport", 200},
		{"other-agent-403", "/v1/agents/other%40acme.com", "agtSupport", 403},
		{"account-reaches-support", "/v1/agents/support%40acme.com", "acct", 200},
		{"account-reaches-other", "/v1/agents/other%40acme.com", "acct", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := sendJSON(t, "GET", srv.URL+c.path, c.bearer, nil)
			if code != c.wantStatus {
				t.Fatalf("GET %s as %s: status %d, want %d (body %v)", c.path, c.bearer, code, c.wantStatus, body)
			}
		})
	}
}

// TestScope_LegacyAuthenticatorIsAccount: with only the legacy Authenticator
// wired (no PrincipalAuthenticator), every caller is treated as account-scoped
// — the pre-Slice-5a behavior, so the ceiling never falsely 403s old deployments.
func TestScope_LegacyAuthenticatorIsAccount(t *testing.T) {
	deps := Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		ListAgents: func(ctx context.Context, userID string) ([]identity.AgentIdentity, error) {
			return []identity.AgentIdentity{sampleAgent()}, nil
		},
		Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)

	code, body := sendJSON(t, "GET", srv.URL+"/v1/agents", "good", nil)
	if code != 200 {
		t.Fatalf("legacy authenticator on account route: status %d, want 200 (body %v)", code, body)
	}
}

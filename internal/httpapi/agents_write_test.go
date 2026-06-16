package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

func sendJSON(t *testing.T, method, url, bearer string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUpdateAgentHITL(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com", "good", map[string]any{
		"hitl_enabled": true, "hitl_ttl_seconds": 3600, "hitl_expiration_action": "reject",
	})
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	// Returns the reloaded agent.
	if body["email"] != "support@acme.com" {
		t.Fatalf("expected reloaded agent, got %v", body)
	}
}

// TestUpdateAgentInboundPolicy exercises the full PATCH → store → re-read →
// AgentView round-trip for the inbound ingestion gate (Slice 7). The fake
// store mutates a captured agent so GetAgent returns the post-update shape.
func TestUpdateAgentInboundPolicy(t *testing.T) {
	ag := sampleAgent()
	ag.InboundPolicy = "open"
	deps := Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "support@acme.com" {
				a := ag
				return &a, nil
			}
			return nil, errors.New("not found")
		},
		UpdateAgentInboundPolicy: func(ctx context.Context, agentID, userID, policy string, allowlist []string) error {
			ag.InboundPolicy = policy
			ag.InboundAllowlist = allowlist
			return nil
		},
		Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)

	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com", "good", map[string]any{
		"inbound_policy": "verified_only",
	})
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["inbound_policy"] != "verified_only" {
		t.Fatalf("expected inbound_policy=verified_only on AgentView, got %v", body["inbound_policy"])
	}
}

// TestUpdateAgentHITLMode exercises the PATCH → store → AgentView round-trip
// for the Slice 7b action-gate sub-mode, and the 400 on an invalid mode.
func TestUpdateAgentHITLMode(t *testing.T) {
	ag := sampleAgent()
	ag.HITLMode = "all"
	deps := Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "support@acme.com" {
				a := ag
				return &a, nil
			}
			return nil, errors.New("not found")
		},
		UpdateAgentHITLMode: func(ctx context.Context, agentID, userID, mode string) error {
			if mode != "all" && mode != "high_impact" {
				return errors.New("invalid hitl_mode " + mode)
			}
			ag.HITLMode = mode
			return nil
		},
		Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)

	// Valid mode round-trips onto the AgentView.
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com", "good", map[string]any{"hitl_mode": "high_impact"})
	if code != 200 || body["hitl_mode"] != "high_impact" {
		t.Fatalf("expected 200 + hitl_mode=high_impact, got %d %v", code, body)
	}
	// Invalid mode → 400 invalid_request.
	code, body = sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com", "good", map[string]any{"hitl_mode": "bogus"})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("expected 400 invalid_request for bogus mode, got %d %v", code, body)
	}
}

func TestUpdateAgentNoFields(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com", "good", map[string]any{})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request, got %d %v", code, body)
	}
}

func TestUpdateAgentNotOwned(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "PATCH", srv.URL+"/v1/agents/other%40acme.com", "good", map[string]any{
		"hitl_enabled": true,
	})
	if code != 403 {
		t.Fatalf("want 403, got %d", code)
	}
}

func TestDeleteAgent(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "DELETE", srv.URL+"/v1/agents/support%40acme.com", "good", nil)
	if code != 200 || body["status"] != "deleted" {
		t.Fatalf("want 200 deleted, got %d %v", code, body)
	}
}

func TestDeleteAgentNotOwned(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/agents/other%40acme.com", "good", nil)
	if code != 403 {
		t.Fatalf("want 403, got %d", code)
	}
}

func postJSON(t *testing.T, url, bearer string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		if c, ok := e["code"].(string); ok {
			return c
		}
	}
	return ""
}

func TestCreateAgentHappyPath(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "bot@acme.com", "name": "Bot",
	})
	if code != 201 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["id"] != "bot@acme.com" || body["domain"] != "acme.com" {
		t.Fatalf("unexpected create response: %v", body)
	}
}

func TestCreateAgentUnverifiedDomain(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "bot@pending.com",
	})
	if code != 400 || errCode(body) != "domain_not_verified" {
		t.Fatalf("want 400 domain_not_verified, got %d %v", code, body)
	}
}

func TestCreateAgentUnregisteredDomain(t *testing.T) {
	srv := testServer(t)
	// The security-critical guard: an agent cannot be created on a domain
	// the caller has not registered + verified.
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "bot@someone-elses.com",
	})
	if code != 400 || errCode(body) != "domain_not_registered" {
		t.Fatalf("want 400 domain_not_registered, got %d %v", code, body)
	}
}

func TestCreateAgentDuplicate(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "dupe@acme.com",
	})
	if code != 409 || errCode(body) != "conflict" {
		t.Fatalf("want 409 conflict, got %d %v", code, body)
	}
}

func TestCreateAgentLimitExceeded(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "overcap", map[string]any{
		"email": "bot@acme.com",
	})
	if code != 402 || errCode(body) != "limit_exceeded" {
		t.Fatalf("want 402 limit_exceeded, got %d %v", code, body)
	}
	// The structured cap details ride in the envelope.
	if e, _ := body["error"].(map[string]any); e != nil {
		if d, _ := e["details"].(map[string]any); d == nil || d["resource"] != "agents" {
			t.Fatalf("missing limit details: %v", body)
		}
	}
}

func TestCreateAgentUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/agents", "", map[string]any{
		"email": "bot@acme.com",
	})
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}

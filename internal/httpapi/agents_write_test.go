package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
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

// TestUpdateAgentName exercises the post-reshape agent PATCH: the only mutable
// field is the display name (screening config moved to /protection).
func TestUpdateAgentName(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com", "good", map[string]any{
		"name": "Renamed Support",
	})
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	// Returns the reloaded agent.
	if body["email"] != "support@acme.com" {
		t.Fatalf("expected reloaded agent, got %v", body)
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
		"name": "x",
	})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestDeleteAgent(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "DELETE", srv.URL+"/v1/agents/support%40acme.com?confirm=DELETE", "good", nil)
	if code != 200 {
		t.Fatalf("want 200, got %d %v", code, body)
	}
	// Uniform deletion object: {deleted:true, email, messages_deleted}.
	if body["deleted"] != true {
		t.Fatalf("want deleted:true, got %v", body)
	}
	if body["email"] != "support@acme.com" {
		t.Fatalf("want email echo, got %v", body)
	}
	// Soft delete preserves messages in trash, so no rows are removed.
	if body["messages_deleted"] != float64(0) {
		t.Fatalf("want messages_deleted:0, got %v", body)
	}
}

func TestDeleteAgentNotOwned(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/agents/other%40acme.com?confirm=DELETE", "good", nil)
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
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
	if body["email"] != "bot@acme.com" || body["domain"] != "acme.com" {
		t.Fatalf("unexpected create response: %v", body)
	}
	if _, hasID := body["id"]; hasID {
		t.Fatalf("AgentView must not carry a redundant id (email is the identity): %v", body)
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

// TestCreateAgentSubdomainCoveredByVerifiedParent: an agent on a subdomain of a
// verified parent domain is created and BOUND to the parent domain (which drives
// DKIM signing, sending status, and quota), while keeping its full subdomain
// address. No exact registration for the subdomain is required.
func TestCreateAgentSubdomainCoveredByVerifiedParent(t *testing.T) {
	srv := testServer(t, func(d *Deps) {
		d.LookupCoveringDomain = func(ctx context.Context, sub, userID string) (*identity.Domain, error) {
			if sub == "acme.team.mnexa.ai" {
				return &identity.Domain{Domain: "team.mnexa.ai", Verified: true}, nil
			}
			return nil, errors.New("no covering domain")
		}
	})
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "otto@acme.team.mnexa.ai",
	})
	if code != 201 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["email"] != "otto@acme.team.mnexa.ai" {
		t.Fatalf("email = %v, want the full subdomain address", body["email"])
	}
	// The load-bearing assertion: the agent is stored under the PARENT domain
	// so the FK, quota JOIN, DKIM signer, and sending-status lookup all resolve.
	if body["domain"] != "team.mnexa.ai" {
		t.Fatalf("subdomain agent must bind to verified parent domain, got %v", body["domain"])
	}
}

// TestCreateAgentSubdomainNoCoveringParent: even with the covering lookup wired,
// a subdomain that no verified parent covers is still rejected — the ownership
// guard is not weakened.
func TestCreateAgentSubdomainNoCoveringParent(t *testing.T) {
	srv := testServer(t, func(d *Deps) {
		d.LookupCoveringDomain = func(ctx context.Context, sub, userID string) (*identity.Domain, error) {
			return nil, errors.New("no covering domain")
		}
	})
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "bot@uncovered.example.org",
	})
	if code != 400 || errCode(body) != "domain_not_registered" {
		t.Fatalf("want 400 domain_not_registered, got %d %v", code, body)
	}
}

// TestCreateAgentSubdomainLabelBoundaryRejected: the handler surfaces the
// store's label-boundary rejection (evilteam.mnexa.ai is NOT a child of the
// registered team.mnexa.ai) as domain_not_registered. The load-bearing
// label-matching security proof lives in identity.TestLookupCoveringDomain_
// LabelBoundaryRejection; this pins the handler mapping.
func TestCreateAgentSubdomainLabelBoundaryRejected(t *testing.T) {
	srv := testServer(t, func(d *Deps) {
		d.LookupCoveringDomain = func(ctx context.Context, sub, userID string) (*identity.Domain, error) {
			if sub == "acme.team.mnexa.ai" {
				return &identity.Domain{Domain: "team.mnexa.ai", Verified: true}, nil
			}
			// evilteam.mnexa.ai shares a string suffix but is not a label child.
			return nil, errors.New("no covering domain")
		}
	})
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "otto@evilteam.mnexa.ai",
	})
	if code != 400 || errCode(body) != "domain_not_registered" {
		t.Fatalf("SECURITY: evilteam.mnexa.ai must be rejected, got %d %v", code, body)
	}
}

// TestCreateAgentExactUnverifiedNotMaskedByParent pins resolution precedence
// (trap #3): an EXACT registered-but-unverified row wins over a verified parent
// — the parent fallback must not mask it. The user must verify the exact domain,
// not silently inherit the parent's identity.
func TestCreateAgentExactUnverifiedNotMaskedByParent(t *testing.T) {
	srv := testServer(t, func(d *Deps) {
		d.LookupDomain = func(ctx context.Context, domain, userID string) (*identity.Domain, error) {
			if domain == "sub.mnexa.ai" {
				return &identity.Domain{Domain: domain, Verified: false}, nil // exact row, unverified
			}
			return nil, errors.New("not registered")
		}
		d.LookupCoveringDomain = func(ctx context.Context, sub, userID string) (*identity.Domain, error) {
			// A verified parent EXISTS — precedence must still reject on the
			// exact unverified row rather than fall through to this.
			return &identity.Domain{Domain: "mnexa.ai", Verified: true}, nil
		}
	})
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "bot@sub.mnexa.ai",
	})
	if code != 400 || errCode(body) != "domain_not_verified" {
		t.Fatalf("exact unverified row must yield domain_not_verified (not masked by parent), got %d %v", code, body)
	}
}

func TestCreateAgentDuplicate(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "dupe@acme.com",
	})
	if code != 409 || errCode(body) != "agent_taken" {
		t.Fatalf("want 409 agent_taken, got %d %v", code, body)
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

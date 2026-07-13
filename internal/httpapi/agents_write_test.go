package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
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
	// The fake deps report 3 cascaded messages (operations_test.go).
	if body["messages_deleted"] != float64(3) {
		t.Fatalf("want messages_deleted:3, got %v", body)
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

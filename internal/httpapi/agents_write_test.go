package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

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
		"email": "bot@acme.com", "name": "Bot", "agent_mode": "local",
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
		"email": "bot@pending.com", "agent_mode": "local",
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
		"email": "bot@someone-elses.com", "agent_mode": "local",
	})
	if code != 400 || errCode(body) != "domain_not_registered" {
		t.Fatalf("want 400 domain_not_registered, got %d %v", code, body)
	}
}

func TestCreateAgentMissingMode(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{"email": "bot@acme.com"})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request, got %d %v", code, body)
	}
}

func TestCreateAgentCloudRequiresWebhook(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "bot@acme.com", "agent_mode": "cloud",
	})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request, got %d %v", code, body)
	}
}

func TestCreateAgentRejectsSSRFWebhook(t *testing.T) {
	srv := testServer(t)
	// http (not https) must be rejected by the reused SSRF validator.
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "bot@acme.com", "agent_mode": "cloud", "webhook_url": "http://example.com/hook",
	})
	if code != 400 || errCode(body) != "invalid_webhook_url" {
		t.Fatalf("want 400 invalid_webhook_url, got %d %v", code, body)
	}
}

func TestCreateAgentDuplicate(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"email": "dupe@acme.com", "agent_mode": "local",
	})
	if code != 409 || errCode(body) != "conflict" {
		t.Fatalf("want 409 conflict, got %d %v", code, body)
	}
}

func TestCreateAgentLimitExceeded(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents", "overcap", map[string]any{
		"email": "bot@acme.com", "agent_mode": "local",
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
		"email": "bot@acme.com", "agent_mode": "local",
	})
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}

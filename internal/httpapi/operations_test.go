package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// sampleAgent is the canonical fixture agent owned by user u_1.
func sampleAgent() identity.AgentIdentity {
	return identity.AgentIdentity{
		ID:                   "support@acme.com",
		Domain:               "acme.com",
		Name:                 "Acme Support",
		AgentMode:            "cloud",
		DomainVerified:       true,
		UserID:               "u_1",
		CreatedAt:            time.Unix(1700000000, 0).UTC(),
		HITLEnabled:          true,
		HITLTTLSeconds:       604800,
		HITLExpirationAction: "reject",
	}
}

// testServer builds a Server with fake collaborators and a sentinel legacy
// handler, returning an httptest server so tests exercise the real chi+Huma
// stack over the wire (transport layer in scope per the implement skill).
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	deps := Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		ListAgents: func(ctx context.Context, userID string) ([]identity.AgentIdentity, error) {
			if userID != "u_1" {
				return nil, errors.New("unexpected user")
			}
			return []identity.AgentIdentity{sampleAgent()}, nil
		},
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "support@acme.com" {
				a := sampleAgent()
				return &a, nil
			}
			return nil, errors.New("not found")
		},
		SharedDomain: "agents.e2a.dev",
		PublicURL:    "https://api.e2a.dev",
		Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("legacy:" + r.URL.Path))
		}),
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetInfo(t *testing.T) {
	srv := testServer(t)
	resp, err := http.Get(srv.URL + "/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("missing X-Request-Id header")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
	var body DeploymentInfoView
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SharedDomain != "agents.e2a.dev" || !body.SlugRegistrationEnabled || body.PublicURL != "https://api.e2a.dev" {
		t.Fatalf("unexpected info: %+v", body)
	}
}

func TestListAgentsAuthorized(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, b)
	}
	var body struct {
		Agents []AgentView `json:"agents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Agents) != 1 {
		t.Fatalf("want 1 agent, got %d", len(body.Agents))
	}
	a := body.Agents[0]
	if a.Email != "support@acme.com" || a.Domain != "acme.com" || a.AgentMode != "cloud" || !a.DomainVerified {
		t.Fatalf("unexpected agent view: %+v", a)
	}
}

func TestGetAgentOwned(t *testing.T) {
	srv := testServer(t)
	// The address is URL-encoded in the path (@ -> %40); the real chi+Huma
	// stack must decode it before lookup.
	req, _ := http.NewRequest("GET", srv.URL+"/v1/agents/support%40acme.com", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, b)
	}
	var a AgentView
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	if a.Email != "support@acme.com" || a.Name != "Acme Support" {
		t.Fatalf("unexpected agent: %+v", a)
	}
}

func TestGetAgentForbiddenWhenUnknown(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/agents/other%40acme.com", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Mirrors legacy: unknown/non-owned agent -> 403, not 404.
	if resp.StatusCode != 403 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Error.Code != "forbidden" {
		t.Fatalf("want code forbidden, got %q", env.Error.Code)
	}
}

func TestListAgentsUnauthorizedEnvelope(t *testing.T) {
	srv := testServer(t)
	resp, err := http.Get(srv.URL + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	headerID := resp.Header.Get("X-Request-Id")
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "unauthorized" {
		t.Fatalf("want code unauthorized, got %q", env.Error.Code)
	}
	if env.Error.RequestID == "" || env.Error.RequestID != headerID {
		t.Fatalf("request_id body=%q header=%q must match and be non-empty", env.Error.RequestID, headerID)
	}
}

func TestRequestIDPropagation(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/info", nil)
	req.Header.Set("X-Request-Id", "req_caller_supplied")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Request-Id"); got != "req_caller_supplied" {
		t.Fatalf("request id not propagated: %q", got)
	}
}

func TestLegacyFallback(t *testing.T) {
	srv := testServer(t)
	// A route the v1 layer does not own must fall through to the legacy
	// handler unchanged (strangler) — and still carry the new request id.
	resp, err := http.Get(srv.URL + "/api/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("expected legacy 418, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "legacy:/api/v1/agents" {
		t.Fatalf("unexpected legacy body: %s", b)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("legacy fallback should still carry X-Request-Id")
	}
}

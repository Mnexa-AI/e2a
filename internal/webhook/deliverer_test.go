package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

func TestDeliverSuccess(t *testing.T) {
	var receivedBody []byte
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	d := NewDeliverer(false)
	agent := &identity.AgentIdentity{
		WebhookURL: server.URL,
	}
	payload := Payload{
		From:       "alice@example.com",
		To:         []string{"bot@agent.example.com"},
		Recipient:  "bot@agent.example.com",
		RawMessage: []byte("test message"),
		AuthHeaders: map[string]string{
			"X-E2A-Auth-Verified": "true",
			"X-E2A-Auth-Sender":  "alice@example.com",
		},
		ReceivedAt: time.Now(),
	}

	err := d.Deliver(context.Background(), agent, payload)
	if err != nil {
		t.Fatalf("Deliver failed: %v", err)
	}

	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", receivedHeaders.Get("Content-Type"))
	}
	if receivedHeaders.Get("X-E2A-Auth-Verified") != "true" {
		t.Error("expected auth header to be forwarded")
	}
	if dep := receivedHeaders.Get("X-E2A-Deprecation"); dep == "" {
		t.Error("expected X-E2A-Deprecation header on legacy webhook delivery")
	}
	if sunset := receivedHeaders.Get("Sunset"); sunset == "" {
		t.Error("expected Sunset header on legacy webhook delivery")
	}

	// Verify the body is valid JSON
	var p Payload
	if err := json.Unmarshal(receivedBody, &p); err != nil {
		t.Fatalf("failed to unmarshal received body: %v", err)
	}
	if p.From != "alice@example.com" {
		t.Errorf("From = %q, want alice@example.com", p.From)
	}
}

func TestDeliverHTTPSRequired(t *testing.T) {
	d := NewDeliverer(true)
	agent := &identity.AgentIdentity{
		WebhookURL: "http://insecure.example.com/webhook",
	}
	payload := Payload{From: "test@test.com", To: []string{"bot@test.com"}, Recipient: "bot@test.com"}

	err := d.Deliver(context.Background(), agent, payload)
	if err == nil {
		t.Error("expected error for HTTP URL with requireHTTPS=true")
	}
	if err != nil && !contains(err.Error(), "HTTPS") {
		t.Errorf("expected error about HTTPS, got: %v", err)
	}
}

func TestDeliverServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	d := NewDeliverer(false)
	agent := &identity.AgentIdentity{WebhookURL: server.URL}
	payload := Payload{From: "test@test.com", To: []string{"bot@test.com"}, Recipient: "bot@test.com"}

	err := d.Deliver(context.Background(), agent, payload)
	if err == nil {
		t.Error("expected error for 500 response")
	}
	if err != nil && !contains(err.Error(), "500") {
		t.Errorf("expected error about status 500, got: %v", err)
	}
}

// TestDeliverRefusesRedirect ensures the deliverer never follows HTTP
// redirects. Following them would let an attacker who controls a
// public webhook host point delivery at an internal IP (SSRF).
func TestDeliverRefusesRedirect(t *testing.T) {
	var redirectFollowed bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectFollowed = true
		w.WriteHeader(200)
	}))
	defer internal.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	d := NewDeliverer(false)
	agent := &identity.AgentIdentity{WebhookURL: redirector.URL}
	payload := Payload{From: "test@test.com", To: []string{"bot@test.com"}, Recipient: "bot@test.com"}

	err := d.Deliver(context.Background(), agent, payload)
	if err == nil {
		t.Error("expected error when webhook responds with redirect")
	}
	if redirectFollowed {
		t.Error("deliverer followed redirect to internal target — SSRF protection broken")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

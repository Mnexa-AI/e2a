package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
)

func setupAPI(t *testing.T) (*httptest.Server, *identity.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotency.NewStore(pool))
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, store, pool
}

// createTestUser creates a user and API key, returning the bearer token for authenticated requests.
func createTestUser(t *testing.T, store *identity.Store, email string) string {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, email, "Test User", "google-"+email)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	key, err := store.CreateAPIKey(ctx, user.ID, "test-key-"+email, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return key.PlaintextKey
}

// authedPost sends an authenticated POST request with the given API key.
func authedPost(t *testing.T, url, payload, apiKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", url, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHealthEndpoint(t *testing.T) {
	server, _, _ := setupAPI(t)
	resp, err := http.Get(server.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
}

func setupAPIWithSMTP(t *testing.T) (*httptest.Server, *identity.Store, *pgxpool.Pool, func() []testutil.SMTPMessage) {
	t.Helper()
	smtpAddr, smtpDone := testutil.FakeSMTPServer(t)
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{Host: smtpAddr.Host, Port: smtpAddr.Port})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotency.NewStore(pool))
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, store, pool, smtpDone
}

// ============================================================
// Feedback endpoint tests
// ============================================================

func TestFeedback_ValidSubmission(t *testing.T) {
	server, _, _ := setupAPI(t)

	payload := `{"email":"user@example.com","category":"bug","message":"Something is broken"}`
	resp, err := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Without GITHUB_FEEDBACK_TOKEN, should still return 200 (graceful fallback)
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
}

func TestFeedback_EmptyMessage(t *testing.T) {
	server, _, _ := setupAPI(t)

	payload := `{"email":"user@example.com","category":"bug","message":""}`
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestFeedback_WhitespaceOnlyMessage(t *testing.T) {
	server, _, _ := setupAPI(t)

	payload := `{"email":"","category":"general","message":"   \n\t  "}`
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for whitespace-only message", resp.StatusCode)
	}
}

func TestFeedback_InvalidCategory(t *testing.T) {
	server, _, _ := setupAPI(t)

	payload := `{"message":"hello","category":"invalid"}`
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for invalid category", resp.StatusCode)
	}
}

func TestFeedback_DefaultCategory(t *testing.T) {
	server, _, _ := setupAPI(t)

	// No category provided — should default to "general" and succeed
	payload := `{"message":"just a thought"}`
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (default category)", resp.StatusCode)
	}
}

func TestFeedback_AllCategories(t *testing.T) {
	server, _, _ := setupAPI(t)

	for _, cat := range []string{"bug", "feature", "general"} {
		t.Run(cat, func(t *testing.T) {
			payload := fmt.Sprintf(`{"message":"test %s","category":"%s"}`, cat, cat)
			resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				t.Errorf("status = %d, want 200 for category %s", resp.StatusCode, cat)
			}
		})
	}
}

func TestFeedback_InvalidJSON(t *testing.T) {
	server, _, _ := setupAPI(t)

	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(`not json`))
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for invalid JSON", resp.StatusCode)
	}
}

func TestFeedback_MessageTooLong(t *testing.T) {
	server, _, _ := setupAPI(t)

	msg := bytes.Repeat([]byte("a"), 5001)
	payload := fmt.Sprintf(`{"message":"%s"}`, string(msg))
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for message too long", resp.StatusCode)
	}
}

func TestFeedback_RateLimit(t *testing.T) {
	server, _, _ := setupAPI(t)

	// Send 10 requests (the limit)
	for i := 0; i < 10; i++ {
		payload := fmt.Sprintf(`{"message":"feedback %d"}`, i)
		resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
	}

	// 11th request should be rate limited
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(`{"message":"one too many"}`))
	defer resp.Body.Close()

	if resp.StatusCode != 429 {
		t.Errorf("status = %d, want 429 for rate-limited request", resp.StatusCode)
	}
}

func TestFeedback_OptionalEmail(t *testing.T) {
	server, _, _ := setupAPI(t)

	// No email field at all — should succeed
	payload := `{"message":"anonymous feedback","category":"general"}`
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 for feedback without email", resp.StatusCode)
	}
}

func TestFeedback_EmailTooLong(t *testing.T) {
	server, _, _ := setupAPI(t)

	longEmail := string(bytes.Repeat([]byte("a"), 255)) + "@example.com"
	payload := fmt.Sprintf(`{"message":"test","email":"%s"}`, longEmail)
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for email too long", resp.StatusCode)
	}
}

func TestFeedback_OversizedBody(t *testing.T) {
	server, _, _ := setupAPI(t)

	// Send a body larger than 64KB to trigger MaxBytesReader
	hugeMsg := string(bytes.Repeat([]byte("x"), 70*1024))
	payload := fmt.Sprintf(`{"message":"%s"}`, hugeMsg)
	resp, _ := http.Post(server.URL+"/api/feedback", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for oversized body", resp.StatusCode)
	}
}

func TestFeedback_MethodNotAllowed(t *testing.T) {
	server, _, _ := setupAPI(t)

	req, _ := http.NewRequest("GET", server.URL+"/api/feedback", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 405 {
		t.Errorf("status = %d, want 405 for GET on feedback endpoint", resp.StatusCode)
	}
}

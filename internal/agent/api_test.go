package agent_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func setupAPI(t *testing.T) (*httptest.Server, *identity.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
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

func TestRegisterAgent(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "owner@example.com")

	// Custom domain must be registered and verified before agent registration
	ctx := context.Background()
	user, _ := store.GetUserByAPIKey(ctx, apiKey)
	store.ClaimOrCreateDomain(ctx, "reg.example.com", user.ID)
	store.VerifyDomain(ctx, "reg.example.com", user.ID)

	payload := `{"email":"agent@reg.example.com","webhook_url":"https://example.com/webhook"}`
	resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)

	if body["id"] == "" {
		t.Error("expected non-empty id")
	}
	if body["domain"] == "" {
		t.Error("expected non-empty domain")
	}
	if body["email"] == "" {
		t.Error("expected non-empty email")
	}
}

func TestRegisterAgentMissingFields(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "missing@example.com")

	resp := authedPost(t, server.URL+"/api/v1/agents", `{"email":""}`, apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRegisterAgentUnauthenticated(t *testing.T) {
	server, _, _ := setupAPI(t)

	payload := `{"email":"agent@unauth.example.com","webhook_url":"https://example.com/webhook"}`
	resp, _ := http.Post(server.URL+"/api/v1/agents", "application/json", bytes.NewBufferString(payload))
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestRegisterAgentSSRF(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "ssrf@example.com")

	tests := []struct {
		name       string
		webhookURL string
	}{
		{"HTTP URL", "http://example.com/webhook"},
		{"Raw IP", "https://127.0.0.1/webhook"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := `{"email":"agent@ssrf-` + tt.name + `.example.com","webhook_url":"` + tt.webhookURL + `"}`
			resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
			defer resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Errorf("status = %d, want 400 for %s", resp.StatusCode, tt.name)
			}
		})
	}
}

func TestRegisterAgentDuplicate(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "dup@example.com")

	ctx := context.Background()
	user, _ := store.GetUserByAPIKey(ctx, apiKey)
	store.ClaimOrCreateDomain(ctx, "dup-api.example.com", user.ID)
	store.VerifyDomain(ctx, "dup-api.example.com", user.ID)

	payload := `{"email":"agent@dup-api.example.com","webhook_url":"https://example.com/webhook"}`
	resp1 := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	resp1.Body.Close()

	resp2 := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	defer resp2.Body.Close()
	if resp2.StatusCode != 409 {
		t.Errorf("status = %d, want 409", resp2.StatusCode)
	}
}

func TestGetAgentAuth(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	// Create user and API key
	user, _ := store.CreateOrGetUser(ctx, "owner-auth@example.com", "Owner", "google-auth")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "auth-key", nil)

	// Register agent owned by this user
	store.ClaimOrCreateDomain(ctx, "auth.example.com", user.ID)
	store.VerifyDomain(ctx, "auth.example.com", user.ID)
	store.CreateAgent(ctx, "agent@auth.example.com", "auth.example.com", "", "https://example.com/webhook", "", user.ID)

	// GET with valid key
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent%40auth.example.com", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != 200 {
		t.Errorf("valid key: status = %d, want 200", resp2.StatusCode)
	}
	resp2.Body.Close()

	// GET with no key
	req3, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent%40auth.example.com", nil)
	resp3, _ := http.DefaultClient.Do(req3)
	if resp3.StatusCode != 401 {
		t.Errorf("no key: status = %d, want 401", resp3.StatusCode)
	}
	resp3.Body.Close()

	// GET with wrong key
	req4, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent%40auth.example.com", nil)
	req4.Header.Set("Authorization", "Bearer wrong-key")
	resp4, _ := http.DefaultClient.Do(req4)
	if resp4.StatusCode != 401 {
		t.Errorf("wrong key: status = %d, want 401", resp4.StatusCode)
	}
	resp4.Body.Close()
}

func TestSendEmailUnverified(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	// Create user and API key, register agent (not domain-verified)
	user, _ := store.CreateOrGetUser(ctx, "owner-unverified@example.com", "Owner", "google-unverified-send")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "unverified-key", nil)
	store.ClaimOrCreateDomain(ctx, "unverified-send.example.com", user.ID)
	store.CreateAgent(ctx, "agent@unverified-send.example.com", "unverified-send.example.com", "", "https://example.com/webhook", "", user.ID)

	sendPayload := `{"to":["someone@other.com"],"subject":"Hi","body":"Hello"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/send", bytes.NewBufferString(sendPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp2, _ := http.DefaultClient.Do(req)
	defer resp2.Body.Close()

	if resp2.StatusCode != 403 {
		t.Errorf("status = %d, want 403 for unverified agent", resp2.StatusCode)
	}
}

func TestSendEmailMissingFields(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-send-missing@example.com", "Owner", "google-send-missing")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "send-missing-key", nil)
	store.ClaimOrCreateDomain(ctx, "send-missing.example.com", user.ID)
	store.VerifyDomain(ctx, "send-missing.example.com", user.ID)
	store.CreateAgent(ctx, "agent@send-missing.example.com", "send-missing.example.com", "", "https://example.com/webhook", "", user.ID)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/send", bytes.NewBufferString(`{"to":["someone@x.com"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
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
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, store, pool, smtpDone
}

func TestSendEmailViaSMTP(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	// Create user and API key
	user, _ := store.CreateOrGetUser(ctx, "owner-send@example.com", "Owner", "google-send")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "send-key", nil)

	// Agent (sender)
	store.ClaimOrCreateDomain(ctx, "sender.example.com", user.ID)
	store.VerifyDomain(ctx, "sender.example.com", user.ID)
	store.CreateAgent(ctx, "agent@sender.example.com", "sender.example.com", "", "https://example.com/webhook", "", user.ID)

	sendPayload := `{"to":["alice@example.com"],"subject":"Hello","body":"Hello from agent"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/send", bytes.NewBufferString(sendPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var sendResp map[string]string
	json.NewDecoder(resp.Body).Decode(&sendResp)
	if sendResp["method"] != "smtp" {
		t.Errorf("method = %q, want smtp", sendResp["method"])
	}
	if sendResp["message_id"] == "" {
		t.Error("expected non-empty message_id")
	}

	// Verify the fake SMTP server received the message
	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	if msgs[0].To != "alice@example.com" {
		t.Errorf("SMTP To = %q, want alice@example.com", msgs[0].To)
	}
}

func TestReplyToMessageUnauthorized(t *testing.T) {
	server, _, _ := setupAPI(t)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/any@example.com/messages/msg_123/reply", bytes.NewBufferString(`{"body":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestReplyToMessageUnverifiedDomain(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-unverified-reply@example.com", "Owner", "google-unverified-reply")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "unverified-reply-key", nil)
	store.ClaimOrCreateDomain(ctx, "unverified-reply.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "agent@unverified-reply.example.com", "unverified-reply.example.com", "", "https://example.com/webhook", "", user.ID)

	// Create an inbound message so the handler can find it and reach the domain verification check
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "sender@example.com", "agent@unverified-reply.example.com", "<test@example.com>", "Test", "", "", nil, nil, nil, nil, nil)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/agent@unverified-reply.example.com/messages/"+msg.ID+"/reply", bytes.NewBufferString(`{"body":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestReplyToMessageNotFound(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-reply-notfound@example.com", "Owner", "google-reply-notfound")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "reply-notfound-key", nil)
	store.ClaimOrCreateDomain(ctx, "reply-notfound.example.com", user.ID)
	store.VerifyDomain(ctx, "reply-notfound.example.com", user.ID)
	store.CreateAgent(ctx, "agent@reply-notfound.example.com", "reply-notfound.example.com", "", "https://example.com/webhook", "", user.ID)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/agent@reply-notfound.example.com/messages/msg_nonexistent/reply", bytes.NewBufferString(`{"body":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestReplyToMessageWrongAgent(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	userA, _ := store.CreateOrGetUser(ctx, "owner-reply-a@example.com", "OwnerA", "google-reply-a")
	store.ClaimOrCreateDomain(ctx, "reply-a.example.com", userA.ID)
	store.VerifyDomain(ctx, "reply-a.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "agent@reply-a.example.com", "reply-a.example.com", "", "https://example.com/webhook", "", userA.ID)

	userB, _ := store.CreateOrGetUser(ctx, "owner-reply-b@example.com", "OwnerB", "google-reply-b")
	apiKeyB, _ := store.CreateAPIKey(ctx, userB.ID, "reply-b-key", nil)
	store.ClaimOrCreateDomain(ctx, "reply-b.example.com", userB.ID)
	store.VerifyDomain(ctx, "reply-b.example.com", userB.ID)
	store.CreateAgent(ctx, "agent@reply-b.example.com", "reply-b.example.com", "", "https://example.com/webhook", "", userB.ID)

	// Create inbound message for agent A
	msg, _ := store.CreateInboundMessage(ctx, "", agentA.ID, "alice@gmail.com", "bot@reply-a.example.com", "<abc@gmail.com>", "Hello", "", "", nil, nil, nil, nil, nil)

	// Agent B tries to reply to agent A's message using B's agent email
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/agent@reply-b.example.com/messages/"+msg.ID+"/reply", bytes.NewBufferString(`{"body":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyB.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404 (wrong agent)", resp.StatusCode)
	}
}

func TestReplyToMessageMissingBody(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-reply-nobody@example.com", "Owner", "google-reply-nobody")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "reply-nobody-key", nil)
	store.ClaimOrCreateDomain(ctx, "reply-nobody.example.com", user.ID)
	store.VerifyDomain(ctx, "reply-nobody.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@reply-nobody.example.com", "reply-nobody.example.com", "", "https://example.com/webhook", "", user.ID)

	msg, _ := store.CreateInboundMessage(ctx, "", a.ID, "alice@gmail.com", "bot@reply-nobody.example.com", "", "", "", "", nil, nil, nil, nil, nil)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/agent@reply-nobody.example.com/messages/"+msg.ID+"/reply", bytes.NewBufferString(`{"body":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestReplyToMessageViaSMTP(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	// Create user and API key for the replying agent
	user, _ := store.CreateOrGetUser(ctx, "owner-replier@example.com", "Owner", "google-replier")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "replier-key", nil)

	// Sender agent (the one replying)
	store.ClaimOrCreateDomain(ctx, "replier.example.com", user.ID)
	store.VerifyDomain(ctx, "replier.example.com", user.ID)
	agentA, _ := store.CreateAgent(ctx, "agent@replier.example.com", "replier.example.com", "", "https://example.com/webhook", "", user.ID)

	// Create inbound message from alice@gmail.com to bot@replier.example.com
	msg, _ := store.CreateInboundMessage(ctx, "", agentA.ID, "alice@gmail.com", "bot@replier.example.com", "<orig@gmail.com>", "Hello Bot", "", "", nil, nil, nil, nil, nil)

	// Reply
	body := `{"body":"Thanks for your email!","html_body":"<p>Thanks for your email!</p>"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/agent@replier.example.com/messages/"+msg.ID+"/reply", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "sent" {
		t.Errorf("status = %q, want sent", result["status"])
	}
	if result["method"] != "smtp" {
		t.Errorf("method = %q, want smtp", result["method"])
	}
	if result["message_id"] == "" {
		t.Error("expected non-empty message_id")
	}

	// Verify the fake SMTP server received the message
	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	if msgs[0].To != "alice@gmail.com" {
		t.Errorf("SMTP To = %q, want alice@gmail.com", msgs[0].To)
	}
}

func TestReplyToMessageSharedDomainDisplayName(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-shared-reply@example.com", "Owner", "google-shared-reply")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "shared-reply-key", nil)

	// Create a shared-domain agent directly (domain contains "@" → IsSharedDomain() = true)
	agentEmail := "reply-bot@agents.e2a.dev"
	store.ClaimOrCreateDomain(ctx, agentEmail, user.ID)
	store.VerifyDomain(ctx, agentEmail, user.ID)
	agent, _ := store.CreateAgent(ctx, agentEmail, agentEmail, "", "", "local", user.ID)

	// Create inbound message
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", agentEmail, "<orig@gmail.com>", "Hello Bot", "", "", nil, nil, nil, nil, nil)

	// Reply
	body := `{"body":"Hi from shared domain agent!"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/"+agentEmail+"/messages/"+msg.ID+"/reply", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Verify the From header contains the full agent email, not just the domain
	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	data := msgs[0].Data
	if !strings.Contains(data, "reply-bot@agents.e2a.dev via e2a") {
		t.Errorf("From header should contain full agent email address, got:\n%s", data)
	}
}

// -- Shared-domain / slug tests --

func TestRegisterAgentWithSlug(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "owner@example.com")

	payload := `{"slug":"my-cool-bot","name":"Cool Bot","webhook_url":"https://example.com/webhook"}`
	resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	if body["id"] == "" {
		t.Error("expected non-empty id")
	}

	// Shared-domain agents should be auto-verified
	ctx := context.Background()
	agent, err := store.GetAgentByEmail(ctx, "my-cool-bot@agents.e2a.dev")
	if err != nil {
		t.Fatalf("GetAgentByDomain: %v", err)
	}
	if !agent.DomainVerified {
		t.Error("expected shared-domain agent to be auto-verified")
	}
	if agent.Domain != "agents.e2a.dev" {
		t.Errorf("Domain = %q, want agents.e2a.dev", agent.Domain)
	}
}

func TestRegisterAgentSlugValidation(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "slugval@example.com")

	tests := []struct {
		name string
		slug string
	}{
		{"too short", "a"},
		{"too long", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"leading hyphen", "-bot"},
		{"trailing hyphen", "bot-"},
		{"uppercase", "MyBot"},
		{"special chars", "bot@123"},
		{"spaces", "my bot"},
		{"reserved: admin", "admin"},
		{"reserved: postmaster", "postmaster"},
		{"reserved: abuse", "abuse"},
		{"reserved: noreply", "noreply"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"slug":%q,"name":"Bot","webhook_url":"https://example.com/webhook"}`, tt.slug)
			resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
			defer resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Errorf("slug %q: status = %d, want 400", tt.slug, resp.StatusCode)
			}
		})
	}
}

func TestRegisterAgentSlugValidAccepted(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "slugok@example.com")

	slugs := []string{"ab", "my-bot", "support-agent-1", "a1"}
	for _, slug := range slugs {
		t.Run(slug, func(t *testing.T) {
			payload := fmt.Sprintf(`{"slug":%q,"name":"Bot","webhook_url":"https://example.com/webhook"}`, slug)
			resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
			defer resp.Body.Close()
			if resp.StatusCode != 201 {
				t.Errorf("slug %q: status = %d, want 201", slug, resp.StatusCode)
			}
		})
	}
}

func TestRegisterAgentDuplicateSlug(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "dupslug@example.com")

	payload := `{"slug":"dup-slug","name":"Bot","webhook_url":"https://example.com/webhook"}`
	resp1 := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	resp1.Body.Close()

	resp2 := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	defer resp2.Body.Close()
	if resp2.StatusCode != 409 {
		t.Errorf("status = %d, want 409", resp2.StatusCode)
	}
}

func TestVerifyDomainSharedDomainSkipped(t *testing.T) {
	// Shared domains are pre-verified (seeded with verified=true, user_id=NULL).
	// Registering an agent on a shared domain via slug should succeed without
	// any explicit domain verification step.
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "owner-skip-verify@example.com")

	payload := `{"slug":"skip-verify","agent_mode":"local"}`
	resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Fatalf("status = %d, want 201 for shared-domain agent via slug", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["email"] != "skip-verify@agents.e2a.dev" {
		t.Errorf("email = %q, want skip-verify@agents.e2a.dev", body["email"])
	}
}

// -- Test email tests --

func setupAPIWithAuth(t *testing.T) (*httptest.Server, *identity.Store, *pgxpool.Pool, *auth.UserAuth) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{Host: "localhost", Port: 2525})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	oauthCfg := &config.OAuthConfig{
		GoogleClientID: "test", GoogleClientSecret: "test", RedirectURL: "http://localhost/cb",
	}
	noopUsage := usage.NewNoopUsageTracker()
	userAuth := auth.NewUserAuth(oauthCfg, store, false)
	api := agent.NewAPI(store, sender, smtpRelay, userAuth, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, store, pool, userAuth
}


// -- Polling endpoint tests --

func TestRegisterAgentLocalMode(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "localreg@example.com")

	ctx := context.Background()
	user, _ := store.GetUserByAPIKey(ctx, apiKey)
	store.ClaimOrCreateDomain(ctx, "local-reg.example.com", user.ID)
	store.VerifyDomain(ctx, "local-reg.example.com", user.ID)

	payload := `{"email":"agent@local-reg.example.com","agent_mode":"local"}`
	resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	a, err := store.GetAgentByEmail(ctx, "agent@local-reg.example.com")
	if err != nil {
		t.Fatalf("GetAgentByDomain: %v", err)
	}
	if a.AgentMode != "local" {
		t.Errorf("agent_mode = %q, want local", a.AgentMode)
	}
}

func TestRegisterAgentInvalidAgentMode(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "badmode@example.com")

	payload := `{"email":"agent@bad-mode.example.com","agent_mode":"invalid"}`
	resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRegisterAgentCloudModeNoWebhook(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "nowh@example.com")

	payload := `{"email":"agent@no-wh.example.com","agent_mode":"cloud"}`
	resp := authedPost(t, server.URL+"/api/v1/agents", payload, apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for cloud without webhook", resp.StatusCode)
	}
}

func TestListAgents_Success(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-list@example.com", "Owner", "google-list")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "list-key", nil)
	store.ClaimOrCreateDomain(ctx, "list.example.com", user.ID)
	store.CreateAgent(ctx, "agent1@list.example.com", "list.example.com", "", "https://example.com/webhook", "", user.ID)
	store.ClaimOrCreateDomain(ctx, "list2.example.com", user.ID)
	store.CreateAgent(ctx, "agent2@list2.example.com", "list2.example.com", "", "", "local", user.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Agents []map[string]interface{} `json:"agents"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(body.Agents))
	}
	// Verify sensitive fields are not exposed
	for _, ag := range body.Agents {
		if _, ok := ag["verification_token"]; ok {
			t.Error("verification_token should not be in response")
		}
		if _, ok := ag["owner_email"]; ok {
			t.Error("owner_email should not be in response")
		}
	}
}

func TestListAgents_Unauthorized(t *testing.T) {
	server, _, _ := setupAPI(t)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestListAgents_Empty(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-empty-list@example.com", "Owner", "google-empty-list")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "empty-list-key", nil)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Agents []json.RawMessage `json:"agents"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Agents) != 0 {
		t.Errorf("got %d agents, want 0", len(body.Agents))
	}
}

func TestGetMessages_Unauthorized(t *testing.T) {
	server, _, _ := setupAPI(t)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/any@example.com/messages", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGetMessages_CloudModeAllowed(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-cloud-poll@example.com", "Owner", "google-cloud-poll")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "cloud-poll-key", nil)
	store.ClaimOrCreateDomain(ctx, "cloud-poll.example.com", user.ID)
	store.VerifyDomain(ctx, "cloud-poll.example.com", user.ID)
	store.CreateAgent(ctx, "agent@cloud-poll.example.com", "cloud-poll.example.com", "", "https://example.com/webhook", "", user.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@cloud-poll.example.com/messages?status=all", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 for cloud-mode agent listing messages", resp.StatusCode)
	}
}

func TestGetMessage_CloudModeAllowed(t *testing.T) {
	server, store, pool := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-cloud-msg@example.com", "Owner", "google-cloud-msg")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "cloud-msg-key", nil)
	store.ClaimOrCreateDomain(ctx, "cloud-msg.example.com", user.ID)
	store.VerifyDomain(ctx, "cloud-msg.example.com", user.ID)
	ag, _ := store.CreateAgent(ctx, "agent@cloud-msg.example.com", "cloud-msg.example.com", "", "https://example.com/webhook", "", user.ID)

	// Insert a message directly so we can fetch it.
	pool.Exec(ctx, `INSERT INTO messages (id, agent_id, direction, sender, recipient, subject, email_message_id, provider_message_id, conversation_id, inbox_status, created_at, expires_at)
		VALUES ('msg_cloud1', $1, 'inbound', 'alice@example.com', 'agent@cloud-msg.example.com', 'Hello', '<cloud1@example.com>', '', '', 'unread', NOW(), NOW() + INTERVAL '30 days')`, ag.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@cloud-msg.example.com/messages/msg_cloud1", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 for cloud-mode agent reading a message", resp.StatusCode)
	}
}

func TestGetMessages_InvalidStatus(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-inv-status@example.com", "Owner", "google-inv-status")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "inv-status-key", nil)
	store.ClaimOrCreateDomain(ctx, "inv-status.example.com", user.ID)
	store.CreateAgent(ctx, "agent@inv-status.example.com", "inv-status.example.com", "", "", "local", user.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@inv-status.example.com/messages?status=invalid", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for invalid status", resp.StatusCode)
	}
}

func TestGetMessages_EmptyList(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-empty-poll@example.com", "Owner", "google-empty-poll")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "empty-poll-key", nil)
	store.ClaimOrCreateDomain(ctx, "empty-poll.example.com", user.ID)
	store.CreateAgent(ctx, "agent@empty-poll.example.com", "empty-poll.example.com", "", "", "local", user.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@empty-poll.example.com/messages", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Messages  []json.RawMessage `json:"messages"`
		NextToken *string           `json:"next_token"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(body.Messages))
	}
	if body.NextToken != nil {
		t.Error("expected next_token to be absent")
	}
}

func TestGetMessage_Unauthorized(t *testing.T) {
	server, _, _ := setupAPI(t)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/any@example.com/messages/msg_123", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGetMessage_NotFound(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-msg-nf@example.com", "Owner", "google-msg-nf")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "msg-nf-key", nil)
	store.ClaimOrCreateDomain(ctx, "msg-nf.example.com", user.ID)
	store.CreateAgent(ctx, "agent@msg-nf.example.com", "msg-nf.example.com", "", "", "local", user.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@msg-nf.example.com/messages/msg_nonexistent", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetMessages_Pagination(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-pagination@example.com", "Owner", "google-pagination")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "pagination-key", nil)
	store.ClaimOrCreateDomain(ctx, "pagination.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@pagination.example.com", "pagination.example.com", "", "", "local", user.ID)

	// Create 5 messages
	for i := 0; i < 5; i++ {
		store.CreateInboundMessage(ctx, "", a.ID, fmt.Sprintf("sender%d@example.com", i), "agent@pagination.example.com", "", fmt.Sprintf("Subject %d", i), "", "unread", nil, nil, nil, nil, nil)
	}

	// Page 1: request 3 messages
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@pagination.example.com/messages?page_size=3&status=all", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("page 1: status = %d, want 200", resp.StatusCode)
	}

	var page1 struct {
		Messages  []json.RawMessage `json:"messages"`
		NextToken *string           `json:"next_token"`
	}
	json.NewDecoder(resp.Body).Decode(&page1)

	if len(page1.Messages) != 3 {
		t.Fatalf("page 1: got %d messages, want 3", len(page1.Messages))
	}
	if page1.NextToken == nil {
		t.Fatal("page 1: expected next_token to be present")
	}

	// Page 2: use next_token
	req2, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@pagination.example.com/messages?page_size=3&status=all&token="+*page1.NextToken, nil)
	req2.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("page 2: status = %d, want 200", resp2.StatusCode)
	}

	var page2 struct {
		Messages  []json.RawMessage `json:"messages"`
		NextToken *string           `json:"next_token"`
	}
	json.NewDecoder(resp2.Body).Decode(&page2)

	if len(page2.Messages) != 2 {
		t.Fatalf("page 2: got %d messages, want 2", len(page2.Messages))
	}
	if page2.NextToken != nil {
		t.Error("page 2: expected next_token to be absent on last page")
	}
}

func TestGetMessages_PaginationFilterMismatch(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-filtermm@example.com", "Owner", "google-filtermm")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "filtermm-key", nil)
	store.ClaimOrCreateDomain(ctx, "filtermm.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@filtermm.example.com", "filtermm.example.com", "", "", "local", user.ID)

	// Create enough messages to get a next_token
	for i := 0; i < 3; i++ {
		store.CreateInboundMessage(ctx, "", a.ID, fmt.Sprintf("sender%d@example.com", i), "agent@filtermm.example.com", "", fmt.Sprintf("Subject %d", i), "", "unread", nil, nil, nil, nil, nil)
	}

	// Get first page with status=all and page_size=2
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@filtermm.example.com/messages?page_size=2&status=all", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	var page1 struct {
		NextToken *string `json:"next_token"`
	}
	json.NewDecoder(resp.Body).Decode(&page1)
	if page1.NextToken == nil {
		t.Fatal("expected next_token")
	}

	// Use that token with a different status filter — should fail
	req2, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@filtermm.example.com/messages?status=unread&token="+*page1.NextToken, nil)
	req2.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()

	if resp2.StatusCode != 400 {
		t.Errorf("filter mismatch: status = %d, want 400", resp2.StatusCode)
	}
}

// Back-compat: a pagination token issued by an older server build that
// didn't encode the `direction` field must keep working against the
// upgraded handler. The default direction is "inbound", so an empty
// cursor.Direction is treated as inbound and the continuation page
// returns 200 — not the 400 "filter mismatch" the strict check would
// have raised before the back-compat shim was added.
func TestGetMessages_PaginationLegacyTokenNoDirection(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-legacytok@example.com", "Owner", "google-legacytok")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "legacytok-key", nil)
	store.ClaimOrCreateDomain(ctx, "legacytok.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@legacytok.example.com", "legacytok.example.com", "", "", "local", user.ID)

	// Create 3 inbound messages so a continuation page has something
	// to return. We capture the first message's CreatedAt+ID to build
	// the legacy token below.
	var first identity.Message
	for i := 0; i < 3; i++ {
		m, _ := store.CreateInboundMessage(ctx, "", a.ID, fmt.Sprintf("sender%d@example.com", i), "agent@legacytok.example.com", "", fmt.Sprintf("Subject %d", i), "", "unread", nil, nil, nil, nil, nil)
		if i == 0 {
			first = *m
		}
	}

	// Hand-craft a legacy token (no `d` field) pointing past the first
	// message — same shape the older server emitted before the
	// direction column was added to the cursor.
	legacyCursor, _ := json.Marshal(struct {
		CreatedAt time.Time `json:"c"`
		ID        string    `json:"i"`
		Status    string    `json:"s"`
		AgentID   string    `json:"a"`
	}{CreatedAt: first.CreatedAt, ID: first.ID, Status: "unread", AgentID: a.ID})
	tok := base64.RawURLEncoding.EncodeToString(legacyCursor)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@legacytok.example.com/messages?status=unread&token="+tok, nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("legacy token: status = %d, want 200 (body: %s)", resp.StatusCode, body)
	}
}

// --- direction= filter (mixed inbound+outbound for the dashboard inbox) ---

func TestGetMessages_DirectionAll_ReturnsMixed(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-dir-all@example.com", "Owner", "google-dir-all")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "dir-all-key", nil)
	store.ClaimOrCreateDomain(ctx, "dirall.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@dirall.example.com", "dirall.example.com", "", "", "local", user.ID)

	// 2 inbound + 2 outbound. Mixed direction.
	for i := 0; i < 2; i++ {
		store.CreateInboundMessage(ctx, "", a.ID, fmt.Sprintf("sender%d@example.com", i), "agent@dirall.example.com", "", fmt.Sprintf("Inbound %d", i), "", "unread", nil, nil, nil, nil, nil)
		store.CreateOutboundMessage(ctx, a.ID, []string{fmt.Sprintf("recv%d@example.com", i)}, nil, nil, fmt.Sprintf("Outbound %d", i), "send", "smtp", "", "")
	}

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@dirall.example.com/messages?direction=all&page_size=10", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	var body struct {
		Messages []struct {
			ID            string `json:"message_id"`
			Direction     string `json:"direction"`
			Status        string `json:"status"`         // inbox_status (back-compat) — populated for inbound
			HITLStatus    string `json:"hitl_status"`    // outbound HITL lifecycle
			WebhookStatus string `json:"webhook_status"` // outbound delivery
			SizeBytes     int    `json:"size_bytes"`
		} `json:"messages"`
	}
	json.NewDecoder(resp.Body).Decode(&body)

	if len(body.Messages) != 4 {
		t.Fatalf("got %d messages, want 4", len(body.Messages))
	}
	var inbound, outbound int
	for _, m := range body.Messages {
		switch m.Direction {
		case "inbound":
			inbound++
			if m.Status != "unread" {
				t.Errorf("inbound row status = %q, want 'unread' (back-compat inbox_status field)", m.Status)
			}
			if m.HITLStatus != "" {
				t.Errorf("inbound row hitl_status = %q, want empty", m.HITLStatus)
			}
		case "outbound":
			outbound++
			if m.HITLStatus == "" {
				t.Errorf("outbound row missing hitl_status (defaults to 'sent')")
			}
		default:
			t.Errorf("unexpected direction %q", m.Direction)
		}
	}
	if inbound != 2 || outbound != 2 {
		t.Errorf("direction split inbound=%d outbound=%d, want 2/2", inbound, outbound)
	}
}

func TestGetMessages_DirectionOutbound_RejectsStatusFilter(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-dir-out@example.com", "Owner", "google-dir-out")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "dir-out-key", nil)
	store.ClaimOrCreateDomain(ctx, "dirout.example.com", user.ID)
	store.CreateAgent(ctx, "agent@dirout.example.com", "dirout.example.com", "", "", "local", user.ID)

	// status=unread + direction=outbound is nonsensical — inbox_status is null
	// on outbound. The handler should reject the combination with 400 rather
	// than silently returning no rows.
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@dirout.example.com/messages?direction=outbound&status=unread", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGetMessages_DirectionTokenReplayRejected(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-dir-tok@example.com", "Owner", "google-dir-tok")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "dir-tok-key", nil)
	store.ClaimOrCreateDomain(ctx, "dirtok.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@dirtok.example.com", "dirtok.example.com", "", "", "local", user.ID)

	for i := 0; i < 3; i++ {
		store.CreateInboundMessage(ctx, "", a.ID, fmt.Sprintf("s%d@example.com", i), "agent@dirtok.example.com", "", fmt.Sprintf("S %d", i), "", "unread", nil, nil, nil, nil, nil)
		store.CreateOutboundMessage(ctx, a.ID, []string{fmt.Sprintf("r%d@example.com", i)}, nil, nil, fmt.Sprintf("O %d", i), "send", "smtp", "", "")
	}

	// Get a next_token for direction=all.
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@dirtok.example.com/messages?direction=all&page_size=2", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	var page1 struct {
		NextToken *string `json:"next_token"`
	}
	json.NewDecoder(resp.Body).Decode(&page1)
	if page1.NextToken == nil {
		t.Fatal("expected next_token for direction=all paginated query")
	}

	// Replay the token with direction=outbound — handler must reject.
	req2, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@dirtok.example.com/messages?direction=outbound&status=all&token="+*page1.NextToken, nil)
	req2.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Errorf("token replay across direction: status = %d, want 400", resp2.StatusCode)
	}
}

func TestGetMessages_InvalidToken(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-badtoken@example.com", "Owner", "google-badtoken")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "badtoken-key", nil)
	store.ClaimOrCreateDomain(ctx, "badtoken.example.com", user.ID)
	store.CreateAgent(ctx, "agent@badtoken.example.com", "badtoken.example.com", "", "", "local", user.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@badtoken.example.com/messages?token=not-valid-base64!!!", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("invalid token: status = %d, want 400", resp.StatusCode)
	}
}

func TestGetMessages_PageSizeDefault(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-pagedefault@example.com", "Owner", "google-pagedefault")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "pagedefault-key", nil)
	store.ClaimOrCreateDomain(ctx, "pagedefault.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@pagedefault.example.com", "pagedefault.example.com", "", "", "local", user.ID)

	// Create 2 messages — with default page_size=50, should return all without next_token
	for i := 0; i < 2; i++ {
		store.CreateInboundMessage(ctx, "", a.ID, fmt.Sprintf("sender%d@example.com", i), "agent@pagedefault.example.com", "", fmt.Sprintf("Subject %d", i), "", "unread", nil, nil, nil, nil, nil)
	}

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/agent@pagedefault.example.com/messages?status=all", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Messages  []json.RawMessage `json:"messages"`
		NextToken *string           `json:"next_token"`
	}
	json.NewDecoder(resp.Body).Decode(&body)

	if len(body.Messages) != 2 {
		t.Errorf("got %d messages, want 2", len(body.Messages))
	}
	if body.NextToken != nil {
		t.Error("expected no next_token when all messages fit in one page")
	}
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

func TestVerifyDomainDevModeSkipsDNS(t *testing.T) {
	// setupAPI passes production=false, so DNS check should be skipped
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-devdns@example.com", "Owner", "google-devdns")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "devdns-key", nil)
	// Create agent with a domain that has no real DNS TXT records
	store.ClaimOrCreateDomain(ctx, "fake.norealdns.local", user.ID)
	store.CreateAgent(ctx, "agent@fake.norealdns.local", "fake.norealdns.local", "", "https://example.com/webhook", "", user.ID)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/domains/fake.norealdns.local/verify", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (dev mode should skip DNS)", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["verified"] != true {
		t.Errorf("expected verified=true in dev mode without DNS")
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

// captureLogs redirects log output to a buffer for the duration of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(original) })
	return &buf
}

func TestMailLog_SendEmail(t *testing.T) {
	logBuf := captureLogs(t)
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-log-send@example.com", "Owner", "google-log-send")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "log-send-key", nil)
	store.ClaimOrCreateDomain(ctx, "log-send.example.com", user.ID)
	store.VerifyDomain(ctx, "log-send.example.com", user.ID)
	store.CreateAgent(ctx, "bot@log-send.example.com", "log-send.example.com", "", "https://example.com/webhook", "", user.ID)

	payload := `{"to":["alice@example.com"],"subject":"Log Test","body":"hello"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/send", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	smtpDone()

	logOutput := logBuf.String()
	for _, want := range []string{"[mail:", "dir=outbound", "type=send", "from=bot@log-send.example.com", "to=[alice@example.com]", "slug=bot", "subject=\"Log Test\""} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("log missing %q, got:\n%s", want, logOutput)
		}
	}
}

func TestMailLog_ReplyEmail(t *testing.T) {
	logBuf := captureLogs(t)
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-log-reply@example.com", "Owner", "google-log-reply")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "log-reply-key", nil)
	store.ClaimOrCreateDomain(ctx, "log-reply.example.com", user.ID)
	store.VerifyDomain(ctx, "log-reply.example.com", user.ID)
	ag, _ := store.CreateAgent(ctx, "bot@log-reply.example.com", "log-reply.example.com", "", "https://example.com/webhook", "", user.ID)

	inbound, _ := store.CreateInboundMessage(ctx, "", ag.ID, "alice@example.com", "bot@log-reply.example.com", "<orig@example.com>", "Thread Start", "conv_test123", "", nil, nil, nil, nil, nil)

	payload := `{"body":"got it","conversation_id":"conv_test123"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/bot@log-reply.example.com/messages/"+inbound.ID+"/reply", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	smtpDone()

	logOutput := logBuf.String()
	for _, want := range []string{"[mail:", "dir=outbound", "type=reply", "from=bot@log-reply.example.com", "to=[alice@example.com]", "slug=bot", "conv_id=conv_test123", "in_reply_to=" + inbound.ID} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("log missing %q, got:\n%s", want, logOutput)
		}
	}
}

// ── Test email endpoint ─────────────────────────────────────────

func TestSendTestEmailSuccess(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-test@example.com", "Owner", "google-test-email")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "test-email-key", nil)
	store.ClaimOrCreateDomain(ctx, "test-email.example.com", user.ID)
	store.VerifyDomain(ctx, "test-email.example.com", user.ID)
	store.CreateAgent(ctx, "bot@test-email.example.com", "test-email.example.com", "", "https://example.com/webhook", "", user.ID)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/bot@test-email.example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "sent" {
		t.Errorf("status = %q, want sent", body["status"])
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	if msgs[0].To != "bot@test-email.example.com" {
		t.Errorf("to = %q, want bot@test-email.example.com", msgs[0].To)
	}
	if !strings.HasPrefix(msgs[0].From, "noreply@") {
		t.Errorf("from = %q, want noreply@...", msgs[0].From)
	}
}

func TestSendTestEmailUnauthenticated(t *testing.T) {
	server, _, _ := setupAPI(t)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/bot@example.com/test", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestSendTestEmailUnverifiedDomain(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-unverified-test@example.com", "Owner", "google-unverified-test")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "unverified-test-key", nil)
	store.ClaimOrCreateDomain(ctx, "unverified-test.example.com", user.ID)
	store.CreateAgent(ctx, "bot@unverified-test.example.com", "unverified-test.example.com", "", "https://example.com/webhook", "", user.ID)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/bot@unverified-test.example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestSendTestEmailWrongUser(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	// Owner creates the agent
	owner, _ := store.CreateOrGetUser(ctx, "real-owner@example.com", "Owner", "google-real-owner")
	store.ClaimOrCreateDomain(ctx, "wrong-user-test.example.com", owner.ID)
	store.VerifyDomain(ctx, "wrong-user-test.example.com", owner.ID)
	store.CreateAgent(ctx, "bot@wrong-user-test.example.com", "wrong-user-test.example.com", "", "https://example.com/webhook", "", owner.ID)

	// Different user tries to send test email
	otherKey := createTestUser(t, store, "other@example.com")

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/bot@wrong-user-test.example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+otherKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404 for agent owned by different user", resp.StatusCode)
	}
}


// --- Per-record DNS verification ---

// TestVerifyDomain_PerRecordDiagnostic: the response now includes
// per-record probe results (mx/spf/dkim) so the redesigned Domains
// page can render found/missing chips per record. In dev mode the
// probes short-circuit to "found" so this test asserts the contract
// shape only — the actual DNS probe paths are exercised by manual
// testing against real domains.
func TestVerifyDomain_PerRecordDiagnostic(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "verify-diag@example.com")
	ctx := context.Background()
	user, _ := store.GetUserByAPIKey(ctx, apiKey)
	store.ClaimOrCreateDomain(ctx, "diag.example.com", user.ID)

	resp := authedPost(t, server.URL+"/api/v1/domains/diag.example.com/verify", "", apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var body struct {
		Domain   string `json:"domain"`
		Verified bool   `json:"verified"`
		MX       string `json:"mx"`
		SPF      string `json:"spf"`
		DKIM     string `json:"dkim"`
	}
	json.NewDecoder(resp.Body).Decode(&body)

	if !body.Verified {
		t.Errorf("verified = false, want true (dev-mode probe accepts TXT)")
	}
	if body.MX != "found" {
		t.Errorf("mx = %q, want found (dev mode short-circuits)", body.MX)
	}
	if body.SPF != "found" {
		t.Errorf("spf = %q, want found", body.SPF)
	}
	// Per-domain DKIM now ships: ClaimOrCreateDomain
	// generates a keypair on insert, so the dev-mode probe short-circuit
	// reports "found". Pre-migration rows without a stored keypair are
	// the only path that still returns "deferred".
	if body.DKIM != "found" {
		t.Errorf("dkim = %q, want found (per-domain DKIM provisions a keypair at register time)", body.DKIM)
	}
}

// TestVerifyDomain_AlreadyVerified_StillReturnsDiagnostic: re-probing
// an already-verified domain returns the same per-record shape so the
// dashboard can refresh chips without losing the verified state.
func TestVerifyDomain_AlreadyVerified_StillReturnsDiagnostic(t *testing.T) {
	server, store, _ := setupAPI(t)
	apiKey := createTestUser(t, store, "verify-reprobe@example.com")
	ctx := context.Background()
	user, _ := store.GetUserByAPIKey(ctx, apiKey)
	store.ClaimOrCreateDomain(ctx, "reprobe.example.com", user.ID)
	store.VerifyDomain(ctx, "reprobe.example.com", user.ID)

	resp := authedPost(t, server.URL+"/api/v1/domains/reprobe.example.com/verify", "", apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Verified bool   `json:"verified"`
		MX       string `json:"mx"`
		DKIM     string `json:"dkim"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if !body.Verified || body.MX != "found" || body.DKIM != "found" {
		t.Errorf("already-verified response missing diagnostic: %+v", body)
	}
}

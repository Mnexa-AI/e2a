//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// setupDomainAndAgent is a helper that creates a user, claims a domain, verifies it,
// and creates an agent on that domain. Returns the user, API key, and agent.
func setupDomainAndAgent(t *testing.T, ts *testutil.E2ATestServer, email, domain, webhookURL, agentMode string) (*identity.User, *identity.APIKey, *identity.AgentIdentity) {
	t.Helper()
	ctx := context.Background()

	userEmail := "owner-" + domain + "@example.com"
	googleSub := "google-" + domain
	user, err := ts.Store.CreateOrGetUser(ctx, userEmail, "Owner", googleSub)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	apiKey, err := ts.Store.CreateAPIKey(ctx, user.ID, domain+"-key", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	if _, err := ts.Store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := ts.Store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}

	agent, err := ts.Store.CreateAgent(ctx, email, domain, "", webhookURL, agentMode, user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return user, apiKey, agent
}

func TestInboundDelivered(t *testing.T) {
	pool := testutil.TestDB(t)
	receiver := testutil.WebhookReceiver(t)
	ts := testutil.TestServer(t, pool)

	_, _, _ = setupDomainAndAgent(t, ts, "agent@inbound.example.com", "inbound.example.com", receiver.Server.URL, "cloud")

	// Send email via SMTP
	msg := "From: alice@gmail.com\r\nTo: agent@inbound.example.com\r\nSubject: Test\r\n\r\nHello from SMTP!"
	err := smtp.SendMail(ts.SMTPAddr, nil, "alice@gmail.com", []string{"agent@inbound.example.com"}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	payloads := receiver.WaitForPayloads(t, 1, 5*time.Second)
	p := payloads[0]

	if p.Body.From != "alice@gmail.com" {
		t.Errorf("From = %q", p.Body.From)
	}
	if p.Body.To != "agent@inbound.example.com" {
		t.Errorf("To = %q", p.Body.To)
	}

	// Domain-Check header should be present
	domainCheck := p.Body.AuthHeaders["X-E2A-Auth-Domain-Check"]
	if domainCheck == "" {
		t.Error("expected non-empty X-E2A-Auth-Domain-Check header")
	}
	if !strings.Contains(domainCheck, "spf=") || !strings.Contains(domainCheck, "dkim=") {
		t.Errorf("Domain-Check = %q, expected spf= and dkim= components", domainCheck)
	}

	// Verify signature
	if !ts.Signer.Verify(headers.AuthHeaders(p.Body.AuthHeaders)) {
		t.Error("auth header signature verification failed")
	}
}

func TestInboundDropsUnverifiedAgent(t *testing.T) {
	pool := testutil.TestDB(t)
	receiver := testutil.WebhookReceiver(t)
	ts := testutil.TestServer(t, pool)
	ctx := context.Background()

	// Register domain but do NOT verify it
	user, _ := ts.Store.CreateOrGetUser(ctx, "owner-unv@example.com", "Owner", "google-unv")
	ts.Store.ClaimOrCreateDomain(ctx, "unverified.example.com", user.ID)
	// Skip VerifyDomain — domain stays unverified, so CreateAgent on it would fail via API,
	// but we can test that the relay drops mail to an address with no matching agent.

	msg := "From: alice@test.com\r\nTo: bot@unverified.example.com\r\nSubject: Hi\r\n\r\nHello"
	smtp.SendMail(ts.SMTPAddr, nil, "alice@test.com", []string{"bot@unverified.example.com"}, []byte(msg))

	time.Sleep(500 * time.Millisecond)
	payloads := receiver.Payloads()
	if len(payloads) != 0 {
		t.Errorf("expected 0 deliveries for unverified agent, got %d", len(payloads))
	}
}

func TestOutboundAgentToAgent(t *testing.T) {
	t.Skip("requires an outbound SMTP relay configured in testutil.TestServer (and a loopback to inbound for the recipient webhook to fire) — tracked as test-infra work")
	pool := testutil.TestDB(t)
	receiver := testutil.WebhookReceiver(t)
	ts := testutil.TestServer(t, pool)

	_, apiKey, _ := setupDomainAndAgent(t, ts, "agent@out-a.example.com", "out-a.example.com", "https://example.com/webhook", "cloud")
	_, _, _ = setupDomainAndAgent(t, ts, "agent@out-b.example.com", "out-b.example.com", receiver.Server.URL, "cloud")

	sendPayload := `{"to":["agent@out-b.example.com"],"subject":"A2A","body":"Hello from A"}`
	req, _ := http.NewRequest("POST", ts.HTTPServer.URL+"/api/v1/send", bytes.NewBufferString(sendPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	payloads := receiver.WaitForPayloads(t, 1, 5*time.Second)
	p := payloads[0]
	if p.Body.AuthHeaders["X-E2A-Auth-Entity-Type"] != "agent" {
		t.Errorf("EntityType = %q, want agent", p.Body.AuthHeaders["X-E2A-Auth-Entity-Type"])
	}
	if p.Body.AuthHeaders["X-E2A-Auth-Verified"] != "true" {
		t.Errorf("Verified = %q, want true", p.Body.AuthHeaders["X-E2A-Auth-Verified"])
	}
}

func TestOutboundRequiresAuth(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool)

	sendPayload := `{"to":["someone@test.com"],"subject":"Hi","body":"Hello"}`
	req, _ := http.NewRequest("POST", ts.HTTPServer.URL+"/api/v1/send", bytes.NewBufferString(sendPayload))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestReplayProtection(t *testing.T) {
	pool := testutil.TestDB(t)
	receiver := testutil.WebhookReceiver(t)
	ts := testutil.TestServer(t, pool)

	_, _, _ = setupDomainAndAgent(t, ts, "agent@replay.example.com", "replay.example.com", receiver.Server.URL, "cloud")

	msg := "From: alice@test.com\r\nTo: agent@replay.example.com\r\nSubject: Replay\r\n\r\nTest"
	smtp.SendMail(ts.SMTPAddr, nil, "alice@test.com", []string{"agent@replay.example.com"}, []byte(msg))

	payloads := receiver.WaitForPayloads(t, 1, 5*time.Second)
	authHeaders := headers.AuthHeaders(payloads[0].Body.AuthHeaders)

	// Should verify with normal window
	if !ts.Signer.Verify(authHeaders) {
		t.Error("expected auth headers to verify within normal window")
	}

	// Should reject with very tight window
	time.Sleep(5 * time.Millisecond)
	if ts.Signer.VerifyWithMaxAge(authHeaders, 1*time.Millisecond) {
		t.Error("expected replay protection to reject stale headers")
	}
}

func TestOutboundResponseFormat(t *testing.T) {
	t.Skip("requires an outbound SMTP relay configured in testutil.TestServer — tracked as test-infra work")
	pool := testutil.TestDB(t)
	receiver := testutil.WebhookReceiver(t)
	ts := testutil.TestServer(t, pool)

	_, apiKey, _ := setupDomainAndAgent(t, ts, "agent@resp-a.example.com", "resp-a.example.com", "https://example.com/w", "cloud")
	_, _, _ = setupDomainAndAgent(t, ts, "agent@resp-b.example.com", "resp-b.example.com", receiver.Server.URL, "cloud")

	sendPayload := `{"to":["agent@resp-b.example.com"],"subject":"Test","body":"Hello"}`
	req, _ := http.NewRequest("POST", ts.HTTPServer.URL+"/api/v1/send", bytes.NewBufferString(sendPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)

	if body["status"] != "sent" {
		t.Errorf("status = %q, want sent", body["status"])
	}
	if body["message_id"] == "" {
		t.Error("expected non-empty message_id")
	}
	if body["method"] != "smtp" {
		t.Errorf("method = %q, want smtp", body["method"])
	}
}

func TestPollMode_E2E(t *testing.T) {
	// The reply portion of this test sends to a non-e2a recipient
	// (alice-poll@gmail.com), which requires an outbound SMTP relay
	// configured in testutil.TestServer — currently empty config.
	// Tracked as test-infra work.
	t.Skip("requires an outbound SMTP relay configured in testutil.TestServer")
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool)

	_, apiKey, _ := setupDomainAndAgent(t, ts, "agent@poll.example.com", "poll.example.com", "", "local")

	// Send email via SMTP to the local-mode agent
	msg := "From: alice-poll@gmail.com\r\nTo: agent@poll.example.com\r\nSubject: Poll Test\r\n\r\nHello via poll!"
	err := smtp.SendMail(ts.SMTPAddr, nil, "alice-poll@gmail.com", []string{"agent@poll.example.com"}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	// Wait a moment for processing
	time.Sleep(500 * time.Millisecond)

	// GET /api/v1/agents/{email}/messages should return the unread message
	req, _ := http.NewRequest("GET", ts.HTTPServer.URL+"/api/v1/agents/agent@poll.example.com/messages", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/v1/agents/{email}/messages status = %d, want 200", resp.StatusCode)
	}

	var listResp struct {
		Messages []struct {
			MessageID      string `json:"message_id"`
			From           string `json:"from"`
			To             string `json:"to"`
			Subject        string `json:"subject"`
			Status         string `json:"status"`
			ConversationID string `json:"conversation_id"`
		} `json:"messages"`
		HasMore bool `json:"has_more"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	if len(listResp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(listResp.Messages))
	}
	if listResp.Messages[0].From != "alice-poll@gmail.com" {
		t.Errorf("From = %q", listResp.Messages[0].From)
	}
	if listResp.Messages[0].Status != "unread" {
		t.Errorf("Status = %q, want unread", listResp.Messages[0].Status)
	}

	msgID := listResp.Messages[0].MessageID

	// GET /api/v1/agents/{email}/messages/{id} should return full content and mark as read
	req2, _ := http.NewRequest("GET", ts.HTTPServer.URL+"/api/v1/agents/agent@poll.example.com/messages/"+msgID, nil)
	req2.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("GET /api/v1/agents/{email}/messages/%s status = %d, want 200", msgID, resp2.StatusCode)
	}

	var msgResp struct {
		MessageID      string            `json:"message_id"`
		From           string            `json:"from"`
		To             string            `json:"to"`
		AuthHeaders    map[string]string `json:"auth_headers"`
		RawMessage     string            `json:"raw_message"`
		ConversationID string            `json:"conversation_id"`
		Status         string            `json:"status"`
	}
	json.NewDecoder(resp2.Body).Decode(&msgResp)

	if msgResp.MessageID != msgID {
		t.Errorf("MessageID = %q, want %q", msgResp.MessageID, msgID)
	}
	if msgResp.From != "alice-poll@gmail.com" {
		t.Errorf("From = %q", msgResp.From)
	}
	if msgResp.Status != "read" {
		t.Errorf("Status = %q, want read", msgResp.Status)
	}

	// Subsequent GET should show no unread messages
	req3, _ := http.NewRequest("GET", ts.HTTPServer.URL+"/api/v1/agents/agent@poll.example.com/messages?status=unread", nil)
	req3.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp3, _ := http.DefaultClient.Do(req3)
	defer resp3.Body.Close()

	var emptyResp struct {
		Messages []interface{} `json:"messages"`
	}
	json.NewDecoder(resp3.Body).Decode(&emptyResp)
	if len(emptyResp.Messages) != 0 {
		t.Errorf("expected 0 unread messages after read, got %d", len(emptyResp.Messages))
	}

	// Reply via API should work
	replyPayload := `{"body":"Got your message!"}`
	req4, _ := http.NewRequest("POST", ts.HTTPServer.URL+"/api/v1/agents/agent@poll.example.com/messages/"+msgID+"/reply", bytes.NewBufferString(replyPayload))
	req4.Header.Set("Content-Type", "application/json")
	req4.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp4, _ := http.DefaultClient.Do(req4)
	defer resp4.Body.Close()

	if resp4.StatusCode != 200 {
		t.Errorf("reply status = %d, want 200", resp4.StatusCode)
	}
}

func TestPushToLocalSwitch_E2E(t *testing.T) {
	pool := testutil.TestDB(t)
	receiver := testutil.WebhookReceiver(t)
	ts := testutil.TestServer(t, pool)
	ctx := context.Background()

	user, apiKey, agent := setupDomainAndAgent(t, ts, "agent@switch.example.com", "switch.example.com", receiver.Server.URL, "cloud")

	// Switch to local mode
	err := ts.Store.UpdateAgentMode(ctx, agent.ID, user.ID, "local", "")
	if err != nil {
		t.Fatalf("UpdateAgentMode: %v", err)
	}

	// Send email — should be stored, not pushed
	msg := "From: alice-switch@gmail.com\r\nTo: agent@switch.example.com\r\nSubject: Switch Test\r\n\r\nAfter switch"
	smtp.SendMail(ts.SMTPAddr, nil, "alice-switch@gmail.com", []string{"agent@switch.example.com"}, []byte(msg))

	time.Sleep(500 * time.Millisecond)

	// Webhook should NOT have received anything
	payloads := receiver.Payloads()
	if len(payloads) != 0 {
		t.Errorf("expected 0 webhook deliveries after switch to local, got %d", len(payloads))
	}

	// API should show the message
	req, _ := http.NewRequest("GET", ts.HTTPServer.URL+"/api/v1/agents/agent@switch.example.com/messages", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	var listResp struct {
		Messages []struct {
			MessageID string `json:"message_id"`
			Status    string `json:"status"`
		} `json:"messages"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	if len(listResp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(listResp.Messages))
	}
	if listResp.Messages[0].Status != "unread" {
		t.Errorf("Status = %q, want unread", listResp.Messages[0].Status)
	}
}

func TestRcptRejectsUnknownRecipient(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool)

	// Do NOT register any agent — send to a completely unknown address
	msg := "From: alice@gmail.com\r\nTo: nobody@unknown.example.com\r\nSubject: Test\r\n\r\nHello!"
	err := smtp.SendMail(ts.SMTPAddr, nil, "alice@gmail.com", []string{"nobody@unknown.example.com"}, []byte(msg))
	if err == nil {
		t.Fatal("expected SendMail to fail for unknown recipient, got nil")
	}
	if !strings.Contains(err.Error(), "550") {
		t.Errorf("expected 550 error, got: %v", err)
	}
}

func TestRcptRejectsUnverifiedDomain(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool)
	ctx := context.Background()

	// Create agent on unverified domain
	user, _ := ts.Store.CreateOrGetUser(ctx, "owner-unverified@example.com", "Owner", "google-unverified")
	ts.Store.ClaimOrCreateDomain(ctx, "unverified.example.com", user.ID)
	// Intentionally do NOT verify the domain
	ts.Store.CreateAgent(ctx, "agent@unverified.example.com", "unverified.example.com", "", "", "local", user.ID)

	msg := "From: alice@gmail.com\r\nTo: agent@unverified.example.com\r\nSubject: Test\r\n\r\nHello!"
	err := smtp.SendMail(ts.SMTPAddr, nil, "alice@gmail.com", []string{"agent@unverified.example.com"}, []byte(msg))
	if err == nil {
		t.Fatal("expected SendMail to fail for unverified domain, got nil")
	}
	if !strings.Contains(err.Error(), "550") {
		t.Errorf("expected 550 error, got: %v", err)
	}
}

func TestRcptAcceptsValidAgent(t *testing.T) {
	pool := testutil.TestDB(t)
	receiver := testutil.WebhookReceiver(t)
	ts := testutil.TestServer(t, pool)

	_, _, _ = setupDomainAndAgent(t, ts, "agent@valid.example.com", "valid.example.com", receiver.Server.URL, "cloud")

	msg := "From: alice@gmail.com\r\nTo: agent@valid.example.com\r\nSubject: Test\r\n\r\nHello!"
	err := smtp.SendMail(ts.SMTPAddr, nil, "alice@gmail.com", []string{"agent@valid.example.com"}, []byte(msg))
	if err != nil {
		t.Fatalf("expected SendMail to succeed for valid agent, got: %v", err)
	}

	// Verify delivery happened
	payloads := receiver.WaitForPayloads(t, 1, 5*time.Second)
	if payloads[0].Body.From != "alice@gmail.com" {
		t.Errorf("From = %q, want alice@gmail.com", payloads[0].Body.From)
	}
}

// Unused import guard
var _ = (*identity.Store)(nil)

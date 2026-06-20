//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/smtp"
	"net/url"
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

// authHeaderSecrets returns the secrets the relay would use to sign a
// user's inbound auth headers. The relay signs with the owner's
// per-user webhook signing secret whenever one exists — and one always
// does, since CreateOrGetUser auto-provisions a "default" secret. So
// tests must verify auth-header signatures against these, not the
// deployment-wide ts.Signer (that signer is only a fallback for
// secret-less/legacy agents, which owned test agents never are).
func authHeaderSecrets(t *testing.T, ts *testutil.E2ATestServer, userID string) []string {
	t.Helper()
	secs, err := ts.Store.GetUserSigningSecrets(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUserSigningSecrets: %v", err)
	}
	out := make([]string, len(secs))
	for i, s := range secs {
		out[i] = s.Secret
	}
	return out
}

// receivedAuthHeaders pulls the auth_headers map out of an email.received
// subscriber envelope's data block (the relay carries the signed
// X-E2A-Auth-* headers there).
func receivedAuthHeaders(data map[string]any) map[string]string {
	raw, _ := data["auth_headers"].(map[string]any)
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func TestInboundDelivered(t *testing.T) {
	pool := testutil.TestDB(t)
	receiver := testutil.SubscriberReceiver(t)
	ts := testutil.TestServer(t, pool)

	// Inbound push now flows exclusively through the /v1/webhooks
	// subscriber resource (the legacy per-agent webhook_url is gone). Any
	// agent receives email.received as long as a subscription matches.
	user, _, agent := setupDomainAndAgent(t, ts, "agent@inbound.example.com", "inbound.example.com", "", "")
	registerWebhook(t, ts, user.ID, receiver.Server.URL+"/received",
		[]string{"email.received"}, identity.WebhookFilters{})
	_ = agent

	// Send email via SMTP
	msg := "From: alice@gmail.com\r\nTo: agent@inbound.example.com\r\nSubject: Test\r\n\r\nHello from SMTP!"
	err := smtp.SendMail(ts.SMTPAddr, nil, "alice@gmail.com", []string{"agent@inbound.example.com"}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 5*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 {
		t.Fatalf("got %d captures, want 1", len(got))
	}
	c := got[0]
	if c.Envelope["type"] != "email.received" {
		t.Errorf("event=%v want email.received", c.Envelope["type"])
	}
	data, _ := c.Envelope["data"].(map[string]any)

	if data["from"] != "alice@gmail.com" {
		t.Errorf("from = %v", data["from"])
	}
	to, _ := data["to"].([]any)
	if len(to) != 1 || to[0] != "agent@inbound.example.com" {
		t.Errorf("to = %v, want [agent@inbound.example.com]", data["to"])
	}

	// Domain-Check header should be present in the carried auth_headers.
	authHeaders := receivedAuthHeaders(data)
	domainCheck := authHeaders["X-E2A-Auth-Domain-Check"]
	if domainCheck == "" {
		t.Error("expected non-empty X-E2A-Auth-Domain-Check header")
	}
	if !strings.Contains(domainCheck, "spf=") || !strings.Contains(domainCheck, "dkim=") {
		t.Errorf("Domain-Check = %q, expected spf= and dkim= components", domainCheck)
	}

	// Verify signature against the owner's per-user signing secret (the
	// relay signs inbound auth headers with it, not the deployment signer).
	if !headers.Verify(authHeaderSecrets(t, ts, user.ID), headers.AuthHeaders(authHeaders)) {
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

	_, apiKey, senderAgent := setupDomainAndAgent(t, ts, "agent@out-a.example.com", "out-a.example.com", "https://example.com/webhook", "cloud")
	_, _, _ = setupDomainAndAgent(t, ts, "agent@out-b.example.com", "out-b.example.com", receiver.Server.URL, "cloud")

	sendPayload := `{"to":["agent@out-b.example.com"],"subject":"A2A","body":"Hello from A"}`
	req, _ := http.NewRequest("POST", ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape(senderAgent.EmailAddress())+"/messages", bytes.NewBufferString(sendPayload))
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

	// Send relocated (Slice 2): POST /v1/agents/{email}/messages. With no
	// bearer the typed handler authenticates first and 401s before it ever
	// resolves the path agent.
	sendPayload := `{"to":["someone@test.com"],"subject":"Hi","body":"Hello"}`
	req, _ := http.NewRequest("POST", ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape("agent@noauth.example.com")+"/messages", bytes.NewBufferString(sendPayload))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestReplayProtection(t *testing.T) {
	pool := testutil.TestDB(t)
	receiver := testutil.SubscriberReceiver(t)
	ts := testutil.TestServer(t, pool)

	user, _, _ := setupDomainAndAgent(t, ts, "agent@replay.example.com", "replay.example.com", "", "")
	registerWebhook(t, ts, user.ID, receiver.Server.URL+"/received",
		[]string{"email.received"}, identity.WebhookFilters{})

	msg := "From: alice@test.com\r\nTo: agent@replay.example.com\r\nSubject: Replay\r\n\r\nTest"
	smtp.SendMail(ts.SMTPAddr, nil, "alice@test.com", []string{"agent@replay.example.com"}, []byte(msg))

	tick(t, ts)
	got := receiver.WaitFor(t, 5*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 {
		t.Fatalf("got %d captures, want 1", len(got))
	}
	data, _ := got[0].Envelope["data"].(map[string]any)
	authHeaders := headers.AuthHeaders(receivedAuthHeaders(data))
	secrets := authHeaderSecrets(t, ts, user.ID)

	// Should verify with normal window
	if !headers.Verify(secrets, authHeaders) {
		t.Error("expected auth headers to verify within normal window")
	}

	// Should reject with very tight window
	time.Sleep(5 * time.Millisecond)
	if headers.VerifyWithMaxAge(secrets, authHeaders, 1*time.Millisecond) {
		t.Error("expected replay protection to reject stale headers")
	}
}

func TestOutboundResponseFormat(t *testing.T) {
	t.Skip("requires an outbound SMTP relay configured in testutil.TestServer — tracked as test-infra work")
	pool := testutil.TestDB(t)
	receiver := testutil.WebhookReceiver(t)
	ts := testutil.TestServer(t, pool)

	_, apiKey, senderAgent := setupDomainAndAgent(t, ts, "agent@resp-a.example.com", "resp-a.example.com", "https://example.com/w", "cloud")
	_, _, _ = setupDomainAndAgent(t, ts, "agent@resp-b.example.com", "resp-b.example.com", receiver.Server.URL, "cloud")

	sendPayload := `{"to":["agent@resp-b.example.com"],"subject":"Test","body":"Hello"}`
	req, _ := http.NewRequest("POST", ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape(senderAgent.EmailAddress())+"/messages", bytes.NewBufferString(sendPayload))
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

	// GET /v1/agents/{email}/messages should return the unread message.
	// /v1 cursor page: {items:[...], next_cursor:...}.
	req, _ := http.NewRequest("GET", ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape("agent@poll.example.com")+"/messages", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("GET /v1/agents/{email}/messages status = %d, want 200", resp.StatusCode)
	}

	var listResp struct {
		Items []struct {
			MessageID      string   `json:"message_id"`
			From           string   `json:"from"`
			To             []string `json:"to"`
			Subject        string   `json:"subject"`
			Status         string   `json:"read_status"`
			ConversationID string   `json:"conversation_id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 message, got %d", len(listResp.Items))
	}
	if listResp.Items[0].From != "alice-poll@gmail.com" {
		t.Errorf("From = %q", listResp.Items[0].From)
	}
	if listResp.Items[0].Status != "unread" {
		t.Errorf("Status = %q, want unread", listResp.Items[0].Status)
	}

	msgID := listResp.Items[0].MessageID

	// GET /v1/agents/{email}/messages/{id} should return full content and mark as read.
	req2, _ := http.NewRequest("GET", ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape("agent@poll.example.com")+"/messages/"+msgID, nil)
	req2.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("GET /v1/agents/{email}/messages/%s status = %d, want 200", msgID, resp2.StatusCode)
	}

	var msgResp struct {
		MessageID      string            `json:"message_id"`
		From           string            `json:"from"`
		To             []string          `json:"to"`
		AuthHeaders    map[string]string `json:"auth_headers"`
		RawMessage     string            `json:"raw_message"`
		ConversationID string            `json:"conversation_id"`
		Status         string            `json:"read_status"`
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
	req3, _ := http.NewRequest("GET", ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape("agent@poll.example.com")+"/messages?read_status=unread", nil)
	req3.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp3, _ := http.DefaultClient.Do(req3)
	defer resp3.Body.Close()

	var emptyResp struct {
		Items []interface{} `json:"items"`
	}
	json.NewDecoder(resp3.Body).Decode(&emptyResp)
	if len(emptyResp.Items) != 0 {
		t.Errorf("expected 0 unread messages after read, got %d", len(emptyResp.Items))
	}

	// Reply via API should work (reply path is unchanged, only de-prefixed).
	replyPayload := `{"body":"Got your message!"}`
	req4, _ := http.NewRequest("POST", ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape("agent@poll.example.com")+"/messages/"+msgID+"/reply", bytes.NewBufferString(replyPayload))
	req4.Header.Set("Content-Type", "application/json")
	req4.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp4, _ := http.DefaultClient.Do(req4)
	defer resp4.Body.Close()

	if resp4.StatusCode != 200 {
		t.Errorf("reply status = %d, want 200", resp4.StatusCode)
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
	receiver := testutil.SubscriberReceiver(t)
	ts := testutil.TestServer(t, pool)

	user, _, _ := setupDomainAndAgent(t, ts, "agent@valid.example.com", "valid.example.com", "", "")
	registerWebhook(t, ts, user.ID, receiver.Server.URL+"/received",
		[]string{"email.received"}, identity.WebhookFilters{})

	msg := "From: alice@gmail.com\r\nTo: agent@valid.example.com\r\nSubject: Test\r\n\r\nHello!"
	err := smtp.SendMail(ts.SMTPAddr, nil, "alice@gmail.com", []string{"agent@valid.example.com"}, []byte(msg))
	if err != nil {
		t.Fatalf("expected SendMail to succeed for valid agent, got: %v", err)
	}

	// Verify delivery happened via the subscriber path.
	tick(t, ts)
	got := receiver.WaitFor(t, 5*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 {
		t.Fatalf("got %d captures, want 1", len(got))
	}
	data, _ := got[0].Envelope["data"].(map[string]any)
	if data["from"] != "alice@gmail.com" {
		t.Errorf("from = %v, want alice@gmail.com", data["from"])
	}
}

// Unused import guard
var _ = (*identity.Store)(nil)

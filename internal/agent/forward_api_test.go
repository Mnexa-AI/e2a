package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// rawInboundForForwardTest builds a minimal RFC 5322 message that the
// forward handler's MIME extractor can parse. Includes a Subject so the
// composed forward subject can be asserted.
func rawInboundForForwardTest(from, to, subject, body string) []byte {
	return []byte("From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		body)
}

func TestForwardMessageUnauthorized(t *testing.T) {
	server, _, _ := setupAPI(t)

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/any@example.com/messages/msg_123/forward",
		bytes.NewBufferString(`{"to":["alice@example.com"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestForwardMessageNotFound(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-notfound@example.com", "Owner", "google-fwd-notfound")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-notfound-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-notfound.example.com", user.ID)
	store.VerifyDomain(ctx, "fwd-notfound.example.com", user.ID)
	store.CreateAgent(ctx, "agent@fwd-notfound.example.com", "fwd-notfound.example.com", "", "https://example.com/webhook", "", user.ID)

	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/agent@fwd-notfound.example.com/messages/msg_nonexistent/forward",
		bytes.NewBufferString(`{"to":["alice@example.com"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestForwardMessageWrongAgent(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	userA, _ := store.CreateOrGetUser(ctx, "owner-fwd-a@example.com", "OwnerA", "google-fwd-a")
	store.ClaimOrCreateDomain(ctx, "fwd-a.example.com", userA.ID)
	store.VerifyDomain(ctx, "fwd-a.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "agent@fwd-a.example.com", "fwd-a.example.com", "", "https://example.com/webhook", "", userA.ID)

	userB, _ := store.CreateOrGetUser(ctx, "owner-fwd-b@example.com", "OwnerB", "google-fwd-b")
	apiKeyB, _ := store.CreateAPIKey(ctx, userB.ID, "fwd-b-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-b.example.com", userB.ID)
	store.VerifyDomain(ctx, "fwd-b.example.com", userB.ID)
	store.CreateAgent(ctx, "agent@fwd-b.example.com", "fwd-b.example.com", "", "https://example.com/webhook", "", userB.ID)

	msg, _ := store.CreateInboundMessage(ctx, "", agentA.ID, "alice@gmail.com", "bot@fwd-a.example.com", "<orig@gmail.com>", "Hello", "", "", nil, nil, nil, nil, nil)

	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/agent@fwd-b.example.com/messages/"+msg.ID+"/forward",
		bytes.NewBufferString(`{"to":["alice@example.com"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyB.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404 (wrong agent)", resp.StatusCode)
	}
}

func TestForwardMessageUnverifiedDomain(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-unverified@example.com", "Owner", "google-fwd-unverified")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-unverified-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-unverified.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "agent@fwd-unverified.example.com", "fwd-unverified.example.com", "", "https://example.com/webhook", "", user.ID)

	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "agent@fwd-unverified.example.com", "<orig@gmail.com>", "Hello", "", "", nil, nil, nil, nil, nil)

	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/agent@fwd-unverified.example.com/messages/"+msg.ID+"/forward",
		bytes.NewBufferString(`{"to":["alice@example.com"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestForwardMessageMissingRecipients(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-norecip@example.com", "Owner", "google-fwd-norecip")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-norecip-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-norecip.example.com", user.ID)
	store.VerifyDomain(ctx, "fwd-norecip.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "agent@fwd-norecip.example.com", "fwd-norecip.example.com", "", "https://example.com/webhook", "", user.ID)

	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "agent@fwd-norecip.example.com", "<orig@gmail.com>", "Hello", "", "", nil, nil, nil, nil, nil)

	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/agent@fwd-norecip.example.com/messages/"+msg.ID+"/forward",
		bytes.NewBufferString(`{"body":"fyi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestForwardMessageViaSMTP(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-smtp@example.com", "Owner", "google-fwd-smtp")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-smtp-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-smtp.example.com", user.ID)
	store.VerifyDomain(ctx, "fwd-smtp.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@fwd-smtp.example.com", "fwd-smtp.example.com", "", "https://example.com/webhook", "", user.ID)

	raw := rawInboundForForwardTest("alice@gmail.com", "bot@fwd-smtp.example.com", "Original Subject", "original body content")
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "bot@fwd-smtp.example.com", "<orig@gmail.com>", "Original Subject", "", "", raw, nil, nil, nil, nil)

	payload := `{"to":["destination@example.com"],"body":"FYI — see below"}`
	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/bot@fwd-smtp.example.com/messages/"+msg.ID+"/forward",
		bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
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

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	if msgs[0].To != "destination@example.com" {
		t.Errorf("SMTP To = %q, want destination@example.com (the forward target, NOT the original sender)", msgs[0].To)
	}
	if !strings.Contains(msgs[0].Data, "Subject: Fwd: Original Subject") {
		t.Errorf("SMTP body missing Subject prefix Fwd:\nbody snippet: %s", firstNLines(msgs[0].Data, 20))
	}
	if !strings.Contains(msgs[0].Data, "FYI — see below") {
		t.Errorf("SMTP body missing caller comment")
	}
	if !strings.Contains(msgs[0].Data, "---------- Forwarded message ---------") {
		t.Errorf("SMTP body missing forwarded divider")
	}
	if !strings.Contains(msgs[0].Data, "From: alice@gmail.com") {
		t.Errorf("SMTP body missing original From in quoted block")
	}
	if !strings.Contains(msgs[0].Data, "original body content") {
		t.Errorf("SMTP body missing original body content")
	}
	// Forward must NOT inherit reply threading headers
	if strings.Contains(msgs[0].Data, "In-Reply-To:") {
		t.Errorf("SMTP body unexpectedly contains In-Reply-To header — forward should be a new thread")
	}
}

func TestForwardMessageHITLHolds(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-hitl@example.com", "Owner", "google-fwd-hitl")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-hitl-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-hitl.example.com", user.ID)
	store.VerifyDomain(ctx, "fwd-hitl.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@fwd-hitl.example.com", "fwd-hitl.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	raw := rawInboundForForwardTest("alice@gmail.com", "bot@fwd-hitl.example.com", "Original", "body")
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "bot@fwd-hitl.example.com", "<orig@gmail.com>", "Original", "", "", raw, nil, nil, nil, nil)

	payload := `{"to":["dest@example.com"],"body":"please review"}`
	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/bot@fwd-hitl.example.com/messages/"+msg.ID+"/forward",
		bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	var body struct {
		Status    string `json:"status"`
		MessageID string `json:"message_id"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Status != "pending_approval" {
		t.Errorf("status = %q, want pending_approval", body.Status)
	}

	// SMTP must not have been touched.
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("expected 0 SMTP messages, got %d", len(msgs))
	}

	// DB row must have type="forward" and persist the original
	// email_message_id (so the reviewer can see what's being forwarded
	// via InboundContext).
	var (
		status, subject string
		msgType, emid   *string
	)
	err := pool.QueryRow(ctx,
		`SELECT status, subject, message_type, email_message_id
		 FROM messages WHERE id = $1`, body.MessageID,
	).Scan(&status, &subject, &msgType, &emid)
	if err != nil {
		t.Fatalf("read pending row: %v", err)
	}
	if status != identity.MessageStatusPendingApproval {
		t.Errorf("status = %q, want pending_approval", status)
	}
	if subject != "Fwd: Original" {
		t.Errorf("subject = %q, want %q", subject, "Fwd: Original")
	}
	if msgType == nil || *msgType != "forward" {
		t.Errorf("message_type = %v, want forward", msgType)
	}
	if emid == nil || *emid != "<orig@gmail.com>" {
		t.Errorf("email_message_id = %v, want <orig@gmail.com>", emid)
	}
}

func firstNLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

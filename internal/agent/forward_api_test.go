package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestForwardMessageSelfForwardUsesLoopback(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-self@example.com", "Owner", "google-fwd-self")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-self-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-self.example.com", user.ID)
	store.VerifyDomain(ctx, "fwd-self.example.com", user.ID)
	// agent_mode=local so we don't need a webhook URL — the loopback
	// delivery writes the inbound row directly.
	agent, _ := store.CreateAgent(ctx, "bot@fwd-self.example.com", "fwd-self.example.com", "", "", "local", user.ID)

	raw := rawInboundForForwardTest("alice@gmail.com", "bot@fwd-self.example.com", "Original", "body line")
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "bot@fwd-self.example.com", "<orig@gmail.com>", "Original", "", "", raw, nil, nil, nil, nil)

	// Forward to the agent's OWN address — should short-circuit SMTP.
	payload := `{"to":["bot@fwd-self.example.com"],"body":"loopback me"}`
	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/bot@fwd-self.example.com/messages/"+msg.ID+"/forward",
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
	if result["method"] != "loopback" {
		t.Errorf("method = %q, want loopback (SMTP must not be involved on self-forward)", result["method"])
	}
	if result["status"] != "sent" {
		t.Errorf("status = %q, want sent", result["status"])
	}

	// SMTP must NOT have been touched.
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("expected 0 SMTP messages on self-forward, got %d", len(msgs))
	}

	// Two outbound rows now exist for the agent: the original inbound
	// (direction=inbound) created by setup, plus a new outbound
	// (type=forward) AND a new inbound for the loopback delivery.
	var outboundForwards, inboundLoopback int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='outbound' AND message_type='forward'`,
		agent.ID).Scan(&outboundForwards)
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound' AND sender='bot@fwd-self.example.com'`,
		agent.ID).Scan(&inboundLoopback)
	if outboundForwards != 1 {
		t.Errorf("outbound forward rows = %d, want 1", outboundForwards)
	}
	if inboundLoopback != 1 {
		t.Errorf("inbound loopback rows = %d, want 1", inboundLoopback)
	}
}

func TestForwardMessageHTMLAtSMTP(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-html@example.com", "Owner", "google-fwd-html")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-html-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-html.example.com", user.ID)
	store.VerifyDomain(ctx, "fwd-html.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@fwd-html.example.com", "fwd-html.example.com", "", "https://example.com/webhook", "", user.ID)

	// Multipart inbound with both text and HTML parts — the forward
	// should preserve both at the SMTP wire.
	boundary := "INBOUNDBOUND"
	raw := []byte("From: alice@gmail.com\r\n" +
		"To: bot@fwd-html.example.com\r\n" +
		"Subject: Multi-part\r\n" +
		"Message-ID: <orig-html@gmail.com>\r\n" +
		"Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n" +
		"\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"text part body\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<p>html part body</p>\r\n" +
		"--" + boundary + "--\r\n")
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "bot@fwd-html.example.com", "<orig-html@gmail.com>", "Multi-part", "", "", raw, nil, nil, nil, nil)

	payload := `{"to":["dest@example.com"],"body":"plain comment","html_body":"<p>html comment</p>"}`
	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/bot@fwd-html.example.com/messages/"+msg.ID+"/forward",
		bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	body := msgs[0].Data

	if !strings.Contains(body, "multipart/alternative") {
		t.Errorf("SMTP body missing multipart/alternative Content-Type")
	}
	if !strings.Contains(body, "Content-Type: text/plain") {
		t.Errorf("SMTP body missing text/plain part")
	}
	if !strings.Contains(body, "Content-Type: text/html") {
		t.Errorf("SMTP body missing text/html part")
	}
	if !strings.Contains(body, "plain comment") {
		t.Errorf("SMTP body missing plain-text caller comment")
	}
	// HTML can be QP-encoded over the wire, so check the HTML
	// comment substring; the forwarded original HTML follows.
	if !strings.Contains(body, "html comment") {
		t.Errorf("SMTP body missing html caller comment")
	}
	if !strings.Contains(body, "html part body") {
		t.Errorf("SMTP body missing original HTML content quoted from forwarded message")
	}
}

func TestForwardMessageWithAttachments(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-att@example.com", "Owner", "google-fwd-att")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-att-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-att.example.com", user.ID)
	store.VerifyDomain(ctx, "fwd-att.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@fwd-att.example.com", "fwd-att.example.com", "", "https://example.com/webhook", "", user.ID)

	raw := rawInboundForForwardTest("alice@gmail.com", "bot@fwd-att.example.com", "Original", "body")
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "bot@fwd-att.example.com", "<orig-att@gmail.com>", "Original", "", "", raw, nil, nil, nil, nil)

	// "hello" → "aGVsbG8=" base64
	payload := `{
	  "to":["dest@example.com"],
	  "body":"see attached",
	  "attachments":[
	    {"filename":"note.txt","content_type":"text/plain","data":"aGVsbG8="}
	  ]
	}`
	req, _ := http.NewRequest("POST",
		server.URL+"/api/v1/agents/bot@fwd-att.example.com/messages/"+msg.ID+"/forward",
		bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	body := msgs[0].Data

	if !strings.Contains(body, `filename="note.txt"`) {
		t.Errorf("SMTP body missing attachment filename")
	}
	if !strings.Contains(body, "Content-Disposition: attachment") {
		t.Errorf("SMTP body missing attachment Content-Disposition")
	}
	if !strings.Contains(body, "aGVsbG8=") {
		t.Errorf("SMTP body missing attachment data (base64 of 'hello')")
	}
}

func TestForwardMessageIdempotentReplay(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-fwd-idem@example.com", "Owner", "google-fwd-idem")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "fwd-idem-key", nil)
	store.ClaimOrCreateDomain(ctx, "fwd-idem.example.com", user.ID)
	store.VerifyDomain(ctx, "fwd-idem.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@fwd-idem.example.com", "fwd-idem.example.com", "", "https://example.com/webhook", "", user.ID)

	raw := rawInboundForForwardTest("alice@gmail.com", "bot@fwd-idem.example.com", "Original", "body")
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "bot@fwd-idem.example.com", "<orig-idem@gmail.com>", "Original", "", "", raw, nil, nil, nil, nil)

	url := server.URL + "/api/v1/agents/bot@fwd-idem.example.com/messages/" + msg.ID + "/forward"
	payload := `{"to":["dest@example.com"],"body":"once"}`
	idemKey := "fwd-idem-key-001"

	doForward := func() (*http.Response, []byte) {
		req, _ := http.NewRequest("POST", url, bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
		req.Header.Set("Idempotency-Key", idemKey)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("forward: %v", err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		return r, b
	}

	r1, b1 := doForward()
	if r1.StatusCode != 200 {
		t.Fatalf("first status = %d, body=%s", r1.StatusCode, b1)
	}
	if r1.Header.Get("Idempotent-Replayed") != "" {
		t.Errorf("first call should not be marked replayed")
	}

	r2, b2 := doForward()
	if r2.StatusCode != 200 {
		t.Fatalf("second status = %d, body=%s", r2.StatusCode, b2)
	}
	if r2.Header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("second call must be marked Idempotent-Replayed=true, got %q", r2.Header.Get("Idempotent-Replayed"))
	}
	if !bytes.Equal(b1, b2) {
		t.Errorf("replay body diverged:\nfirst:  %s\nsecond: %s", b1, b2)
	}

	if msgs := smtpDone(); len(msgs) != 1 {
		t.Errorf("SMTP messages = %d, want exactly 1 (replay must not re-send)", len(msgs))
	}
}

package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// enableHITL is a tiny helper that enables HITL on an existing agent using
// default TTL + reject-on-expiry.
func enableHITL(t *testing.T, store *identity.Store, agentID, userID string) {
	t.Helper()
	if err := store.UpdateAgentHITL(
		context.Background(), agentID, userID,
		true, identity.HITLDefaultTTLSeconds, identity.HITLExpirationReject,
	); err != nil {
		t.Fatalf("UpdateAgentHITL: %v", err)
	}
}

func TestSendEmailHITLGateHolds(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-hitl-send@example.com", "Owner", "google-hitl-send")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "hitl-send-key", nil)
	store.ClaimOrCreateDomain(ctx, "hitl-send.example.com", user.ID)
	store.VerifyDomain(ctx, "hitl-send.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@hitl-send.example.com", "hitl-send.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	payload := `{"to":["alice@example.com"],"cc":["carol@example.com"],"bcc":["dave@example.com"],"subject":"Hello","body":"Plain body","html_body":"<p>HTML body</p>","conversation_id":"conv_xyz"}`
	before := time.Now()
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/send", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	after := time.Now()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	var body struct {
		Status            string    `json:"status"`
		MessageID         string    `json:"message_id"`
		ApprovalExpiresAt time.Time `json:"approval_expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "pending_approval" {
		t.Errorf("response status = %q, want pending_approval", body.Status)
	}
	if body.MessageID == "" {
		t.Error("response missing message_id")
	}
	if body.ApprovalExpiresAt.Before(before) || body.ApprovalExpiresAt.After(after.Add(8*24*time.Hour)) {
		t.Errorf("approval_expires_at out of expected range: %v", body.ApprovalExpiresAt)
	}

	// SMTP must not have been touched.
	msgs := smtpDone()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 SMTP messages (held for approval), got %d", len(msgs))
	}

	// DB row: status, recipients, subject, body, conversation_id, type.
	var (
		status, subject, convID string
		msgType                 *string
		toR, cc, bcc            []string
		bodyText, bodyHTML      *string
	)
	err = pool.QueryRow(ctx,
		`SELECT status, subject, conversation_id, message_type,
		        to_recipients, cc, bcc, body_text, body_html
		 FROM messages WHERE id = $1`, body.MessageID,
	).Scan(&status, &subject, &convID, &msgType, &toR, &cc, &bcc, &bodyText, &bodyHTML)
	if err != nil {
		t.Fatalf("read pending row: %v", err)
	}
	if status != identity.MessageStatusPendingApproval {
		t.Errorf("status = %q, want %q", status, identity.MessageStatusPendingApproval)
	}
	if subject != "Hello" {
		t.Errorf("subject = %q", subject)
	}
	if convID != "conv_xyz" {
		t.Errorf("conversation_id = %q", convID)
	}
	if msgType == nil || *msgType != "send" {
		t.Errorf("message_type = %v, want send", msgType)
	}
	if len(toR) != 1 || toR[0] != "alice@example.com" {
		t.Errorf("to_recipients = %v", toR)
	}
	if len(cc) != 1 || cc[0] != "carol@example.com" {
		t.Errorf("cc = %v", cc)
	}
	if len(bcc) != 1 || bcc[0] != "dave@example.com" {
		t.Errorf("bcc = %v", bcc)
	}
	if bodyText == nil || *bodyText != "Plain body" {
		t.Errorf("body_text = %v", bodyText)
	}
	if bodyHTML == nil || *bodyHTML != "<p>HTML body</p>" {
		t.Errorf("body_html = %v", bodyHTML)
	}
}

func TestSendEmailHITLGatePersistsAttachments(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-hitl-att@example.com", "Owner", "google-hitl-att")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "hitl-att-key", nil)
	store.ClaimOrCreateDomain(ctx, "hitl-att.example.com", user.ID)
	store.VerifyDomain(ctx, "hitl-att.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@hitl-att.example.com", "hitl-att.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	payload := `{
	  "to":["alice@example.com"],
	  "subject":"With attachment",
	  "body":"see attached",
	  "attachments":[{"filename":"hello.txt","content_type":"text/plain","data":"aGVsbG8="}]
	}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/send", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var body struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(resp.Body).Decode(&body)

	if len(smtpDone()) != 0 {
		t.Fatal("SMTP should not be hit when HITL is enabled")
	}

	var attachments []byte
	err = pool.QueryRow(ctx,
		`SELECT attachments_json FROM messages WHERE id = $1`, body.MessageID,
	).Scan(&attachments)
	if err != nil {
		t.Fatalf("read attachments: %v", err)
	}
	var got []map[string]string
	if err := json.Unmarshal(attachments, &got); err != nil {
		t.Fatalf("unmarshal attachments_json: %v", err)
	}
	if len(got) != 1 || got[0]["filename"] != "hello.txt" || got[0]["data"] != "aGVsbG8=" {
		t.Errorf("attachments round-trip mismatch: %v", got)
	}
}

func TestSendEmailHITLOffStillSends(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-hitl-off@example.com", "Owner", "google-hitl-off")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "hitl-off-key", nil)
	store.ClaimOrCreateDomain(ctx, "hitl-off.example.com", user.ID)
	store.VerifyDomain(ctx, "hitl-off.example.com", user.ID)
	store.CreateAgent(ctx, "bot@hitl-off.example.com", "hitl-off.example.com", "", "https://example.com/webhook", "", user.ID)

	payload := `{"to":["alice@example.com"],"subject":"Hello","body":"Plain body"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/send", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}

	// Every non-HITL send should record a row with status='sent' (never pending).
	var pendingCount int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM messages WHERE status = 'pending_approval'`,
	).Scan(&pendingCount)
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pendingCount != 0 {
		t.Errorf("pending_approval rows = %d, want 0 when HITL is off", pendingCount)
	}
}

func TestReplyHITLGateHoldsWithReplyTo(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-hitl-reply@example.com", "Owner", "google-hitl-reply")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "hitl-reply-key", nil)
	store.ClaimOrCreateDomain(ctx, "hitl-reply.example.com", user.ID)
	store.VerifyDomain(ctx, "hitl-reply.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@hitl-reply.example.com", "hitl-reply.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	inbound, _ := store.CreateInboundMessage(ctx, "", agent.ID,
		"alice@gmail.com", "bot@hitl-reply.example.com",
		"<orig@gmail.com>", "Hello Bot", "", "", nil, nil, nil, nil, nil)

	payload := `{"body":"Thanks!","html_body":"<p>Thanks!</p>","conversation_id":"conv_r"}`
	url := server.URL + "/api/v1/agents/bot@hitl-reply.example.com/messages/" + inbound.ID + "/reply"
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Errorf("response status = %q, want pending_approval", body.Status)
	}

	if len(smtpDone()) != 0 {
		t.Fatal("SMTP should not be hit when HITL is enabled (reply path)")
	}

	// The reply-to Message-ID must be stored so approval can reconstruct
	// proper In-Reply-To/References headers.
	var status, subject, emailMsgID, convID string
	var msgType *string
	var bodyText *string
	err = pool.QueryRow(ctx,
		`SELECT status, subject, email_message_id, conversation_id, message_type, body_text
		 FROM messages WHERE id = $1`, body.MessageID,
	).Scan(&status, &subject, &emailMsgID, &convID, &msgType, &bodyText)
	if err != nil {
		t.Fatalf("read pending row: %v", err)
	}
	if status != identity.MessageStatusPendingApproval {
		t.Errorf("status = %q", status)
	}
	if subject != "Re: Hello Bot" {
		t.Errorf("subject = %q, want 'Re: Hello Bot'", subject)
	}
	if emailMsgID != "<orig@gmail.com>" {
		t.Errorf("email_message_id = %q, want <orig@gmail.com>", emailMsgID)
	}
	if convID != "conv_r" {
		t.Errorf("conversation_id = %q", convID)
	}
	if msgType == nil || *msgType != "reply" {
		t.Errorf("message_type = %v, want reply", msgType)
	}
	if bodyText == nil || *bodyText != "Thanks!" {
		t.Errorf("body_text = %v", bodyText)
	}
}

func TestSendTestEmailHITLGate(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-hitl-test@example.com", "Owner", "google-hitl-test")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "hitl-test-key", nil)
	store.ClaimOrCreateDomain(ctx, "hitl-test.example.com", user.ID)
	store.VerifyDomain(ctx, "hitl-test.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@hitl-test.example.com", "hitl-test.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	url := server.URL + "/api/v1/agents/bot@hitl-test.example.com/test"
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	var body struct {
		Status    string `json:"status"`
		MessageID string `json:"message_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "pending_approval" {
		t.Errorf("response status = %q, want pending_approval", body.Status)
	}
	if body.MessageID == "" {
		t.Fatal("response missing message_id")
	}

	if len(smtpDone()) != 0 {
		t.Fatal("SMTP should not be hit when HITL is enabled (test path)")
	}

	var status string
	var msgType *string
	var toR []string
	err = pool.QueryRow(ctx,
		`SELECT status, message_type, to_recipients FROM messages WHERE id = $1`, body.MessageID,
	).Scan(&status, &msgType, &toR)
	if err != nil {
		t.Fatalf("read pending row: %v", err)
	}
	if status != identity.MessageStatusPendingApproval {
		t.Errorf("status = %q", status)
	}
	if msgType == nil || *msgType != "test" {
		t.Errorf("message_type = %v, want test", msgType)
	}
	if len(toR) != 1 || toR[0] != agent.EmailAddress() {
		t.Errorf("to_recipients = %v, want [%s]", toR, agent.EmailAddress())
	}
}

// TestSendTestEmailHITLApproveDeliversViaLoopback: the original
// production repro for PR #109. With HITL on, the Test email button
// holds a self-send; clicking approve via the dashboard endpoint must
// finalize via loopback. Before the fix this errored with
// "no valid recipients" because the approval finalizer routed through
// outbound.Sender.Send, which strips the agent's own address.
func TestSendTestEmailHITLApproveDeliversViaLoopback(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-hitl-test-approve@example.com", "Owner", "google-hitl-test-approve")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "hitl-test-approve-key", nil)
	store.ClaimOrCreateDomain(ctx, "hitl-test-approve.example.com", user.ID)
	store.VerifyDomain(ctx, "hitl-test-approve.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@hitl-test-approve.example.com", "hitl-test-approve.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	// Step 1: click Test → held for approval.
	testReq, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/bot@hitl-test-approve.example.com/test", nil)
	testReq.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	testResp, err := http.DefaultClient.Do(testReq)
	if err != nil {
		t.Fatal(err)
	}
	defer testResp.Body.Close()
	if testResp.StatusCode != http.StatusAccepted {
		t.Fatalf("test hold status = %d, want 202", testResp.StatusCode)
	}
	var holdBody struct {
		Status    string `json:"status"`
		MessageID string `json:"message_id"`
	}
	if err := json.NewDecoder(testResp.Body).Decode(&holdBody); err != nil {
		t.Fatal(err)
	}
	if holdBody.MessageID == "" {
		t.Fatal("hold response missing message_id")
	}

	// Step 2: approve via the dashboard endpoint (agent-scoped path).
	approveReq, _ := http.NewRequest("POST", server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+holdBody.MessageID+"/approve", bytes.NewBufferString(`{}`))
	approveReq.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	approveReq.Header.Set("Content-Type", "application/json")
	approveResp, err := http.DefaultClient.Do(approveReq)
	if err != nil {
		t.Fatal(err)
	}
	defer approveResp.Body.Close()
	if approveResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(approveResp.Body)
		t.Fatalf("approve status = %d, want 200; body=%s", approveResp.StatusCode, body)
	}
	var approveBody map[string]interface{}
	json.NewDecoder(approveResp.Body).Decode(&approveBody)
	if approveBody["method"] != "loopback" {
		t.Errorf("approve method = %v, want loopback", approveBody["method"])
	}

	// Loopback path → no SMTP traffic at all.
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("self-send test-email approve must not hit SMTP, got %d messages", len(msgs))
	}

	// Outbound row → sent + loopback; inbound row landed in the agent's mailbox.
	var status, method string
	pool.QueryRow(ctx,
		`SELECT status, method FROM messages WHERE id=$1`, holdBody.MessageID,
	).Scan(&status, &method)
	if status != identity.MessageStatusSent {
		t.Errorf("outbound status = %q, want sent", status)
	}
	if method != "loopback" {
		t.Errorf("outbound method = %q, want loopback", method)
	}

	var inboundCount int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='Test email from e2a'`,
		agent.ID).Scan(&inboundCount)
	if inboundCount != 1 {
		t.Errorf("inbound rows = %d, want 1", inboundCount)
	}
}

package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// authed does an authenticated request of arbitrary method.
func authed(t *testing.T, method, url, body, apiKey string) *http.Response {
	t.Helper()
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = bytes.NewBuffer(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestListPendingMessagesHandler(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-list@example.com", "Owner", "google-list-handler")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "list-key", nil)
	store.ClaimOrCreateDomain(ctx, "list-h.example.com", user.ID)
	store.VerifyDomain(ctx, "list-h.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@list-h.example.com", "list-h.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	// Submit two sends via the API so pending rows exist.
	payloads := []string{
		`{"to":["a@example.com"],"subject":"First","body":"b"}`,
		`{"to":["b@example.com"],"subject":"Second","body":"b"}`,
	}
	for _, p := range payloads {
		resp := authed(t, "POST", server.URL+"/api/v1/send", p, apiKey.PlaintextKey)
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("send: status = %d, want 202", resp.StatusCode)
		}
	}

	resp := authed(t, "GET", server.URL+"/api/v1/messages?status=pending_approval", "", apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d", resp.StatusCode)
	}
	var body struct {
		Messages []struct {
			ID, Subject, Status, Direction string
			To                             []string
			BodyText                       string `json:"body_text"`
			BodyHTML                       string `json:"body_html"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(body.Messages))
	}
	for _, m := range body.Messages {
		if m.Status != "pending_approval" {
			t.Errorf("status = %q", m.Status)
		}
		if m.Direction != "outbound" {
			t.Errorf("direction = %q", m.Direction)
		}
		// Summary must not include body
		if m.BodyText != "" || m.BodyHTML != "" {
			t.Errorf("summary leaked body: text=%q html=%q", m.BodyText, m.BodyHTML)
		}
	}
}

func TestListPendingMessagesRejectsOtherStatus(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-listreject@example.com", "Owner", "google-list-reject")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "list-reject-key", nil)

	resp := authed(t, "GET", server.URL+"/api/v1/messages?status=sent", "", apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (only pending_approval supported)", resp.StatusCode)
	}
}

func TestGetOutboundMessageHandler(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-getdetail@example.com", "Owner", "google-get-detail")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "get-detail-key", nil)
	store.ClaimOrCreateDomain(ctx, "get-detail.example.com", user.ID)
	store.VerifyDomain(ctx, "get-detail.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@get-detail.example.com", "get-detail.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	send := `{"to":["alice@example.com"],"subject":"Hello","body":"plain body","html_body":"<p>html</p>","attachments":[{"filename":"f.txt","content_type":"text/plain","data":"aGk="}]}`
	resp := authed(t, "POST", server.URL+"/api/v1/send", send, apiKey.PlaintextKey)
	defer resp.Body.Close()
	var sendResp struct {
		MessageID string `json:"message_id"`
	}
	json.NewDecoder(resp.Body).Decode(&sendResp)

	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+sendResp.MessageID, "", apiKey.PlaintextKey)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get detail: status = %d", getResp.StatusCode)
	}
	var detail map[string]interface{}
	if err := json.NewDecoder(getResp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail["body_text"] != "plain body" {
		t.Errorf("body_text = %v", detail["body_text"])
	}
	if detail["body_html"] != "<p>html</p>" {
		t.Errorf("body_html = %v", detail["body_html"])
	}
	atts, ok := detail["attachments"].([]interface{})
	if !ok || len(atts) != 1 {
		t.Fatalf("attachments missing from detail: %v", detail["attachments"])
	}
	att := atts[0].(map[string]interface{})
	if att["filename"] != "f.txt" {
		t.Errorf("attachment filename = %v", att["filename"])
	}
}

func TestGetOutboundMessageCrossUserReturns404(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	// User A creates pending
	userA, _ := store.CreateOrGetUser(ctx, "owner-cross-a@example.com", "A", "google-cross-a")
	keyA, _ := store.CreateAPIKey(ctx, userA.ID, "a-key", nil)
	store.ClaimOrCreateDomain(ctx, "cross-a.example.com", userA.ID)
	store.VerifyDomain(ctx, "cross-a.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "bot@cross-a.example.com", "cross-a.example.com", "", "https://example.com/webhook", "", userA.ID)
	enableHITL(t, store, agentA.ID, userA.ID)

	// User B (no access to A's messages)
	userB, _ := store.CreateOrGetUser(ctx, "owner-cross-b@example.com", "B", "google-cross-b")
	keyB, _ := store.CreateAPIKey(ctx, userB.ID, "b-key", nil)

	resp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["x@example.com"],"subject":"h","body":"b"}`, keyA.PlaintextKey)
	var sendResp struct {
		MessageID string `json:"message_id"`
	}
	json.NewDecoder(resp.Body).Decode(&sendResp)
	resp.Body.Close()

	// User B tries to fetch User A's pending message
	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+sendResp.MessageID, "", keyB.PlaintextKey)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user GET: status = %d, want 404", getResp.StatusCode)
	}
}

func TestApprovePendingMessageSendsViaSMTP(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-approve@example.com", "Owner", "google-approve-handler")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "approve-key", nil)
	store.ClaimOrCreateDomain(ctx, "approve-h.example.com", user.ID)
	store.VerifyDomain(ctx, "approve-h.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@approve-h.example.com", "approve-h.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	// Create a pending message via the send endpoint
	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["alice@example.com"],"subject":"Hello","body":"plain body","html_body":"<p>HTML</p>"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)

	// Approve-as-is
	appResp := authed(t, "POST",
		server.URL+"/api/v1/messages/"+sendBody.MessageID+"/approve",
		"", apiKey.PlaintextKey)
	defer appResp.Body.Close()
	if appResp.StatusCode != http.StatusOK {
		t.Fatalf("approve: status = %d", appResp.StatusCode)
	}
	var appBody struct {
		Status            string `json:"status"`
		MessageID         string `json:"message_id"`
		ProviderMessageID string `json:"provider_message_id"`
		Method            string `json:"method"`
		Edited            bool   `json:"edited"`
	}
	json.NewDecoder(appResp.Body).Decode(&appBody)
	if appBody.Status != "sent" {
		t.Errorf("response status = %q", appBody.Status)
	}
	if appBody.MessageID != sendBody.MessageID {
		t.Errorf("message_id mismatch: %q vs %q", appBody.MessageID, sendBody.MessageID)
	}
	if appBody.ProviderMessageID == "" {
		t.Error("provider_message_id missing")
	}
	if appBody.Method != "smtp" {
		t.Errorf("method = %q", appBody.Method)
	}
	if appBody.Edited {
		t.Error("edited should be false for approve-as-is")
	}

	// SMTP server received exactly one message with the expected headers/body.
	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	if msgs[0].To != "alice@example.com" {
		t.Errorf("SMTP To = %q", msgs[0].To)
	}
	if !strings.Contains(msgs[0].Data, "plain body") {
		t.Errorf("SMTP body missing plain text:\n%s", msgs[0].Data)
	}
	if !strings.Contains(msgs[0].Data, "Hello") {
		t.Errorf("SMTP missing subject:\n%s", msgs[0].Data)
	}
}

func TestApprovePendingMessageWithEditsSendsEditedContent(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-apprv-edit@example.com", "Owner", "google-apprv-edit")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "apprv-edit-key", nil)
	store.ClaimOrCreateDomain(ctx, "apprv-edit.example.com", user.ID)
	store.VerifyDomain(ctx, "apprv-edit.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@apprv-edit.example.com", "apprv-edit.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["alice@example.com"],"subject":"Draft","body":"original"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)

	editPayload := `{"subject":"Edited subject","body_text":"edited body","to":["bob@example.com"]}`
	appResp := authed(t, "POST",
		server.URL+"/api/v1/messages/"+sendBody.MessageID+"/approve",
		editPayload, apiKey.PlaintextKey)
	defer appResp.Body.Close()
	if appResp.StatusCode != http.StatusOK {
		t.Fatalf("approve with edits: status = %d", appResp.StatusCode)
	}
	var appBody struct {
		Edited bool `json:"edited"`
	}
	json.NewDecoder(appResp.Body).Decode(&appBody)
	if !appBody.Edited {
		t.Error("response.edited should be true")
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	if msgs[0].To != "bob@example.com" {
		t.Errorf("SMTP To = %q, want bob@example.com (edited)", msgs[0].To)
	}
	if !strings.Contains(msgs[0].Data, "Edited subject") {
		t.Errorf("SMTP missing edited subject:\n%s", msgs[0].Data)
	}
	if !strings.Contains(msgs[0].Data, "edited body") {
		t.Errorf("SMTP missing edited body:\n%s", msgs[0].Data)
	}
	if strings.Contains(msgs[0].Data, "original") {
		t.Errorf("SMTP should not contain 'original' body text:\n%s", msgs[0].Data)
	}
}

func TestApproveAlreadySentReturns409(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-apprv-409@example.com", "Owner", "google-apprv-409")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "apprv-409-key", nil)
	store.ClaimOrCreateDomain(ctx, "apprv-409.example.com", user.ID)
	store.VerifyDomain(ctx, "apprv-409.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@apprv-409.example.com", "apprv-409.example.com", "", "https://example.com/webhook", "", user.ID)

	// HITL off for the send → goes straight to sent
	sent, _ := store.CreateOutboundMessage(ctx, agent.ID, []string{"a@example.com"}, nil, nil,
		"already sent", "send", "smtp", "<p>", "")

	resp := authed(t, "POST",
		server.URL+"/api/v1/messages/"+sent.ID+"/approve",
		"", apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestApproveReplyFromHITLUsesStoredReplyTo(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-apprv-reply@example.com", "Owner", "google-apprv-reply")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "apprv-reply-key", nil)
	store.ClaimOrCreateDomain(ctx, "apprv-reply.example.com", user.ID)
	store.VerifyDomain(ctx, "apprv-reply.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@apprv-reply.example.com", "apprv-reply.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	inbound, _ := store.CreateInboundMessage(ctx, "", agent.ID,
		"alice@gmail.com", "bot@apprv-reply.example.com",
		"<orig@gmail.com>", "Hello Bot", "", "", nil, nil, nil, nil, nil)

	replyResp := authed(t, "POST",
		server.URL+"/api/v1/agents/bot@apprv-reply.example.com/messages/"+inbound.ID+"/reply",
		`{"body":"Sure!"}`, apiKey.PlaintextKey)
	defer replyResp.Body.Close()
	var replyBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(replyResp.Body).Decode(&replyBody)

	appResp := authed(t, "POST",
		server.URL+"/api/v1/messages/"+replyBody.MessageID+"/approve",
		"", apiKey.PlaintextKey)
	defer appResp.Body.Close()
	if appResp.StatusCode != http.StatusOK {
		t.Fatalf("approve reply: status = %d", appResp.StatusCode)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	// The In-Reply-To header should reference the original inbound Message-ID
	if !strings.Contains(msgs[0].Data, "In-Reply-To: <orig@gmail.com>") {
		t.Errorf("approved reply missing In-Reply-To header:\n%s", msgs[0].Data)
	}
}

func TestRejectPendingMessageHandler(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-reject-h@example.com", "Owner", "google-reject-h")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "reject-h-key", nil)
	store.ClaimOrCreateDomain(ctx, "reject-h.example.com", user.ID)
	store.VerifyDomain(ctx, "reject-h.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@reject-h.example.com", "reject-h.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["a@example.com"],"subject":"h","body":"b"}`, apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)

	rejResp := authed(t, "POST",
		server.URL+"/api/v1/messages/"+sendBody.MessageID+"/reject",
		`{"reason":"inappropriate tone"}`, apiKey.PlaintextKey)
	defer rejResp.Body.Close()
	if rejResp.StatusCode != http.StatusOK {
		t.Fatalf("reject: status = %d", rejResp.StatusCode)
	}
	var rejBody struct {
		Status          string `json:"status"`
		RejectionReason string `json:"rejection_reason"`
	}
	json.NewDecoder(rejResp.Body).Decode(&rejBody)
	if rejBody.Status != "rejected" {
		t.Errorf("status = %q", rejBody.Status)
	}
	if rejBody.RejectionReason != "inappropriate tone" {
		t.Errorf("reason = %q", rejBody.RejectionReason)
	}

	// Row scrubbed in DB
	var bodyText, bodyHTML *string
	var attachments []byte
	err := pool.QueryRow(ctx,
		`SELECT body_text, body_html, attachments_json FROM messages WHERE id = $1`,
		sendBody.MessageID).Scan(&bodyText, &bodyHTML, &attachments)
	if err != nil {
		t.Fatal(err)
	}
	if bodyText != nil || bodyHTML != nil || attachments != nil {
		t.Errorf("row not scrubbed after reject")
	}
}

func TestRejectAlreadyRejectedReturns409(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-reject-twice@example.com", "Owner", "google-reject-twice-h")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "reject-twice-key", nil)
	store.ClaimOrCreateDomain(ctx, "reject-twice.example.com", user.ID)
	store.VerifyDomain(ctx, "reject-twice.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@reject-twice.example.com", "reject-twice.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["a@example.com"],"subject":"h","body":"b"}`, apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)

	resp1 := authed(t, "POST",
		server.URL+"/api/v1/messages/"+sendBody.MessageID+"/reject",
		`{"reason":"first"}`, apiKey.PlaintextKey)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first reject: status = %d", resp1.StatusCode)
	}

	resp2 := authed(t, "POST",
		server.URL+"/api/v1/messages/"+sendBody.MessageID+"/reject",
		`{"reason":"second"}`, apiKey.PlaintextKey)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("second reject: status = %d, want 409", resp2.StatusCode)
	}
}

func TestApproveCrossUserReturns404(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	userA, _ := store.CreateOrGetUser(ctx, "a-apprv-cross@example.com", "A", "google-a-cross")
	keyA, _ := store.CreateAPIKey(ctx, userA.ID, "a-cross-key", nil)
	store.ClaimOrCreateDomain(ctx, "a-cross.example.com", userA.ID)
	store.VerifyDomain(ctx, "a-cross.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "bot@a-cross.example.com", "a-cross.example.com", "", "https://example.com/webhook", "", userA.ID)
	enableHITL(t, store, agentA.ID, userA.ID)

	userB, _ := store.CreateOrGetUser(ctx, "b-apprv-cross@example.com", "B", "google-b-cross")
	keyB, _ := store.CreateAPIKey(ctx, userB.ID, "b-cross-key", nil)

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["x@example.com"],"subject":"h","body":"b"}`, keyA.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)

	appResp := authed(t, "POST",
		server.URL+"/api/v1/messages/"+sendBody.MessageID+"/approve",
		"", keyB.PlaintextKey)
	defer appResp.Body.Close()
	if appResp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user approve: status = %d, want 404", appResp.StatusCode)
	}
}

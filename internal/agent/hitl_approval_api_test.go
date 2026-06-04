package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// authedChunked posts a body with Transfer-Encoding: chunked so the
// server sees ContentLength == -1. Used to regression-test handlers
// that previously gated body decode on ContentLength > 0 (which
// silently swallowed bodies on chunked requests).
func authedChunked(t *testing.T, method, url, body, apiKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	// Force chunked: Body is an io.Reader of unknown length; clear
	// ContentLength so net/http's transport doesn't infer a length and
	// adds Transfer-Encoding: chunked instead.
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

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

	resp := authed(t, "GET", server.URL+"/api/v1/pending", "", apiKey.PlaintextKey)
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

	resp := authed(t, "GET", server.URL+"/api/v1/pending?status=sent", "", apiKey.PlaintextKey)
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

// TestGetOutboundMessage_ReplyAttachesInboundContext: when the pending
// outbound is a reply, GET /api/v1/messages/{id} must return an
// `inbound` block populated from the parent inbound row so the review
// panel's Provenance pane has the sender, subject, timestamp, and
// SPF/DKIM/DMARC auth headers to render. Without this, the field is
// declared in the response schema but always nil → UI always shows
// the "No inbound context" fallback.
func TestGetOutboundMessage_ReplyAttachesInboundContext(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-inbound-ctx@example.com", "Owner", "google-inbound-ctx")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "inbound-ctx-key", nil)
	store.ClaimOrCreateDomain(ctx, "inbound-ctx.example.com", user.ID)
	store.VerifyDomain(ctx, "inbound-ctx.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@inbound-ctx.example.com", "inbound-ctx.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	authHeaders := map[string]string{
		"spf":   "pass",
		"dkim":  "pass",
		"dmarc": "pass",
	}
	inbound, err := store.CreateInboundMessage(ctx, "", agent.ID,
		"alice@gmail.com", "bot@inbound-ctx.example.com",
		"<orig@gmail.com>", "Hello Bot", "", "",
		nil, authHeaders, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	// Hold a reply via the API so the outbound row's
	// email_message_id points at our inbound.
	replyURL := server.URL + "/api/v1/agents/bot@inbound-ctx.example.com/messages/" + inbound.ID + "/reply"
	replyResp := authed(t, "POST", replyURL, `{"body":"Thanks!"}`, apiKey.PlaintextKey)
	defer replyResp.Body.Close()
	if replyResp.StatusCode != http.StatusAccepted {
		t.Fatalf("reply hold status = %d, want 202", replyResp.StatusCode)
	}
	var holdBody struct {
		MessageID string `json:"message_id"`
	}
	json.NewDecoder(replyResp.Body).Decode(&holdBody)
	if holdBody.MessageID == "" {
		t.Fatal("hold response missing message_id")
	}

	// GET the detail. Inbound context should be present and populated.
	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+holdBody.MessageID, "", apiKey.PlaintextKey)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get detail status = %d, want 200", getResp.StatusCode)
	}
	var detail map[string]interface{}
	if err := json.NewDecoder(getResp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}

	inboundCtx, ok := detail["inbound"].(map[string]interface{})
	if !ok {
		t.Fatalf("inbound field missing or wrong type from detail; got %T: %v", detail["inbound"], detail["inbound"])
	}
	if inboundCtx["sender"] != "alice@gmail.com" {
		t.Errorf("inbound.sender = %v, want alice@gmail.com", inboundCtx["sender"])
	}
	if inboundCtx["subject"] != "Hello Bot" {
		t.Errorf("inbound.subject = %v, want 'Hello Bot'", inboundCtx["subject"])
	}
	// created_at is an RFC 3339 timestamp; spot-check it's a non-empty string.
	createdAt, _ := inboundCtx["created_at"].(string)
	if createdAt == "" {
		t.Errorf("inbound.created_at should be set, got %v", inboundCtx["created_at"])
	}
	ah, ok := inboundCtx["auth_headers"].(map[string]interface{})
	if !ok {
		t.Fatalf("inbound.auth_headers missing or wrong type: %v", inboundCtx["auth_headers"])
	}
	for _, k := range []string{"spf", "dkim", "dmarc"} {
		if ah[k] != "pass" {
			t.Errorf("inbound.auth_headers[%s] = %v, want 'pass'", k, ah[k])
		}
	}
}

// TestGetOutboundMessage_SendHasNoInboundContext: messages that aren't
// replies (no parent inbound) have an empty email_message_id, so no
// lookup runs and the inbound field is omitted from the response.
func TestGetOutboundMessage_SendHasNoInboundContext(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-send-no-ctx@example.com", "Owner", "google-send-no-ctx")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "send-no-ctx-key", nil)
	store.ClaimOrCreateDomain(ctx, "send-no-ctx.example.com", user.ID)
	store.VerifyDomain(ctx, "send-no-ctx.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@send-no-ctx.example.com", "send-no-ctx.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["alice@example.com"],"subject":"Cold outreach","body":"hello"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var holdBody struct {
		MessageID string `json:"message_id"`
	}
	json.NewDecoder(sendResp.Body).Decode(&holdBody)

	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+holdBody.MessageID, "", apiKey.PlaintextKey)
	defer getResp.Body.Close()
	var detail map[string]interface{}
	json.NewDecoder(getResp.Body).Decode(&detail)

	// omitempty on the InboundContext field means a nil pointer drops the
	// key entirely from JSON — accept either "missing" or "explicit null".
	if v, present := detail["inbound"]; present && v != nil {
		t.Errorf("inbound should be absent or null on a non-reply, got %v", v)
	}
	if agent.ID == "" {
		t.Fatal("unreachable: agent setup must have an ID")
	}
}

// TestGetOutboundMessage_ReplyWithMissingInboundReturnsNilContext: when
// the pending reply's parent inbound has aged out of retention (or was
// otherwise removed), the lookup returns sql.ErrNoRows and the inbound
// field is omitted from the response. The UI handles this by rendering
// "No inbound context" — the reviewer still gets to act on the message
// but loses the Provenance pane.
func TestGetOutboundMessage_ReplyWithMissingInboundReturnsNilContext(t *testing.T) {
	server, store, pool, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-missing-inbound@example.com", "Owner", "google-missing-inbound")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "missing-inbound-key", nil)
	store.ClaimOrCreateDomain(ctx, "missing-inbound.example.com", user.ID)
	store.VerifyDomain(ctx, "missing-inbound.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@missing-inbound.example.com", "missing-inbound.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	inbound, err := store.CreateInboundMessage(ctx, "", agent.ID,
		"alice@gmail.com", "bot@missing-inbound.example.com",
		"<missing@gmail.com>", "Subject that will vanish", "", "",
		nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	replyURL := server.URL + "/api/v1/agents/bot@missing-inbound.example.com/messages/" + inbound.ID + "/reply"
	replyResp := authed(t, "POST", replyURL, `{"body":"reply"}`, apiKey.PlaintextKey)
	defer replyResp.Body.Close()
	var holdBody struct {
		MessageID string `json:"message_id"`
	}
	json.NewDecoder(replyResp.Body).Decode(&holdBody)

	// Simulate the inbound aging out of retention by deleting the row
	// directly. The outbound's email_message_id still points at the
	// (now-gone) parent.
	if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, inbound.ID); err != nil {
		t.Fatalf("delete parent inbound: %v", err)
	}

	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+holdBody.MessageID, "", apiKey.PlaintextKey)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get detail status = %d, want 200 (missing inbound is non-fatal)", getResp.StatusCode)
	}
	var detail map[string]interface{}
	json.NewDecoder(getResp.Body).Decode(&detail)

	if v, present := detail["inbound"]; present && v != nil {
		t.Errorf("inbound should be omitted when parent is missing, got %v", v)
	}
	// Sanity: the outbound's own email_message_id is still surfaced —
	// only the inbound block is gone.
	if detail["email_message_id"] != "<missing@gmail.com>" {
		t.Errorf("email_message_id = %v, want <missing@gmail.com>", detail["email_message_id"])
	}
}

// TestGetOutboundMessage_InboundContextIsAgentScoped: two different
// agents (different users) have inbound rows with the SAME email
// Message-ID. A reply on agent A must only attach agent A's inbound,
// even when both rows are technically alive in the messages table.
// GetInboundByEmailMessageID enforces this at the store layer; this
// test pins the HTTP-boundary behavior so a regression in
// handleGetOutboundMessage's lookup couldn't cross the boundary.
func TestGetOutboundMessage_InboundContextIsAgentScoped(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	defer smtpDone()
	ctx := context.Background()

	// Agent A — the reviewer's
	userA, _ := store.CreateOrGetUser(ctx, "agent-scoped-a@example.com", "A", "google-agent-scoped-a")
	keyA, _ := store.CreateAPIKey(ctx, userA.ID, "a-key", nil)
	store.ClaimOrCreateDomain(ctx, "scoped-a.example.com", userA.ID)
	store.VerifyDomain(ctx, "scoped-a.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "bot@scoped-a.example.com", "scoped-a.example.com", "", "https://example.com/webhook", "", userA.ID)
	enableHITL(t, store, agentA.ID, userA.ID)

	inboundA, err := store.CreateInboundMessage(ctx, "", agentA.ID,
		"alice@gmail.com", "bot@scoped-a.example.com",
		"<shared-id@gmail.com>", "Subject for A", "", "",
		nil, map[string]string{"spf": "pass"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed inbound A: %v", err)
	}

	// Agent B — a different user, same Message-ID by coincidence.
	userB, _ := store.CreateOrGetUser(ctx, "agent-scoped-b@example.com", "B", "google-agent-scoped-b")
	store.ClaimOrCreateDomain(ctx, "scoped-b.example.com", userB.ID)
	store.VerifyDomain(ctx, "scoped-b.example.com", userB.ID)
	agentB, _ := store.CreateAgent(ctx, "bot@scoped-b.example.com", "scoped-b.example.com", "", "https://example.com/webhook", "", userB.ID)
	if _, err := store.CreateInboundMessage(ctx, "", agentB.ID,
		"bob@gmail.com", "bot@scoped-b.example.com",
		"<shared-id@gmail.com>", "Subject for B (must NOT leak)", "", "",
		nil, map[string]string{"spf": "fail"}, nil, nil, nil); err != nil {
		t.Fatalf("seed inbound B: %v", err)
	}

	// Reply on agent A using agent A's inbound.
	replyURL := server.URL + "/api/v1/agents/bot@scoped-a.example.com/messages/" + inboundA.ID + "/reply"
	replyResp := authed(t, "POST", replyURL, `{"body":"r"}`, keyA.PlaintextKey)
	defer replyResp.Body.Close()
	var holdBody struct {
		MessageID string `json:"message_id"`
	}
	json.NewDecoder(replyResp.Body).Decode(&holdBody)

	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+holdBody.MessageID, "", keyA.PlaintextKey)
	defer getResp.Body.Close()
	var detail map[string]interface{}
	json.NewDecoder(getResp.Body).Decode(&detail)

	inboundCtx, ok := detail["inbound"].(map[string]interface{})
	if !ok {
		t.Fatalf("inbound block missing: %v", detail["inbound"])
	}
	// Must be A's row, never B's, even though both have the same
	// email_message_id.
	if inboundCtx["sender"] != "alice@gmail.com" {
		t.Errorf("inbound.sender = %v, want alice@gmail.com (NOT bob@gmail.com from agent B)", inboundCtx["sender"])
	}
	if inboundCtx["subject"] != "Subject for A" {
		t.Errorf("inbound.subject = %v, want 'Subject for A' (NOT B's)", inboundCtx["subject"])
	}
	ah, _ := inboundCtx["auth_headers"].(map[string]interface{})
	if ah["spf"] != "pass" {
		t.Errorf("inbound.auth_headers.spf = %v, want 'pass' (NOT 'fail' from B's row)", ah["spf"])
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
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+sendBody.MessageID+"/approve",
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
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+sendBody.MessageID+"/approve",
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
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+sent.ID+"/approve",
		"", apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

// Regression: Transfer-Encoding: chunked yields ContentLength == -1 on
// the server, and the approve handler used to skip body decode for
// non-positive ContentLength. That silently dropped the reviewer's
// overrides (subject/body/to/cc/bcc) and sent the stored draft as-is —
// a HITL invariant breach. This test posts edits via chunked encoding
// and asserts the SMTP send reflects them.
func TestApprovePendingMessageWithChunkedEditsHonorsOverrides(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-apprv-chunk@example.com", "Owner", "google-apprv-chunk")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "apprv-chunk-key", nil)
	store.ClaimOrCreateDomain(ctx, "apprv-chunk.example.com", user.ID)
	store.VerifyDomain(ctx, "apprv-chunk.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@apprv-chunk.example.com", "apprv-chunk.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["alice@example.com"],"subject":"Original subject","body":"original body"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)

	editPayload := `{"subject":"Edited via chunked","body_text":"chunked body","to":["bob@example.com"]}`
	appResp := authedChunked(t, "POST",
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+sendBody.MessageID+"/approve",
		editPayload, apiKey.PlaintextKey)
	defer appResp.Body.Close()
	if appResp.StatusCode != http.StatusOK {
		t.Fatalf("approve via chunked: status = %d", appResp.StatusCode)
	}
	var appBody struct {
		Edited bool `json:"edited"`
	}
	json.NewDecoder(appResp.Body).Decode(&appBody)
	if !appBody.Edited {
		t.Error("response.edited should be true — chunked body was decoded")
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	if msgs[0].To != "bob@example.com" {
		t.Errorf("SMTP To = %q, want bob@example.com (edited)", msgs[0].To)
	}
	if !strings.Contains(msgs[0].Data, "Edited via chunked") {
		t.Errorf("SMTP missing edited subject (chunked body was swallowed):\n%s", msgs[0].Data)
	}
	if !strings.Contains(msgs[0].Data, "chunked body") {
		t.Errorf("SMTP missing edited body:\n%s", msgs[0].Data)
	}
	if strings.Contains(msgs[0].Data, "Original subject") {
		t.Errorf("SMTP contains original subject — overrides were dropped:\n%s", msgs[0].Data)
	}
}

// Regression: same chunked-encoding bug on the reject path silently
// dropped the rejection reason. Assert the reason round-trips through
// a chunked POST.
func TestRejectPendingMessageWithChunkedReasonRecordsReason(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-rej-chunk@example.com", "Owner", "google-rej-chunk")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "rej-chunk-key", nil)
	store.ClaimOrCreateDomain(ctx, "rej-chunk.example.com", user.ID)
	store.VerifyDomain(ctx, "rej-chunk.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@rej-chunk.example.com", "rej-chunk.example.com", "", "https://example.com/webhook", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["alice@example.com"],"subject":"Bad draft","body":"…"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)

	rejResp := authedChunked(t, "POST",
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+sendBody.MessageID+"/reject",
		`{"reason":"off-topic for this audience"}`, apiKey.PlaintextKey)
	defer rejResp.Body.Close()
	if rejResp.StatusCode != http.StatusOK {
		t.Fatalf("reject via chunked: status = %d", rejResp.StatusCode)
	}

	// Verify the reason landed via the detail endpoint.
	detailResp := authed(t, "GET",
		server.URL+"/api/v1/messages/"+sendBody.MessageID, "", apiKey.PlaintextKey)
	defer detailResp.Body.Close()
	var detail struct {
		Status          string `json:"status"`
		RejectionReason string `json:"rejection_reason"`
	}
	json.NewDecoder(detailResp.Body).Decode(&detail)
	if detail.Status != "rejected" {
		t.Errorf("detail.status = %q, want rejected", detail.Status)
	}
	if detail.RejectionReason != "off-topic for this audience" {
		t.Errorf("rejection_reason = %q — chunked body was swallowed", detail.RejectionReason)
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
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+replyBody.MessageID+"/approve",
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
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+sendBody.MessageID+"/reject",
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
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+sendBody.MessageID+"/reject",
		`{"reason":"first"}`, apiKey.PlaintextKey)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first reject: status = %d", resp1.StatusCode)
	}

	resp2 := authed(t, "POST",
		server.URL+"/api/v1/agents/"+agent.Email+"/messages/"+sendBody.MessageID+"/reject",
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
		server.URL+"/api/v1/agents/"+agentA.Email+"/messages/"+sendBody.MessageID+"/approve",
		"", keyB.PlaintextKey)
	defer appResp.Body.Close()
	if appResp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user approve: status = %d, want 404", appResp.StatusCode)
	}
}

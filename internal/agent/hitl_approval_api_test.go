package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/outbound"
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

	// Seed two pending rows directly in the store.
	seeds := []struct {
		to      string
		subject string
	}{
		{"a@example.com", "First"},
		{"b@example.com", "Second"},
	}
	for _, s := range seeds {
		if _, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
			[]string{s.to}, nil, nil, s.subject, "b", "", nil,
			"send", "", "", 3600); err != nil {
			t.Fatalf("seed pending: %v", err)
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

	attachmentsJSON, err := json.Marshal([]outbound.Attachment{
		{Filename: "f.txt", ContentType: "text/plain", Data: "aGk="},
	})
	if err != nil {
		t.Fatalf("marshal attachments: %v", err)
	}
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil, "Hello", "plain body", "<p>html</p>",
		attachmentsJSON, "send", "", "", 3600)
	if err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+msg.ID, "", apiKey.PlaintextKey)
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
	store.CreateAPIKey(ctx, userA.ID, "a-key", nil)
	store.ClaimOrCreateDomain(ctx, "cross-a.example.com", userA.ID)
	store.VerifyDomain(ctx, "cross-a.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "bot@cross-a.example.com", "cross-a.example.com", "", "https://example.com/webhook", "", userA.ID)
	enableHITL(t, store, agentA.ID, userA.ID)

	// User B (no access to A's messages)
	userB, _ := store.CreateOrGetUser(ctx, "owner-cross-b@example.com", "B", "google-cross-b")
	keyB, _ := store.CreateAPIKey(ctx, userB.ID, "b-key", nil)

	msg, err := store.CreatePendingOutboundMessage(ctx, agentA.ID,
		[]string{"x@example.com"}, nil, nil, "h", "b", "", nil,
		"send", "", "", 3600)
	if err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	// User B tries to fetch User A's pending message
	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+msg.ID, "", keyB.PlaintextKey)
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
	if _, err := store.CreateInboundMessage(ctx, "", agent.ID,
		"alice@gmail.com", "bot@inbound-ctx.example.com",
		"<orig@gmail.com>", "Hello Bot", "", "",
		nil, authHeaders, nil, nil, nil); err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	// Seed a pending reply whose email_message_id points at our inbound.
	hold, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@gmail.com"}, nil, nil, "Re: Hello Bot", "Thanks!", "", nil,
		"reply", "", "<orig@gmail.com>", 3600)
	if err != nil {
		t.Fatalf("seed pending reply: %v", err)
	}

	// GET the detail. Inbound context should be present and populated.
	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+hold.ID, "", apiKey.PlaintextKey)
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

	hold, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil, "Cold outreach", "hello", "", nil,
		"send", "", "", 3600)
	if err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+hold.ID, "", apiKey.PlaintextKey)
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

	hold, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@gmail.com"}, nil, nil, "Re: Subject that will vanish", "reply", "", nil,
		"reply", "", "<missing@gmail.com>", 3600)
	if err != nil {
		t.Fatalf("seed pending reply: %v", err)
	}

	// Simulate the inbound aging out of retention by deleting the row
	// directly. The outbound's email_message_id still points at the
	// (now-gone) parent.
	if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, inbound.ID); err != nil {
		t.Fatalf("delete parent inbound: %v", err)
	}

	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+hold.ID, "", apiKey.PlaintextKey)
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

	if _, err := store.CreateInboundMessage(ctx, "", agentA.ID,
		"alice@gmail.com", "bot@scoped-a.example.com",
		"<shared-id@gmail.com>", "Subject for A", "", "",
		nil, map[string]string{"spf": "pass"}, nil, nil, nil); err != nil {
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

	// Reply on agent A using agent A's inbound (same shared Message-ID).
	hold, err := store.CreatePendingOutboundMessage(ctx, agentA.ID,
		[]string{"alice@gmail.com"}, nil, nil, "Re: Subject for A", "r", "", nil,
		"reply", "", "<shared-id@gmail.com>", 3600)
	if err != nil {
		t.Fatalf("seed pending reply: %v", err)
	}

	getResp := authed(t, "GET", server.URL+"/api/v1/messages/"+hold.ID, "", keyA.PlaintextKey)
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

package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// TestSelfSend_HappyPath: sending to one's own agent inbox short-
// circuits the SMTP path, returns method=loopback, and produces both
// an outbound (sent-history) and inbound (inbox) row.
func TestSelfSend_HappyPath(t *testing.T) {
	server, store, pool := setupAPI(t)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "self-owner@example.com", "Owner", "google-self-owner")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	apiKeyObj, err := store.CreateAPIKey(ctx, user.ID, "self-send-key")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "selfdomain.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, "selfdomain.example.com", user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	agentRow, err := store.CreateAgent(ctx, "bot@selfdomain.example.com", "selfdomain.example.com", "", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	payload := `{"to":["bot@selfdomain.example.com"],"subject":"note to self","body":"remember to refill coffee"}`
	resp := authedPost(t, server.URL+"/api/v1/send", payload, apiKeyObj.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d want 200; body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["method"] != "loopback" {
		t.Errorf("method=%q want loopback", body["method"])
	}
	if body["status"] != "sent" {
		t.Errorf("status=%q want sent", body["status"])
	}
	if !strings.HasPrefix(body["message_id"], "<") || !strings.Contains(body["message_id"], "loopback.") {
		t.Errorf("message_id=%q should look like an RFC 5322 Message-ID with loopback host", body["message_id"])
	}

	// Both rows should land in the messages table — outbound + inbound,
	// each tagged to the same agent.
	var outboundCount, inboundCount int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='outbound' AND subject='note to self'`,
		agentRow.ID).Scan(&outboundCount)
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='note to self'`,
		agentRow.ID).Scan(&inboundCount)
	if outboundCount != 1 {
		t.Errorf("outbound rows=%d want 1", outboundCount)
	}
	if inboundCount != 1 {
		t.Errorf("inbound rows=%d want 1", inboundCount)
	}

	// Inbound row's sender + recipient must both be the agent's address —
	// the inbox view should clearly show this as a self-note.
	var sender, recipient string
	pool.QueryRow(ctx,
		`SELECT sender, recipient FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='note to self'`,
		agentRow.ID).Scan(&sender, &recipient)
	if sender != "bot@selfdomain.example.com" || recipient != "bot@selfdomain.example.com" {
		t.Errorf("self-note row sender=%q recipient=%q; both must be the agent's own address", sender, recipient)
	}

	// Outbound row's method column persists "loopback" so operators can
	// distinguish self-sends from real SMTP traffic in audit queries.
	var method string
	pool.QueryRow(ctx,
		`SELECT method FROM messages WHERE agent_id=$1 AND direction='outbound' AND subject='note to self'`,
		agentRow.ID).Scan(&method)
	if method != "loopback" {
		t.Errorf("outbound method=%q want loopback", method)
	}
}

// TestSelfSend_PreservesAttachmentsInMIME: a self-send with an
// attachment must persist the attachment in the inbound row's
// raw_message so the SDK's MIME parser (used by client.getMessage)
// finds it on read. Before this fix the loopback path stored only
// req.Body and silently discarded req.Attachments — sender's
// outbound row recorded the send but the recipient inbox had no
// trace of the file.
//
// Also asserts the synthetic Received: trace header is present per
// RFC 5321 §4.4 — local-delivery MTAs (sendmail, Postfix, Exim) all
// stamp at least one Received line even for same-host delivery.
func TestSelfSend_PreservesAttachmentsInMIME(t *testing.T) {
	server, store, pool := setupAPI(t)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "self-attach@example.com", "Owner", "google-self-attach")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	apiKeyObj, err := store.CreateAPIKey(ctx, user.ID, "self-attach-key")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "selfattach.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, "selfattach.example.com", user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, "bot@selfattach.example.com", "selfattach.example.com", "", "", "local", user.ID); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// 'aGVsbG8gZmlsZQ==' is base64 of "hello file" — small enough to
	// keep the test fast, large enough to round-trip through the MIME
	// composer's base64 encoding without ambiguity.
	payload := `{
	  "to":["bot@selfattach.example.com"],
	  "subject":"note with file",
	  "body":"see attached",
	  "attachments":[{
	    "filename":"note.txt",
	    "content_type":"text/plain",
	    "data":"aGVsbG8gZmlsZQ=="
	  }]
	}`
	resp := authedPost(t, server.URL+"/api/v1/send", payload, apiKeyObj.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d want 200; body=%s", resp.StatusCode, readBody(t, resp))
	}
	var sendResp map[string]string
	json.NewDecoder(resp.Body).Decode(&sendResp)
	if sendResp["method"] != "loopback" {
		t.Errorf("method=%q want loopback", sendResp["method"])
	}

	// Fetch the inbound row's raw_message and verify the MIME shape.
	var rawBytes []byte
	if err := pool.QueryRow(ctx,
		`SELECT raw_message FROM messages
		   WHERE agent_id='bot@selfattach.example.com'
		     AND direction='inbound'
		     AND subject='note with file'`,
	).Scan(&rawBytes); err != nil {
		t.Fatalf("fetch inbound row: %v", err)
	}
	raw := string(rawBytes)

	// 1) Synthetic Received: trace header (RFC 5321 §4.4 conformance).
	if !strings.HasPrefix(raw, "Received: by ") {
		t.Errorf("inbound raw_message should start with synthetic Received: header; got:\n%.200s", raw)
	}
	if !strings.Contains(raw, "with loopback id ") {
		t.Errorf("Received: header should carry 'with loopback id' keyword for forensic grep; got:\n%.300s", raw)
	}

	// 2) Multipart MIME envelope with the attachment present.
	if !strings.Contains(raw, "Content-Type: multipart/") {
		t.Errorf("raw_message should be multipart MIME (attachments present); got:\n%.500s", raw)
	}
	if !strings.Contains(raw, `filename="note.txt"`) {
		t.Errorf("attachment filename header missing from MIME; got:\n%.800s", raw)
	}
	// The base64-encoded payload of the attachment ('hello file' →
	// 'aGVsbG8gZmlsZQ==') must appear in the multipart body. Composer
	// can re-wrap base64 at 76 chars (RFC 2045 §6.8) but for a 16-char
	// payload the wrap doesn't fire.
	if !strings.Contains(raw, "aGVsbG8gZmlsZQ==") {
		t.Errorf("attachment base64 payload missing from MIME body; got:\n%.800s", raw)
	}

	// 3) From: and To: headers match the self-loop identity.
	if !strings.Contains(raw, "From: bot@selfattach.example.com") {
		t.Errorf("From: header should be the agent's own address; got:\n%.300s", raw)
	}
	if !strings.Contains(raw, "To: bot@selfattach.example.com") {
		t.Errorf("To: header should be the agent's own address; got:\n%.300s", raw)
	}
}

// TestSelfSend_NoAttachmentsUsesSinglePart: when no attachments are
// included, the loopback path uses the simpler single-part composer
// (text/plain or text/html) rather than always emitting multipart.
// Keeps the stored MIME small for the dominant note-to-self case.
func TestSelfSend_NoAttachmentsUsesSinglePart(t *testing.T) {
	server, store, pool := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "self-plain@example.com", "Owner", "google-self-plain")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "self-plain-key")
	store.ClaimOrCreateDomain(ctx, "selfplain.example.com", user.ID)
	store.VerifyDomain(ctx, "selfplain.example.com", user.ID)
	store.CreateAgent(ctx, "bot@selfplain.example.com", "selfplain.example.com", "", "", "local", user.ID)

	payload := `{"to":["bot@selfplain.example.com"],"subject":"plain","body":"hi me"}`
	resp := authedPost(t, server.URL+"/api/v1/send", payload, apiKeyObj.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d; body=%s", resp.StatusCode, readBody(t, resp))
	}

	var rawBytes []byte
	pool.QueryRow(ctx,
		`SELECT raw_message FROM messages
		   WHERE agent_id='bot@selfplain.example.com'
		     AND direction='inbound'
		     AND subject='plain'`,
	).Scan(&rawBytes)
	raw := string(rawBytes)

	if !strings.HasPrefix(raw, "Received: by ") {
		t.Errorf("Received: header missing on plain self-send too; got:\n%.200s", raw)
	}
	// No multipart wrapper for the attachment-less path. The header
	// can still mention boundary= in other contexts so we check for
	// the canonical multipart Content-Type form.
	if strings.Contains(raw, "Content-Type: multipart/") {
		t.Errorf("plain self-send should NOT use multipart MIME; got:\n%.400s", raw)
	}
	if !strings.Contains(raw, "hi me") {
		t.Errorf("body text missing from raw_message; got:\n%.400s", raw)
	}
}

// TestSelfReply_LoopbackShortCircuit: replying to a self-sent message
// must work — the SMTP path would error because outbound.Sender
// strips the agent's own address from the recipient list (self-spam
// guard), leaving "no valid recipients" when the original sender
// IS the agent itself.
//
// The reply path should detect that the resolved reply destination
// is self and route through the same loopback short-circuit as
// send_email-to-self. Symmetric round-trip: send_email(self) →
// inbound row → reply_to_message → second inbound row.
func TestSelfReply_LoopbackShortCircuit(t *testing.T) {
	server, store, pool := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "self-reply@example.com", "Owner", "google-self-reply")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "self-reply-key")
	store.ClaimOrCreateDomain(ctx, "selfreply.example.com", user.ID)
	store.VerifyDomain(ctx, "selfreply.example.com", user.ID)
	store.CreateAgent(ctx, "bot@selfreply.example.com", "selfreply.example.com", "", "", "local", user.ID)
	agentEmail := "bot@selfreply.example.com"

	// Step 1: send a self-note. Establishes the inbound row we'll
	// reply to. We use the API rather than store.CreateInboundMessage
	// directly so the row carries the same MIME shape a real
	// self-send produces (i.e. has the Received: header + From:/To:
	// matching the agent, which ParseReplyRecipients consumes).
	payload := `{"to":["bot@selfreply.example.com"],"subject":"original","body":"the first message"}`
	resp := authedPost(t, server.URL+"/api/v1/send", payload, apiKeyObj.PlaintextKey)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("initial self-send failed: status=%d", resp.StatusCode)
	}

	// Look up the inbound msg_id for the self-note.
	var msgID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM messages
		   WHERE agent_id=$1 AND direction='inbound' AND subject='original'
		   ORDER BY created_at DESC LIMIT 1`,
		agentEmail).Scan(&msgID); err != nil {
		t.Fatalf("lookup inbound id: %v", err)
	}

	// Step 2: reply. This is the call that errored before the fix
	// (no valid recipients after the self-strip in outbound.Sender).
	replyURL := server.URL + "/api/v1/agents/" + agentEmail + "/messages/" + msgID + "/reply"
	replyResp := authedPost(t, replyURL,
		`{"body":"replying to my own note"}`,
		apiKeyObj.PlaintextKey)
	defer replyResp.Body.Close()
	if replyResp.StatusCode != 200 {
		t.Fatalf("self-reply status=%d want 200; body=%s",
			replyResp.StatusCode, readBody(t, replyResp))
	}
	var body map[string]string
	json.NewDecoder(replyResp.Body).Decode(&body)
	if body["method"] != "loopback" {
		t.Errorf("self-reply method=%q want loopback", body["method"])
	}
	if body["status"] != "sent" {
		t.Errorf("self-reply status=%q want sent", body["status"])
	}

	// Step 3: the inbox now holds both messages — the original AND
	// the reply. Both as direction=inbound rows.
	var inboundCount int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound'`,
		agentEmail).Scan(&inboundCount)
	if inboundCount != 2 {
		t.Errorf("inbound rows after self-reply = %d, want 2 (original + reply)", inboundCount)
	}

	// The reply must carry an Re:-prefixed subject and the agent as
	// both sender + recipient.
	var replyMsgSubject, replyMsgSender, replyMsgRecipient string
	pool.QueryRow(ctx,
		`SELECT subject, sender, recipient FROM messages
		   WHERE agent_id=$1 AND direction='inbound' AND subject LIKE 'Re:%'
		   ORDER BY created_at DESC LIMIT 1`,
		agentEmail).Scan(&replyMsgSubject, &replyMsgSender, &replyMsgRecipient)
	if replyMsgSubject != "Re: original" {
		t.Errorf("reply subject=%q want 'Re: original'", replyMsgSubject)
	}
	if replyMsgSender != agentEmail || replyMsgRecipient != agentEmail {
		t.Errorf("reply self-loop sender=%q recipient=%q, both should be %q",
			replyMsgSender, replyMsgRecipient, agentEmail)
	}
}

// TestSelfReply_ReplyAllOnSelfThread_LoopbackShortCircuit: the
// reply-to-self fix in 51ef9c8 only covered the empty-CC case. When
// reply_all=true on a self-thread, ParseReplyRecipients carries the
// original message's CC list forward verbatim — and on a self-loop
// that list includes the agent's own address. Without pre-stripping
// the agent's aliases from CC before checking isSelfSend, the
// predicate sees `len(CC) != 0`, returns false, and the call falls
// through to the SMTP path, where outbound.Sender's own alias-
// strip leaves the recipient list empty and the request errors out
// with HTTP 400 "no valid recipients."
//
// This test sets up that exact shape (original self-send had self
// in CC; reply with reply_all=true) and asserts the loopback path
// fires. Pre-fix this returned 400; post-fix it returns 200 with
// method=loopback.
func TestSelfReply_ReplyAllOnSelfThread_LoopbackShortCircuit(t *testing.T) {
	server, store, pool := setupAPI(t)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "self-replyall@example.com", "Owner", "google-self-replyall")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	apiKeyObj, err := store.CreateAPIKey(ctx, user.ID, "self-replyall-key")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "selfreplyall.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, "selfreplyall.example.com", user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, "bot@selfreplyall.example.com", "selfreplyall.example.com", "", "", "local", user.ID); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	agentEmail := "bot@selfreplyall.example.com"

	// Step 1: self-send WITH self in CC. The handleSendEmail isSelfSend
	// check rejects mixed CC, so this routes through the SMTP path...
	// but we want the inbound row to be populated for the reply. The
	// simplest way: insert the inbound row directly with the exact
	// MIME shape `composeLoopbackMIME` would produce — including a
	// To: header for the agent and a Cc: header that ALSO references
	// the agent. Then the reply's ParseReplyRecipients pulls that
	// CC entry forward.
	rawMIME := "From: " + agentEmail + "\r\n" +
		"To: " + agentEmail + "\r\n" +
		"Cc: " + agentEmail + "\r\n" +
		"Subject: original\r\n" +
		"Message-ID: <orig-replyall-test@selfreplyall.example.com>\r\n" +
		"\r\n" +
		"hello me"
	if _, err := store.CreateInboundMessage(
		ctx, "msg_replyall_seed", agentEmail, agentEmail, agentEmail,
		"<orig-replyall-test@selfreplyall.example.com>", "original", "", "unread",
		[]byte(rawMIME), nil,
		[]string{agentEmail}, []string{agentEmail}, nil,
	); err != nil {
		t.Fatalf("CreateInboundMessage seed: %v", err)
	}

	// Step 2: reply with reply_all=true. Pre-fix this errored with
	// 400 "no valid recipients". Post-fix it must succeed and route
	// through the loopback short-circuit (method=loopback).
	replyURL := server.URL + "/api/v1/agents/" + agentEmail + "/messages/msg_replyall_seed/reply"
	replyResp := authedPost(t, replyURL,
		`{"body":"replying with reply_all","reply_all":true}`,
		apiKeyObj.PlaintextKey)
	defer replyResp.Body.Close()
	if replyResp.StatusCode != 200 {
		t.Fatalf("self-reply (replyAll=true) status=%d want 200; body=%s",
			replyResp.StatusCode, readBody(t, replyResp))
	}
	var body map[string]string
	json.NewDecoder(replyResp.Body).Decode(&body)
	if body["method"] != "loopback" {
		t.Errorf("method=%q want loopback (replyAll on self-thread must take the short-circuit, not fall through to SMTP)", body["method"])
	}

	// The reply must persist as both outbound + inbound, same as a
	// regular self-reply. Spot-check the inbound row exists.
	var inboundRepliesCount int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages
		   WHERE agent_id=$1 AND direction='inbound' AND subject LIKE 'Re:%'`,
		agentEmail).Scan(&inboundRepliesCount)
	if inboundRepliesCount != 1 {
		t.Errorf("inbound replies = %d, want 1 (the loopback-routed reply)", inboundRepliesCount)
	}
}

// TestSelfSend_HoldsForHITL: an agent with HITL enabled holds a
// self-send for approval just like any other send. The "is the
// recipient external" question is independent of "did a human
// review this outbound message". The approval-finalize path then
// delivers via loopback (see TestSelfSend_HITLApprovalDeliversViaLoopback).
func TestSelfSend_HoldsForHITL(t *testing.T) {
	server, store, pool := setupAPI(t)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "hitl-self@example.com", "Owner", "google-hitl-self")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	apiKeyObj, err := store.CreateAPIKey(ctx, user.ID, "hitl-self-key")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "hitlself.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, "hitlself.example.com", user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	agentRow, err := store.CreateAgent(ctx, "bot@hitlself.example.com", "hitlself.example.com", "", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Flip HITL on. UpdateAgentSettings is the dashboard-side write;
	// using direct SQL keeps the test focused on the send path.
	if _, err := pool.Exec(ctx,
		`UPDATE agent_identities SET hitl_enabled=true WHERE id=$1`,
		agentRow.ID); err != nil {
		t.Fatal(err)
	}

	payload := `{"to":["bot@hitlself.example.com"],"subject":"hitl self","body":"this should be held for approval"}`
	resp := authedPost(t, server.URL+"/api/v1/send", payload, apiKeyObj.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status=%d want 202 (HITL must hold the self-send); body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "pending_approval" {
		t.Errorf("status=%q want pending_approval", body["status"])
	}

	// And the held row should exist as pending_approval (no inbound
	// row yet — that's written by the approval finalize step).
	var pending int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND status='pending_approval'`,
		agentRow.ID).Scan(&pending)
	if pending != 1 {
		t.Errorf("pending rows=%d want 1", pending)
	}
	var inbound int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound'`,
		agentRow.ID).Scan(&inbound)
	if inbound != 0 {
		t.Errorf("inbound rows=%d want 0 (loopback delivery should wait for approval)", inbound)
	}
}

// TestSelfSend_HITLApprovalDeliversViaLoopback: when a self-send is
// held for HITL approval and then approved, the finalize step delivers
// via the loopback path — outbound row flips to status=sent with
// method=loopback, and an inbound row is created in the agent's inbox.
// Before this fix the finalize path called outbound.Sender.Send, which
// strips the agent's own address from the recipient list and errors
// with "no valid recipients".
func TestSelfSend_HITLApprovalDeliversViaLoopback(t *testing.T) {
	server, store, pool := setupAPI(t)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "hitl-self-approve@example.com", "Owner", "google-hitl-self-approve")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	apiKeyObj, err := store.CreateAPIKey(ctx, user.ID, "hitl-self-approve-key")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "hitlselfapprove.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, "hitlselfapprove.example.com", user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	agentRow, err := store.CreateAgent(ctx, "bot@hitlselfapprove.example.com", "hitlselfapprove.example.com", "", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE agent_identities SET hitl_enabled=true WHERE id=$1`,
		agentRow.ID); err != nil {
		t.Fatal(err)
	}

	// Step 1: send → held for approval
	payload := `{"to":["bot@hitlselfapprove.example.com"],"subject":"awaiting review","body":"please approve me"}`
	resp := authedPost(t, server.URL+"/api/v1/send", payload, apiKeyObj.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("hold status=%d want 202; body=%s", resp.StatusCode, readBody(t, resp))
	}
	var holdBody map[string]string
	json.NewDecoder(resp.Body).Decode(&holdBody)
	heldMessageID := holdBody["message_id"]
	if heldMessageID == "" {
		t.Fatal("hold response missing message_id")
	}

	// Step 2: approve via the dashboard endpoint (empty body = approve as-is)
	approveURL := server.URL + "/api/v1/messages/" + heldMessageID + "/approve"
	approveResp := authedPost(t, approveURL, `{}`, apiKeyObj.PlaintextKey)
	defer approveResp.Body.Close()
	if approveResp.StatusCode != 200 {
		t.Fatalf("approve status=%d want 200; body=%s", approveResp.StatusCode, readBody(t, approveResp))
	}
	var approveBody map[string]interface{}
	json.NewDecoder(approveResp.Body).Decode(&approveBody)
	if approveBody["method"] != "loopback" {
		t.Errorf("approve method=%v want loopback", approveBody["method"])
	}

	// Step 3: outbound row is now sent + loopback, and the inbound
	// row exists with the same subject.
	var sentStatus, sentMethod string
	pool.QueryRow(ctx,
		`SELECT status, method FROM messages WHERE id=$1`,
		heldMessageID).Scan(&sentStatus, &sentMethod)
	if sentStatus != "sent" {
		t.Errorf("outbound status=%q want sent", sentStatus)
	}
	if sentMethod != "loopback" {
		t.Errorf("outbound method=%q want loopback", sentMethod)
	}

	var inboundCount int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='awaiting review'`,
		agentRow.ID).Scan(&inboundCount)
	if inboundCount != 1 {
		t.Errorf("inbound rows=%d want 1 (loopback delivery should write the recipient-side row)", inboundCount)
	}
}

// TestSelfSend_RequiresVerifiedDomain: the domain-verification gate
// fires before isSelfSend dispatch. Loopback is not a backdoor for
// unverified agents to use email-shaped storage.
func TestSelfSend_RequiresVerifiedDomain(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "self-unverified@example.com", "Owner", "google-self-unv")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "self-unv-key")
	store.ClaimOrCreateDomain(ctx, "selfunv.example.com", user.ID)
	// no VerifyDomain
	store.CreateAgent(ctx, "bot@selfunv.example.com", "selfunv.example.com", "", "", "local", user.ID)

	payload := `{"to":["bot@selfunv.example.com"],"subject":"x","body":"y"}`
	resp := authedPost(t, server.URL+"/api/v1/send", payload, apiKeyObj.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("status=%d want 403", resp.StatusCode)
	}
}

// TestSelfSend_DetectionEdgeCases: case-insensitive, whitespace-
// trimmed, single-address requirement. Mixed/external recipients must
// fall through to SMTP (covered indirectly — TestSendEmailViaSMTP
// already exercises the non-loopback path).
func TestSelfSend_DetectionEdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		to     []string
		cc     []string
		want   bool
		reason string
	}{
		{"exact match", []string{"bot@x.com"}, nil, true, ""},
		{"case-insensitive local", []string{"BOT@x.com"}, nil, true, "ASCII case-insensitive"},
		{"case-insensitive domain", []string{"bot@X.COM"}, nil, true, "domain comparison is case-insensitive"},
		{"whitespace trimmed", []string{"  bot@x.com  "}, nil, true, "trim should normalize"},
		{"different address", []string{"other@x.com"}, nil, false, "not self"},
		{"self plus external in To", []string{"bot@x.com", "other@x.com"}, nil, false, "external recipient → SMTP"},
		{"self plus cc", []string{"bot@x.com"}, []string{"cc@x.com"}, false, "cc → SMTP"},
		{"empty to", []string{}, nil, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := outbound.SendRequest{To: c.to, CC: c.cc}
			got := agent.IsSelfSendForTest(req, "bot@x.com")
			if got != c.want {
				t.Errorf("isSelfSend(%v, cc=%v) = %v, want %v (%s)", c.to, c.cc, got, c.want, c.reason)
			}
		})
	}
}


package agent_test

import (
	"context"
	"encoding/json"
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

// TestSelfSend_BypassesHITL: an agent with HITL enabled still self-
// sends immediately. Holding a note-to-self for the agent to approve
// to itself is degenerate UX; the loopback path explicitly skips the
// approval queue.
func TestSelfSend_BypassesHITL(t *testing.T) {
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

	payload := `{"to":["bot@hitlself.example.com"],"subject":"hitl self","body":"this should still ship immediately"}`
	resp := authedPost(t, server.URL+"/api/v1/send", payload, apiKeyObj.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d want 200 (HITL must be bypassed for self-send); body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "sent" {
		t.Errorf("status=%q want sent (not pending_approval)", body["status"])
	}

	// And no pending-approval row should exist.
	var pending int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND status='pending_approval'`,
		agentRow.ID).Scan(&pending)
	if pending != 0 {
		t.Errorf("pending rows=%d want 0 — HITL was supposed to be bypassed", pending)
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


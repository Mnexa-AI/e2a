//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// sendURL builds the /v1 send endpoint for an agent. Send was relocated
// (Slice 2): POST /v1/agents/{URL-encoded agent email}/messages, with the
// sender taken from the path (the body no longer carries `from`).
func sendURL(base, agentEmail string) string {
	return base + "/v1/agents/" + url.PathEscape(agentEmail) + "/messages"
}

// Tests in this file exercise the webhooks-as-a-resource path end-to-end.
// Distinct from e2e_test.go which covers the legacy
// agent_identities.webhook_url path (per-agent, single URL, no filters).
//
// Coverage map (from the approved test plan):
//   P1 — outbound events: email.sent, email.pending_approval,
//         email.approved, email.rejected fire from real handler triggers
//   P2 — inbound:         email.received fires from a real SMTP message
//   P3 — filters:         agent_ids + conversation_ids + labels (H5)
//   P4 — signing:         rotation grace dual-sig + disabled webhook silent
//   P5 — auto-disable:    10 failed events flip enabled=false
//
// Two design decisions worth re-stating:
//   1) Webhooks are provisioned via Store.CreateWebhook directly (NOT
//      POST /v1/webhooks). The public API rejects 127.0.0.1 URLs
//      (SSRF guard); the only way to test the worker hitting a local
//      receiver is to bypass the handler-side validator. The handler
//      itself has unit-test coverage at internal/agent/webhooks_api_test.go.
//   2) Worker timing: we call SubscriberWorker.Tick(ctx) directly instead
//      of waiting on the 30s production interval. That keeps the suite
//      deterministic and under 60s.

// authedJSON wraps an authenticated JSON request and returns the
// response status + body. Tests assert against both.
func authedJSON(t *testing.T, method, url, apiKey, body string) (int, []byte) {
	t.Helper()
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = bytes.NewBuffer(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// verifyHMACv1 returns true iff at least one v1= entry in the
// X-E2A-Signature header verifies against secret using the standard
// "<t>.<rawBody>" payload. Mirrors the SDK helpers' behavior so a
// test failure points at the wire protocol, not the verifier.
func verifyHMACv1(t *testing.T, headers http.Header, rawBody []byte, secret string) bool {
	t.Helper()
	sig := headers.Get("X-E2A-Signature")
	if sig == "" {
		return false
	}
	var ts string
	var v1s []string
	for _, part := range strings.Split(sig, ",") {
		p := strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(p, "t="):
			ts = strings.TrimPrefix(p, "t=")
		case strings.HasPrefix(p, "v1="):
			v1s = append(v1s, strings.TrimPrefix(p, "v1="))
		}
	}
	if ts == "" || len(v1s) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(rawBody)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, v := range v1s {
		if v == want {
			return true
		}
	}
	return false
}

// setupSubscriberOwner provisions a user + API key + a verified domain
// + an agent on that domain. Returns the agent so the test can
// register webhooks scoped to it. mode is "local" or "cloud"; HITL
// flag toggles HITL on the agent.
func setupSubscriberOwner(t *testing.T, ts *testutil.E2ATestServer, prefix string, hitl bool) (*identity.User, *identity.APIKey, *identity.AgentIdentity) {
	t.Helper()
	ctx := context.Background()
	user, err := ts.Store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "google-"+prefix)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	key, err := ts.Store.CreateAPIKey(ctx, user.ID, prefix+"-key", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	domain := prefix + ".example.com"
	if _, err := ts.Store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := ts.Store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	email := "bot@" + domain
	agent, err := ts.Store.CreateAgent(ctx, email, domain, "Bot", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if hitl {
		// Enable HITL with a reasonable TTL.
		if err := ts.Store.UpdateAgentHITL(ctx, agent.ID, user.ID, true, 3600, "reject"); err != nil {
			t.Fatalf("UpdateAgentHITL: %v", err)
		}
		// Reload so the in-test agent struct reflects HITL flag.
		agent, err = ts.Store.GetAgentByID(ctx, agent.ID)
		if err != nil {
			t.Fatalf("GetAgentByID: %v", err)
		}
	}
	return user, key, agent
}

// registerWebhook is the cheap path around the public-API URL
// validator. The validator rejects 127.0.0.1 (SSRF guard); the
// storage layer accepts any URL. The handler's validation has its
// own unit test coverage.
func registerWebhook(t *testing.T, ts *testutil.E2ATestServer, userID, url string, events []string, filters identity.WebhookFilters) *identity.Webhook {
	t.Helper()
	wh, err := ts.Store.CreateWebhook(context.Background(), userID, url, "e2e", events, filters)
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	return wh
}

// tick drains the subscriber worker once. Tests call this between
// trigger and assertion so they don't wait on the 30s production tick.
func tick(t *testing.T, ts *testutil.E2ATestServer) {
	t.Helper()
	// publishAsync uses a goroutine — give it a beat to land the
	// delivery row before we drain.
	time.Sleep(50 * time.Millisecond)
	ts.SubscriberWorker.Tick(context.Background())
}

// ----------------------------------------------------------------------
// P1 — Outbound events
// ----------------------------------------------------------------------

func TestWebhooksE2E_EmailSent(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	_, key, agent := setupSubscriberOwner(t, ts, "wh-sent", false)
	wh := registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/sent",
		[]string{"email.sent"}, identity.WebhookFilters{})

	// Trigger /send through the real HTTP API so publishAsync fires
	// from the actual handler, not a hand-crafted publisher call.
	body := `{"to":["alice@example.com"],"subject":"hi","body":"hello"}`
	status, _ := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey, body)
	if status != 200 {
		t.Fatalf("send status=%d", status)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 2*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 {
		t.Fatalf("got %d captures, want 1", len(got))
	}
	c := got[0]
	if c.URL != "/sent" {
		t.Errorf("path=%q want /sent", c.URL)
	}
	if c.Envelope["type"] != "email.sent" {
		t.Errorf("event=%v want email.sent", c.Envelope["type"])
	}
	if !verifyHMACv1(t, c.Headers, c.RawBody, wh.SigningSecret) {
		t.Errorf("HMAC v1 verification failed: sig=%q", c.Headers.Get("X-E2A-Signature"))
	}
}

func TestWebhooksE2E_HITL_PendingApproved(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	_, key, agent := setupSubscriberOwner(t, ts, "wh-hitl", true)
	pendingHook := registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/pending",
		[]string{"email.pending_approval"}, identity.WebhookFilters{})
	approvedHook := registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/approved",
		[]string{"email.approved"}, identity.WebhookFilters{})

	// send → held for approval, returns 202 + message_id.
	body := `{"to":["alice@example.com"],"subject":"draft","body":"please review"}`
	status, respBytes := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey, body)
	if status != 202 {
		t.Fatalf("send status=%d body=%s want 202 (HITL hold)", status, string(respBytes))
	}
	var sendResp map[string]any
	json.Unmarshal(respBytes, &sendResp)
	msgID, _ := sendResp["message_id"].(string)
	if msgID == "" {
		t.Fatalf("no message_id in hold response: %s", string(respBytes))
	}

	// First tick — should deliver email.pending_approval.
	tick(t, ts)
	receiver.WaitFor(t, 2*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	got := receiver.Captured()
	if len(got) != 1 {
		t.Fatalf("after pending: got %d captures, want 1", len(got))
	}
	if got[0].URL != "/pending" || got[0].Envelope["type"] != "email.pending_approval" {
		t.Errorf("first capture path=%q event=%v", got[0].URL, got[0].Envelope["type"])
	}
	if !verifyHMACv1(t, got[0].Headers, got[0].RawBody, pendingHook.SigningSecret) {
		t.Errorf("pending HMAC verification failed")
	}

	// Approve via the API.
	approveBody := `{}`
	approveStatus, approveResp := authedJSON(t, "POST",
		ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape(agent.EmailAddress())+"/messages/"+msgID+"/approve",
		key.PlaintextKey, approveBody)
	if approveStatus != 200 {
		t.Fatalf("approve status=%d body=%s", approveStatus, string(approveResp))
	}

	// Second tick — should deliver email.approved.
	tick(t, ts)
	receiver.WaitFor(t, 2*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 2 })
	got = receiver.Captured()
	if len(got) != 2 {
		t.Fatalf("after approve: got %d captures, want 2", len(got))
	}
	approved := got[1]
	if approved.URL != "/approved" || approved.Envelope["type"] != "email.approved" {
		t.Errorf("second capture path=%q event=%v", approved.URL, approved.Envelope["type"])
	}
	if !verifyHMACv1(t, approved.Headers, approved.RawBody, approvedHook.SigningSecret) {
		t.Errorf("approved HMAC verification failed")
	}
	// data.reviewed_by_user_id is set on approve (build path puts the
	// caller's user id there). Verify the shape, not the value — the
	// per-test user id is opaque.
	data, _ := approved.Envelope["data"].(map[string]any)
	if data["reviewed_by_user_id"] == "" {
		t.Errorf("approved event missing reviewed_by_user_id: %v", data)
	}
}

func TestWebhooksE2E_HITL_Rejected(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	_, key, agent := setupSubscriberOwner(t, ts, "wh-reject", true)
	rejectedHook := registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/rejected",
		[]string{"email.rejected"}, identity.WebhookFilters{})

	body := `{"to":["alice@example.com"],"subject":"nope","body":"please reject"}`
	status, respBytes := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey, body)
	if status != 202 {
		t.Fatalf("send status=%d", status)
	}
	var sendResp map[string]any
	json.Unmarshal(respBytes, &sendResp)
	msgID, _ := sendResp["message_id"].(string)

	// Drain pending_approval (which we don't care about here) before
	// rejecting, so the post-reject Captured() is the rejected event
	// alone.
	tick(t, ts)
	receiver.Reset()

	rejectStatus, rejectResp := authedJSON(t, "POST",
		ts.HTTPServer.URL+"/v1/agents/"+url.PathEscape(agent.EmailAddress())+"/messages/"+msgID+"/reject",
		key.PlaintextKey, `{"reason":"off-policy"}`)
	if rejectStatus != 200 {
		t.Fatalf("reject status=%d body=%s", rejectStatus, string(rejectResp))
	}

	tick(t, ts)
	receiver.WaitFor(t, 2*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	got := receiver.Captured()
	if len(got) != 1 {
		t.Fatalf("after reject: got %d captures, want 1", len(got))
	}
	if got[0].URL != "/rejected" || got[0].Envelope["type"] != "email.rejected" {
		t.Errorf("path=%q event=%v", got[0].URL, got[0].Envelope["type"])
	}
	if !verifyHMACv1(t, got[0].Headers, got[0].RawBody, rejectedHook.SigningSecret) {
		t.Errorf("rejected HMAC verification failed")
	}
	data, _ := got[0].Envelope["data"].(map[string]any)
	if data["rejection_reason"] != "off-policy" {
		t.Errorf("rejection_reason=%v want off-policy", data["rejection_reason"])
	}
}

// ----------------------------------------------------------------------
// P2 — Inbound (email.received via SMTP)
// ----------------------------------------------------------------------

func TestWebhooksE2E_EmailReceived(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	_, _, agent := setupSubscriberOwner(t, ts, "wh-recv", false)
	wh := registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/received",
		[]string{"email.received"}, identity.WebhookFilters{})

	// SMTP a message into the relay. The relay marks it unverified
	// in dev (no SPF/DKIM for example.com) but still persists +
	// publishes — the event fires regardless of verification status.
	msg := "From: alice@gmail.com\r\nTo: " + agent.EmailAddress() + "\r\nSubject: Hi\r\n\r\nhello from SMTP"
	if err := smtp.SendMail(ts.SMTPAddr, nil, "alice@gmail.com",
		[]string{agent.EmailAddress()}, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 3*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 {
		t.Fatalf("got %d captures, want 1", len(got))
	}
	if got[0].URL != "/received" || got[0].Envelope["type"] != "email.received" {
		t.Errorf("path=%q event=%v", got[0].URL, got[0].Envelope["type"])
	}
	if !verifyHMACv1(t, got[0].Headers, got[0].RawBody, wh.SigningSecret) {
		t.Errorf("received HMAC verification failed")
	}
	data, _ := got[0].Envelope["data"].(map[string]any)
	if data["message_id"] == "" || !strings.HasPrefix(fmt.Sprint(data["message_id"]), "msg_") {
		t.Errorf("expected msg_ id in data.message_id, got %v", data["message_id"])
	}
}

// ----------------------------------------------------------------------
// P3 — Filter matching
// ----------------------------------------------------------------------

func TestWebhooksE2E_FilterByAgentID(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	// One owner, two agents (different mailboxes on different domains
	// so a verified-domain check passes for both).
	_, keyA, agentA := setupSubscriberOwner(t, ts, "wh-filta", false)
	domainB := "wh-filtb.example.com"
	if _, err := ts.Store.ClaimOrCreateDomain(context.Background(), domainB, agentA.UserID); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	if err := ts.Store.VerifyDomain(context.Background(), domainB, agentA.UserID); err != nil {
		t.Fatalf("verify b: %v", err)
	}
	agentB, err := ts.Store.CreateAgent(context.Background(),
		"bot@"+domainB, domainB, "Bot B", "", "local", agentA.UserID)
	if err != nil {
		t.Fatalf("create agent b: %v", err)
	}

	// One webhook per agent, filtered to that agent's id.
	whA := registerWebhook(t, ts, agentA.UserID, receiver.Server.URL+"/a",
		[]string{"email.sent"}, identity.WebhookFilters{AgentIDs: []string{agentA.ID}})
	whB := registerWebhook(t, ts, agentA.UserID, receiver.Server.URL+"/b",
		[]string{"email.sent"}, identity.WebhookFilters{AgentIDs: []string{agentB.ID}})

	// Send through agent A → only /a fires.
	body := `{"to":["alice@example.com"],"subject":"from A","body":"x"}`
	status, _ := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agentA.EmailAddress()), keyA.PlaintextKey, body)
	if status != 200 {
		t.Fatalf("send-A status=%d", status)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 2*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 || got[0].URL != "/a" {
		t.Fatalf("expected exactly /a; got %d captures: %v", len(got), summarize(got))
	}
	if !verifyHMACv1(t, got[0].Headers, got[0].RawBody, whA.SigningSecret) {
		t.Errorf("A HMAC failed")
	}

	// Send through agent B → only /b fires.
	receiver.Reset()
	bodyB := `{"to":["bob@example.com"],"subject":"from B","body":"x"}`
	status, _ = authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agentB.EmailAddress()), keyA.PlaintextKey, bodyB)
	if status != 200 {
		t.Fatalf("send-B status=%d", status)
	}

	tick(t, ts)
	got = receiver.WaitFor(t, 2*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 || got[0].URL != "/b" {
		t.Fatalf("expected exactly /b; got %d captures: %v", len(got), summarize(got))
	}
	if !verifyHMACv1(t, got[0].Headers, got[0].RawBody, whB.SigningSecret) {
		t.Errorf("B HMAC failed")
	}
}

func TestWebhooksE2E_FilterByConversationID(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	_, key, agent := setupSubscriberOwner(t, ts, "wh-conv", false)
	registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/match",
		[]string{"email.sent"}, identity.WebhookFilters{ConversationIDs: []string{"conv-match"}})

	// Send WITHOUT conversation_id — should NOT match (H5 rule:
	// filter requires X but event has none).
	body := `{"to":["alice@example.com"],"subject":"no conv","body":"x"}`
	status, _ := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey, body)
	if status != 200 {
		t.Fatalf("send-noconv status=%d", status)
	}
	tick(t, ts)
	// Use a small explicit wait then assert no posts: short timeout
	// to avoid a flaky pass if the worker is slow.
	receiver.WaitFor(t, 500*time.Millisecond, func(c []testutil.SubscriberCaptured) bool { return false })
	if got := receiver.Captured(); len(got) != 0 {
		t.Errorf("filter required conversation_id but event had none → should skip; got %v", summarize(got))
	}

	// Send WITH the matching conversation_id → fires.
	body = `{"to":["alice@example.com"],"subject":"yes conv","body":"x","conversation_id":"conv-match"}`
	status, _ = authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey, body)
	if status != 200 {
		t.Fatalf("send-conv status=%d", status)
	}
	tick(t, ts)
	got := receiver.WaitFor(t, 2*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 || got[0].URL != "/match" {
		t.Fatalf("expected /match; got %v", summarize(got))
	}
}

// TestWebhooksE2E_FilterByLabelsH5 documents H5: when a filter
// requires a value of type T but the event has no T (here, an
// email.sent event whose envelope carries no labels), the subscriber
// MUST be skipped. Without this rule, an "urgent-only" subscriber
// would receive every unlabelled message — the design explicitly
// rejects that as a foot-gun.
func TestWebhooksE2E_FilterByLabelsH5(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	_, key, agent := setupSubscriberOwner(t, ts, "wh-label", false)
	registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/labelled",
		[]string{"email.sent"}, identity.WebhookFilters{Labels: []string{"urgent"}})

	body := `{"to":["alice@example.com"],"subject":"unlabelled","body":"x"}`
	if status, _ := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey, body); status != 200 {
		t.Fatalf("send status=%d", status)
	}
	tick(t, ts)
	receiver.WaitFor(t, 500*time.Millisecond, func(c []testutil.SubscriberCaptured) bool { return false })
	if got := receiver.Captured(); len(got) != 0 {
		t.Errorf("H5 violation: filter required labels but event had none → should skip; got %v", summarize(got))
	}
}

// ----------------------------------------------------------------------
// P4 — Signing edge cases (rotation grace + disabled webhook silent)
// ----------------------------------------------------------------------

func TestWebhooksE2E_RotationGrace_DualSig(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	_, key, agent := setupSubscriberOwner(t, ts, "wh-rot", false)
	wh := registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/rot",
		[]string{"email.sent"}, identity.WebhookFilters{})
	oldSecret := wh.SigningSecret

	// Rotate. After this, the worker should dual-sign every delivery
	// for the 24h grace window.
	newSecret, _, err := ts.Store.RotateSecret(context.Background(), wh.ID, agent.UserID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newSecret == oldSecret {
		t.Fatalf("rotation returned same secret")
	}

	body := `{"to":["alice@example.com"],"subject":"rotated","body":"x"}`
	if status, _ := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey, body); status != 200 {
		t.Fatalf("send status=%d", status)
	}
	tick(t, ts)
	got := receiver.WaitFor(t, 2*time.Second, func(c []testutil.SubscriberCaptured) bool { return len(c) >= 1 })
	if len(got) != 1 {
		t.Fatalf("got %d captures, want 1", len(got))
	}

	// Parse signature; expect exactly 2 v1= entries.
	sig := got[0].Headers.Get("X-E2A-Signature")
	v1count := strings.Count(sig, "v1=")
	if v1count != 2 {
		t.Errorf("expected 2 v1= entries during rotation grace, got %d (sig=%q)", v1count, sig)
	}
	// Both must verify — caller migrating from old → new must still
	// pass during the grace window.
	if !verifyHMACv1(t, got[0].Headers, got[0].RawBody, oldSecret) {
		t.Errorf("old secret should still verify during grace")
	}
	if !verifyHMACv1(t, got[0].Headers, got[0].RawBody, newSecret) {
		t.Errorf("new secret should verify")
	}
}

func TestWebhooksE2E_DisabledWebhookNoFire(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)

	_, key, agent := setupSubscriberOwner(t, ts, "wh-disab", false)
	wh := registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/dis",
		[]string{"email.sent"}, identity.WebhookFilters{})

	// Disable via storage (the public API does it via PATCH, which is
	// exercised in the handler tests).
	enabled := false
	if _, err := ts.Store.UpdateWebhook(context.Background(), wh.ID, agent.UserID,
		identity.WebhookUpdate{Enabled: &enabled}); err != nil {
		t.Fatalf("UpdateWebhook disable: %v", err)
	}

	body := `{"to":["alice@example.com"],"subject":"x","body":"x"}`
	if status, _ := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey, body); status != 200 {
		t.Fatalf("send status=%d", status)
	}
	tick(t, ts)
	// Give the worker + publisher a beat; assert nothing arrived.
	receiver.WaitFor(t, 500*time.Millisecond, func(c []testutil.SubscriberCaptured) bool { return false })
	if got := receiver.Captured(); len(got) != 0 {
		t.Errorf("disabled webhook should receive no events; got %v", summarize(got))
	}
}

// ----------------------------------------------------------------------
// P5 — Auto-disable
// ----------------------------------------------------------------------

func TestWebhooksE2E_AutoDisableAfterFailures(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	receiver := testutil.SubscriberReceiver(t)
	receiver.SetStatus("/dead", 503)

	_, _, agent := setupSubscriberOwner(t, ts, "wh-autodis", false)
	wh := registerWebhook(t, ts, agent.UserID, receiver.Server.URL+"/dead",
		[]string{"email.sent"}, identity.WebhookFilters{})

	// Seed 10 failed delivery rows directly. Going through real
	// /send and exhausting retries would take longer than the test
	// budget (5 attempts × backoff). The auto-disable janitor only
	// reads the rows' final status; seeding them keeps the test
	// deterministic.
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO webhook_subscriber_deliveries
			    (id, webhook_id, event_type, event_payload, status, attempts, max_attempts, last_error, last_status_code)
			 VALUES ($1, $2, 'email.sent', '{"event":"email.sent"}'::jsonb, 'failed', 5, 5, 'HTTP 503', 503)`,
			"whd_seed_autodis_"+strconv.Itoa(i)+"_"+wh.ID, wh.ID,
		)
		if err != nil {
			t.Fatalf("seed failed delivery: %v", err)
		}
	}

	n, err := ts.Store.AutoDisableFailingWebhooks(ctx)
	if err != nil {
		t.Fatalf("AutoDisableFailingWebhooks: %v", err)
	}
	if n != 1 {
		t.Errorf("disabled count = %d, want 1", n)
	}
	after, err := ts.Store.GetWebhookByID(ctx, wh.ID, agent.UserID)
	if err != nil {
		t.Fatalf("GetWebhookByID: %v", err)
	}
	if after.Enabled {
		t.Errorf("webhook should be disabled after threshold failures")
	}
	if after.AutoDisabledAt == nil {
		t.Errorf("auto_disabled_at not set")
	}
}

// summarize turns captured payloads into a debuggable one-line summary
// for failed assertions. Returning the full envelope blob would dwarf
// the actual cause.
func summarize(c []testutil.SubscriberCaptured) string {
	parts := make([]string, 0, len(c))
	for _, x := range c {
		evt, _ := x.Envelope["type"].(string)
		parts = append(parts, x.URL+"["+evt+"]")
	}
	return strings.Join(parts, " ")
}

//go:build integration

package e2e_test

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tokencanopy/e2a/internal/testutil"
)

// asyncSendURL is sendURL without ?wait=sent: it returns the accept-time result
// (202 / status=accepted) instead of blocking until the send is terminal, which
// is what lets this test observe a parent mid-window.
func asyncSendURL(base, agentEmail string) string {
	return base + "/v1/agents/" + url.PathEscape(agentEmail) + "/messages"
}

// errEnvelope is the /v1 error envelope, narrowed to the branchable code.
type errEnvelope struct {
	Err struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func errCodeOf(t *testing.T, body []byte) string {
	t.Helper()
	var e errEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("parse error envelope %q: %v", body, err)
	}
	return e.Err.Code
}

// waitForProviderID polls the message row until the send worker has recorded the
// provider-assigned Message-ID, then returns it. A bounded poll on the state the
// worker produces — not a sleep — so it is deterministic in both directions:
// it cannot pass early, and it fails loudly rather than racing.
//
// Reads via GetRepliableMessage because that is the query the reply/forward
// guard itself consults (and the only one carrying provider_message_id +
// method + delivery_status together) — so the test asserts on exactly the row
// view the handler sees.
func waitForProviderID(t *testing.T, ts *testutil.E2ATestServer, msgID string) string {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for {
		m, err := ts.Store.GetRepliableMessage(ctx, msgID)
		if err != nil {
			t.Fatalf("load message %s: %v", msgID, err)
		}
		if m.ProviderMessageID != "" {
			return m.ProviderMessageID
		}
		if time.Now().After(deadline) {
			t.Fatalf("message %s still has no provider_message_id after 20s (delivery_status=%q)",
				msgID, m.DeliveryStatus)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestReplyToUnsubmittedOutboundParentE2E pins the accept→submit window on the
// async outbound pipeline: the interval where the agent's own outbound parent is
// durably accepted (delivery_status='accepted') but the River send worker has
// not yet submitted it to SES, so provider_message_id — the ONLY id an outbound
// parent threads off (identity.Message.ThreadMessageID) — is still empty.
//
// Replies are composed once, at accept time. Inside that window
// ThreadMessageID() returns "", so a reply's In-Reply-To/References would be
// omitted from its raw bytes PERMANENTLY and the recipient's client would fork a
// new thread. The row's `status` column reads 'sent' the whole time (that column
// is the review/hold axis, not delivery), so nothing else fails closed here.
// The window is sub-second in the happy path but widens to minutes/hours during
// a provider outage, when accepted messages sit out the retry horizon.
//
// The guard turns that silent fork into a retriable 409, and this test proves
// both halves of the contract:
//
//	phase 1 — parent unsubmitted → reply AND forward return 409
//	          message_not_yet_delivered (nothing is silently forked);
//	phase 2 — same parent, once actually submitted → reply returns 200 and its
//	          threading headers anchor on the parent's qualified
//	          provider_message_id (the guard is a narrow window, not a
//	          regression of reply-to-own-outbound).
//
// Determinism: the server is built WithManualJobs, so the River client is never
// started during phase 1 — the send worker has no producer and CANNOT run. No
// sleeps, no races. Phase 2 starts it and polls for the state it produces.
func TestReplyToUnsubmittedOutboundParentE2E(t *testing.T) {
	pool := testutil.TestDB(t)
	// Same fake-SES shape as TestReplyThreadingSESMessageIDE2E: bare id in the
	// 250 response, qualified against the message_id_domain override.
	fakeSMTP, _ := testutil.FakeSMTPServer(t)
	ts := testutil.TestServer(t, pool,
		testutil.WithOutboundSMTP(fakeSMTP.Host, fakeSMTP.Port, "test.e2a.dev"),
		testutil.WithOutboundSMTPMessageIDDomain("us-east-2.amazonses.com"),
		testutil.WithManualJobs())
	_, key, agent := setupDomainAndAgent(t, ts, "agent@window.example.com", "window.example.com", "", "")
	ctx := context.Background()

	// (1) Send without wait=sent. The accept-tx commits the row + the send job;
	// the worker is not running, so the row stays 'accepted' with no provider id.
	status, body := authedJSON(t, "POST", asyncSendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey,
		`{"to":["alice@gmail.com"],"subject":"Kickoff","text":"first touch"}`)
	if status != 202 {
		t.Fatalf("send status=%d body=%s, want 202 (accepted)", status, body)
	}
	var accepted threadSendResp
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("parse send response %q: %v", body, err)
	}
	if accepted.Status != "accepted" {
		t.Fatalf("send status=%q, want accepted", accepted.Status)
	}
	if accepted.ProviderMessageID != "" {
		t.Fatalf("accepted send already has provider_message_id %q — the window did not open",
			accepted.ProviderMessageID)
	}

	// Pin the window in the DB: this is the exact state the guard must catch —
	// an external ('smtp', not 'loopback') outbound, accepted, no provider id.
	parent, err := ts.Store.GetRepliableMessage(ctx, accepted.MessageID)
	if err != nil {
		t.Fatalf("load parent row: %v", err)
	}
	if parent.DeliveryStatus != "accepted" || parent.ProviderMessageID != "" || parent.Method == "loopback" {
		t.Fatalf("parent row = {delivery_status:%q provider_message_id:%q method:%q}, want {accepted, \"\", non-loopback}",
			parent.DeliveryStatus, parent.ProviderMessageID, parent.Method)
	}

	// (2) Reply inside the window → retriable 409, not a silently forked thread.
	status, body = authedJSON(t, "POST",
		subResource(ts.HTTPServer.URL, agent.EmailAddress(), accepted.MessageID, "reply"),
		key.PlaintextKey, `{"text":"second touch"}`)
	if status != 409 {
		t.Fatalf("reply to unsubmitted parent status=%d body=%s, want 409", status, body)
	}
	if code := errCodeOf(t, body); code != "message_not_yet_delivered" {
		t.Errorf("reply error code = %q, want message_not_yet_delivered (body=%s)", code, body)
	}

	// (3) Forward shares the parent-resolution seam, so it must fire identically.
	status, body = authedJSON(t, "POST",
		subResource(ts.HTTPServer.URL, agent.EmailAddress(), accepted.MessageID, "forward"),
		key.PlaintextKey, `{"to":["bob@gmail.com"],"text":"fyi"}`)
	if status != 409 {
		t.Fatalf("forward of unsubmitted parent status=%d body=%s, want 409", status, body)
	}
	if code := errCodeOf(t, body); code != "message_not_yet_delivered" {
		t.Errorf("forward error code = %q, want message_not_yet_delivered (body=%s)", code, body)
	}

	// (4) Close the window: start the worker and wait for the submit to record
	// the qualified provider id.
	ts.StartJobs(t, ctx)
	providerID := waitForProviderID(t, ts, accepted.MessageID)
	qualified := regexp.MustCompile(`^<[^@>]+@us-east-2\.amazonses\.com>$`)
	if !qualified.MatchString(providerID) {
		t.Fatalf("provider_message_id = %q, want the domain-qualified on-wire form <id@us-east-2.amazonses.com>",
			providerID)
	}

	// (5) The SAME reply now succeeds and threads onto the parent — proving the
	// guard bounds a window rather than blocking reply-to-own-outbound.
	status, body = authedJSON(t, "POST",
		subResource(ts.HTTPServer.URL, agent.EmailAddress(), accepted.MessageID, "reply"),
		key.PlaintextKey, `{"text":"second touch"}`)
	if status != 200 {
		t.Fatalf("reply after submit status=%d body=%s, want 200", status, body)
	}
	rep := parseThreadSend(t, body)
	repMsg, err := ts.Store.GetMessageWithContent(ctx, rep.MessageID, agent.ID)
	if err != nil {
		t.Fatalf("load reply row: %v", err)
	}
	raw := string(repMsg.RawMessage)
	if !strings.Contains(raw, "In-Reply-To: "+providerID) {
		t.Errorf("reply In-Reply-To not anchored on the parent's on-wire Message-ID %q; raw headers=\n%s",
			providerID, raw[:min(len(raw), 600)])
	}
	if !strings.Contains(raw, "References: "+providerID) {
		t.Errorf("reply References missing the parent's on-wire Message-ID %q; raw headers=\n%s",
			providerID, raw[:min(len(raw), 600)])
	}
}

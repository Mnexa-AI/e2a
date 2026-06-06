package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5"
)

// fakePublisher captures Publish calls so the tests can assert on the
// envelope without spinning up a DB.
type fakePublisher struct {
	mu     sync.Mutex
	events []webhookpub.Event
}

func (f *fakePublisher) Publish(_ context.Context, e webhookpub.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakePublisher) wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		l := len(f.events)
		f.mu.Unlock()
		if l >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("publisher: timed out waiting for %d events (got %d)", n, len(f.events))
}

func TestPublishAsync_NilPublisherIsNoOp(t *testing.T) {
	a := &API{}
	// Should not panic.
	a.publishAsync(webhookpub.Event{Type: "email.sent"})
}

func TestBuildSentEvent_PopulatesEnvelope(t *testing.T) {
	a := &API{}
	agent := &identity.AgentIdentity{
		ID:     "bot@x.example.com",
		Domain: "x.example.com",
		UserID: "u_1",
	}
	outMsg := &identity.Message{ID: "msg_1"}
	res := &outbound.SendResult{
		MessageID: "ses_1",
		Method:    "smtp",
		To:        []string{"alice@example.com"},
	}
	req := outbound.SendRequest{
		To:             []string{"alice@example.com"},
		Subject:        "hello",
		ConversationID: "conv_42",
	}
	ev := a.buildSentEvent(agent, outMsg, res, req, "send")
	if ev.Type != webhookpub.EventEmailSent {
		t.Errorf("Type = %q, want email.sent", ev.Type)
	}
	if ev.UserID != "u_1" {
		t.Errorf("UserID = %q, want u_1", ev.UserID)
	}
	if ev.AgentID != agent.ID {
		t.Errorf("AgentID = %q, want %q", ev.AgentID, agent.ID)
	}
	if ev.MessageID != "msg_1" {
		t.Errorf("MessageID = %q, want msg_1", ev.MessageID)
	}
	if ev.ConversationID != "conv_42" {
		t.Errorf("ConversationID = %q, want conv_42", ev.ConversationID)
	}
	data, ok := ev.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data is not a map: %T", ev.Data)
	}
	if data["subject"] != "hello" {
		t.Errorf("subject = %v, want hello", data["subject"])
	}
}

func TestBuildSentEvent_NilOutMsgUsesEmptyMessageID(t *testing.T) {
	a := &API{}
	agent := &identity.AgentIdentity{ID: "bot@x.example.com", UserID: "u_1"}
	res := &outbound.SendResult{MessageID: "ses_2", Method: "smtp"}
	ev := a.buildSentEvent(agent, nil, res, outbound.SendRequest{}, "send")
	if ev.MessageID != "" {
		t.Errorf("MessageID should be empty when outMsg is nil, got %q", ev.MessageID)
	}
}

func TestBuildPendingApprovalEvent(t *testing.T) {
	a := &API{}
	agent := &identity.AgentIdentity{ID: "bot@x.example.com", UserID: "u_1"}
	expiry := time.Now().Add(1 * time.Hour)
	msg := &identity.Message{ID: "pend_1", ApprovalExpiresAt: &expiry}
	req := outbound.SendRequest{To: []string{"alice@example.com"}, Subject: "review me"}
	ev := a.buildPendingApprovalEvent(agent, msg, req, "send")
	if ev.Type != webhookpub.EventEmailPendingApproval {
		t.Errorf("Type = %q, want email.pending_approval", ev.Type)
	}
	if ev.MessageID != "pend_1" {
		t.Errorf("MessageID = %q, want pend_1", ev.MessageID)
	}
	if ev.UserID != "u_1" {
		t.Errorf("UserID = %q", ev.UserID)
	}
}

func TestBuildApprovedEvent(t *testing.T) {
	a := &API{}
	agent := &identity.AgentIdentity{ID: "bot@x.example.com", UserID: "u_1"}
	sent := &identity.Message{
		ID:                "msg_a",
		Subject:           "hi",
		Type:              "send",
		ProviderMessageID: "ses_a",
		Method:            "smtp",
		ToRecipients:      []string{"alice@example.com"},
		Edited:            true,
	}
	ev := a.buildApprovedEvent(agent, sent, "u_reviewer")
	if ev.Type != webhookpub.EventEmailApproved {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.MessageID != "msg_a" {
		t.Errorf("MessageID = %q", ev.MessageID)
	}
	data := ev.Data.(map[string]interface{})
	if data["edited"] != true {
		t.Errorf("edited = %v, want true", data["edited"])
	}
	if data["reviewed_by_user_id"] != "u_reviewer" {
		t.Errorf("reviewed_by_user_id = %v", data["reviewed_by_user_id"])
	}
}

func TestBuildRejectedEvent(t *testing.T) {
	a := &API{}
	rejected := &identity.Message{ID: "msg_r", AgentID: "bot@x.example.com", Type: "send"}
	ev := a.buildRejectedEvent("u_reviewer", rejected, "off-policy")
	if ev.Type != webhookpub.EventEmailRejected {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.MessageID != "msg_r" {
		t.Errorf("MessageID = %q", ev.MessageID)
	}
	data := ev.Data.(map[string]interface{})
	if data["rejection_reason"] != "off-policy" {
		t.Errorf("rejection_reason = %v", data["rejection_reason"])
	}
}

func TestPublishAsync_DispatchesViaPublisher(t *testing.T) {
	fp := &fakePublisher{}
	a := &API{publisher: fp}
	a.publishAsync(webhookpub.Event{Type: webhookpub.EventEmailSent, UserID: "u_1"})
	fp.wait(t, 1)
	if fp.events[0].Type != webhookpub.EventEmailSent {
		t.Errorf("Type = %q", fp.events[0].Type)
	}
}

// ─── C3 fix: dual-path dedup ──────────────────────────────────────
//
// When the outbox flag is on, the API's publish helpers must NOT
// also fire the legacy publisher.Publish goroutine. Two writes to
// webhook_subscriber_deliveries — one from the outbox worker (with
// event_id set) and one from publisher.Publish (with event_id=NULL)
// — cannot be dedup'd by the partial unique index
// idx_wsd_event_webhook_uniq because the index excludes NULL
// event_ids. Customer's webhook fires twice per event. The
// outboxEnabled() helper is what suppresses the legacy branch.

// fakeAgentOutbox satisfies webhookpub.Outbox enough for the helper-
// level tests. Records best-effort + PublishTx calls and lets the
// test toggle Enabled() to exercise the dedup branch.
type fakeAgentOutbox struct {
	enabled         bool
	publishTxN      int
	publishTxErr    error // simulate PublishTx failure for fallback tests
	bestEffortN     int
	bestEffortWrote bool // simulate PublishBestEffortTx wrote=true
	lastPublishTx   webhookpub.Event
}

func (f *fakeAgentOutbox) PublishTx(_ context.Context, _ pgx.Tx, e webhookpub.Event) error {
	f.publishTxN++
	f.lastPublishTx = e
	return f.publishTxErr
}

func (f *fakeAgentOutbox) PublishBestEffortTx(_ context.Context, _ pgx.Tx, _ webhookpub.Event) bool {
	f.bestEffortN++
	return f.bestEffortWrote
}

func (f *fakeAgentOutbox) DeleteExpiredWebhookEvents(_ context.Context) (int, error) { return 0, nil }
func (f *fakeAgentOutbox) Enabled() bool                                             { return f.enabled }

// The C3 dedup/fallback gating is centralized in shouldFireLegacy.
// We test that helper directly below (TestShouldFireLegacy_*) — it's
// what gates the publishAsync call in all four publishX helpers.
//
// The publishX wrappers are thin: they run the outbox block (when
// applicable), record whether the outbox wrote, then call
// shouldFireLegacy. Exercising them end-to-end would require a real
// *identity.Store for WithTx, so the per-wrapper integration
// coverage lives in internal/webhookpub/outbox_integration_test.go.
//
// DEFERRED (review follow-up): add DB-backed assertions that the
// `outboxWrote` plumbing inside each publishX correctly stays false
// when (a) WithTx itself returns an error before PublishTx runs and
// (b) PublishTx/PublishBestEffortTx returns a failure. The current
// unit tests pin shouldFireLegacy(outboxWrote) at every input; the
// gap is the wire between WithTx's error path and the call site's
// outboxWrote reassignment. Each helper has the explicit reset
// (api.go publishSent:326, publishApproved:378) or sets true only
// inside the else (publishPendingApproval:356, publishRejected:400),
// so the gating is correct by inspection — a regression test would
// add belt-and-suspenders.
//
// The publisher-fires-when-no-outbox path (deployments that haven't
// wired SetOutbox) is covered by the test below.

func TestPublishSent_FiresLegacyWhenNoOutbox(t *testing.T) {
	// Defensive: when outbox is nil entirely (deployments that haven't
	// wired the slice-3 SetOutbox call), legacy must still fire.
	fp := &fakePublisher{}
	a := &API{publisher: fp}
	a.publishSent(context.Background(), webhookpub.Event{Type: webhookpub.EventEmailSent}, nil)
	fp.wait(t, 1)
}

// ─── Fallback semantics: legacy fires when outbox failed ────────
//
// These tests are the load-bearing assertions for the *revised* C3
// fix. The first version suppressed legacy purely on outbox.Enabled()
// — review caught that as introducing a worse bug: if outbox is
// enabled and its write fails, no path delivers the event. The
// revised contract is: legacy fires when (a) outbox is disabled OR
// (b) outbox is enabled but did not write.
//
// To exercise the bug, the tests need the outbox block to actually
// run (so we need a stub *identity.Store with WithTx). We can't
// supply that without a DB, so these tests exercise shouldFireLegacy
// directly — the helper is what gates the publishAsync call.

func TestShouldFireLegacy_NoOutbox_AlwaysTrue(t *testing.T) {
	a := &API{}
	if !a.shouldFireLegacy(false) {
		t.Errorf("shouldFireLegacy(false) with nil outbox = false; want true (legacy is sole path)")
	}
	if !a.shouldFireLegacy(true) {
		t.Errorf("shouldFireLegacy(true) with nil outbox = false; want true (legacy is sole path; outboxWrote is irrelevant)")
	}
}

func TestShouldFireLegacy_OutboxDisabled_AlwaysTrue(t *testing.T) {
	a := &API{outbox: &fakeAgentOutbox{enabled: false}}
	if !a.shouldFireLegacy(false) {
		t.Errorf("disabled outbox: shouldFireLegacy(false) = false; want true")
	}
	if !a.shouldFireLegacy(true) {
		t.Errorf("disabled outbox: shouldFireLegacy(true) = false; want true")
	}
}

func TestShouldFireLegacy_OutboxEnabledAndWrote_SuppressesLegacy(t *testing.T) {
	a := &API{outbox: &fakeAgentOutbox{enabled: true}}
	if a.shouldFireLegacy(true) {
		t.Errorf("enabled outbox + wrote: shouldFireLegacy(true) = true; want false (this is the C3 dedup)")
	}
}

func TestShouldFireLegacy_OutboxEnabledButFailed_FallsBackToLegacy(t *testing.T) {
	// LOAD-BEARING: revised C3 fix. Pre-revision, this returned false
	// (legacy suppressed when outbox enabled, regardless of write
	// outcome) — which silently dropped events when the outbox write
	// failed. The fallback to legacy when outboxWrote=false is what
	// prevents that loss.
	a := &API{outbox: &fakeAgentOutbox{enabled: true}}
	if !a.shouldFireLegacy(false) {
		t.Errorf("enabled outbox + did NOT write: shouldFireLegacy(false) = false; want true (legacy is the at-least-once fallback when outbox failed)")
	}
}

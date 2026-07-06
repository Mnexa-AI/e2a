package agent

import (
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

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
	if ev.Type != webhookpub.EventEmailPendingReview {
		t.Errorf("Type = %q, want email.pending_review", ev.Type)
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
	if ev.Type != webhookpub.EventEmailReviewApproved {
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
	if ev.Type != webhookpub.EventEmailReviewRejected {
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

func TestEmitBlockedOutbound(t *testing.T) {
	// email.blocked emits through the (unconditional) outbox, not a legacy
	// publisher, so assert the built event's shape/routing directly.
	a := &API{}
	agent := &identity.AgentIdentity{ID: "bot@x.example.com", Domain: "x.example.com", UserID: "u_1"}
	req := outbound.SendRequest{To: []string{"alice@evil.com"}, Subject: "blocked one", ConversationID: "conv_9"}
	v := outboundVerdict{Applied: "block", ReviewReason: "recipient_gate", Reason: "recipient not in allowlist"}
	softRef := blockAuditID(agent.ID, req)

	ev := a.buildBlockedOutboundEvent(agent, softRef, req, v)

	if ev.Type != webhookpub.EventEmailBlocked {
		t.Errorf("Type = %q, want email.blocked", ev.Type)
	}
	if ev.UserID != "u_1" || ev.AgentID != agent.ID || ev.MessageID != softRef {
		t.Errorf("routing keys = (user=%q agent=%q msg=%q)", ev.UserID, ev.AgentID, ev.MessageID)
	}
	// Deterministic id keeps a retried block idempotent.
	if want := webhookpub.DeterministicEventID(softRef, webhookpub.EventEmailBlocked); ev.ID != want {
		t.Errorf("ID = %q, want deterministic %q", ev.ID, want)
	}
	data, ok := ev.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data is not a map: %T", ev.Data)
	}
	if data["direction"] != "outbound" {
		t.Errorf("direction = %v, want outbound", data["direction"])
	}
	if data["reason_source"] != "recipient_gate" {
		t.Errorf("reason_source = %v, want recipient_gate", data["reason_source"])
	}
}

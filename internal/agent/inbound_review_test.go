package agent

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// TestBuildInboundReleasedEvent_Envelope pins the design-critical shape of the
// release signal: an approved inbound message gets NO email.received re-fire, so
// email.review_approved (direction=inbound) is its only push notification.
func TestBuildInboundReleasedEvent_Envelope(t *testing.T) {
	a := &API{}
	msg := &identity.ReviewMessageMeta{
		ID: "msg_in1", AgentID: "bot@x.example.com", Direction: "inbound",
		Sender: "evil@x.com", Subject: "Held", Type: "received",
	}
	// owner is the routing key; reviewer is the human who acted (distinct args so
	// a future non-owner reviewer can't misroute the event).
	ev := a.buildInboundReleasedEvent(msg, "u_owner", "u_reviewer")
	if ev.Type != webhookpub.EventEmailReviewApproved {
		t.Errorf("Type = %q, want email.review_approved", ev.Type)
	}
	if ev.AgentID != "bot@x.example.com" || ev.MessageID != "msg_in1" {
		t.Errorf("routing keys wrong: agent=%q msg=%q", ev.AgentID, ev.MessageID)
	}
	// UserID is the routing key — the agent OWNER's webhooks must fire, not the
	// reviewer's.
	if ev.UserID != "u_owner" {
		t.Errorf("UserID (routing) = %q, want the owner u_owner", ev.UserID)
	}
	data := ev.Data.(map[string]interface{})
	if data["direction"] != "inbound" {
		t.Errorf("direction = %v, want inbound", data["direction"])
	}
	if data["from"] != "evil@x.com" || data["subject"] != "Held" {
		t.Errorf("payload missing sender/subject: %v", data)
	}
	if data["reviewed_by_user_id"] != "u_reviewer" {
		t.Errorf("reviewed_by_user_id = %v, want the reviewer u_reviewer", data["reviewed_by_user_id"])
	}
}

// TestBuildInboundRejectedEvent_Envelope pins the drop signal shape.
func TestBuildInboundRejectedEvent_Envelope(t *testing.T) {
	a := &API{}
	msg := &identity.ReviewMessageMeta{ID: "msg_in2", AgentID: "bot@x.example.com", Direction: "inbound", Type: "received"}
	ev := a.buildInboundRejectedEvent(msg, "u_owner", "u_reviewer", "prompt injection")
	if ev.Type != webhookpub.EventEmailReviewRejected {
		t.Errorf("Type = %q, want email.review_rejected", ev.Type)
	}
	if ev.UserID != "u_owner" {
		t.Errorf("UserID (routing) = %q, want the owner u_owner", ev.UserID)
	}
	data := ev.Data.(map[string]interface{})
	if data["direction"] != "inbound" || data["rejection_reason"] != "prompt injection" {
		t.Errorf("payload wrong: %v", data)
	}
	if data["reviewed_by_user_id"] != "u_reviewer" {
		t.Errorf("reviewed_by_user_id = %v, want the reviewer u_reviewer", data["reviewed_by_user_id"])
	}
}

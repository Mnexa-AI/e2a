package agent

import (
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// --- Webhooks-as-a-resource HTTP layer (slice 2) ---
//
// The handlers here serve POST/GET/LIST/PATCH/DELETE on /api/v1/webhooks
// plus /rotate-secret, /test, /deliveries subresources. The storage
// layer in internal/identity/webhooks.go does the per-row work; this
// layer applies the public-facing validation rules from the design.

// NOTE: webhook create/update validation (URL/SSRF, event types, filter caps,
// agent-ownership) now lives in the typed /v1 layer (internal/httpapi/
// webhooks.go) — the legacy copy that lived here was removed in the v1 cutover
// along with its routes. The event builders below remain (they feed the live
// publisher, not the removed HTTP handlers).

// --- event builders (slice 3) ---
//
// Each helper translates an in-process trigger into a webhookpub.Event.
// They live here (next to the webhook resource) so the publisher's
// envelope shape is co-located with the handler shape, and the
// build sites in api.go / hitl_api.go stay one-line.

// buildSentEvent constructs an email.sent event for /send, /reply,
// /forward. outMsg may be nil if CreateOutboundMessage failed — in
// that case we still publish (the SES send already happened) but with
// an empty message_id; receivers see the event without a row to fetch.
func (a *API) buildSentEvent(
	agent *identity.AgentIdentity,
	outMsg *identity.Message,
	result *outbound.SendResult,
	req outbound.SendRequest,
	msgType string,
) webhookpub.Event {
	messageID := ""
	if outMsg != nil {
		messageID = outMsg.ID
	}
	data := map[string]interface{}{
		"message_id":          messageID,
		"provider_message_id": result.MessageID,
		"method":              result.Method,
		"from":                agent.EmailAddress(),
		"to":                  result.To,
		"cc":                  result.CC,
		"bcc":                 result.BCC,
		"subject":             req.Subject,
		"type":                msgType,
		"conversation_id":     req.ConversationID,
	}
	return webhookpub.Event{
		ID:             generateEventIDForAgent(),
		Type:           webhookpub.EventEmailSent,
		CreatedAt:      time.Now().UTC(),
		UserID:         agent.UserID,
		AgentID:        agent.ID,
		ConversationID: req.ConversationID,
		MessageID:      messageID,
		Data:           data,
	}
}

// buildPendingApprovalEvent fires when a HITL-enabled agent holds an
// outbound message for human review. msg is the pending row; req is
// the composed SendRequest that produced it.
func (a *API) buildPendingApprovalEvent(
	agent *identity.AgentIdentity,
	msg *identity.Message,
	req outbound.SendRequest,
	msgType string,
) webhookpub.Event {
	data := map[string]interface{}{
		"message_id":          msg.ID,
		"from":                agent.EmailAddress(),
		"to":                  req.To,
		"cc":                  req.CC,
		"bcc":                 req.BCC,
		"subject":             req.Subject,
		"type":                msgType,
		"conversation_id":     req.ConversationID,
		"approval_expires_at": msg.ApprovalExpiresAt,
	}
	return webhookpub.Event{
		ID:             generateEventIDForAgent(),
		Type:           webhookpub.EventEmailPendingApproval,
		CreatedAt:      time.Now().UTC(),
		UserID:         agent.UserID,
		AgentID:        agent.ID,
		ConversationID: req.ConversationID,
		MessageID:      msg.ID,
		Data:           data,
	}
}

// buildApprovedEvent fires after ApproveAndSend hands the message to
// SES. sent carries the post-approve message row (now status=sent).
func (a *API) buildApprovedEvent(
	agent *identity.AgentIdentity,
	sent *identity.Message,
	reviewerUserID string,
) webhookpub.Event {
	data := map[string]interface{}{
		"message_id":          sent.ID,
		"provider_message_id": sent.ProviderMessageID,
		"method":              sent.Method,
		"from":                agent.EmailAddress(),
		"to":                  sent.ToRecipients,
		"subject":             sent.Subject,
		"type":                sent.Type,
		"edited":              sent.Edited,
		"reviewed_by_user_id": reviewerUserID,
	}
	return webhookpub.Event{
		ID:        generateEventIDForAgent(),
		Type:      webhookpub.EventEmailApproved,
		CreatedAt: time.Now().UTC(),
		UserID:    agent.UserID,
		AgentID:   agent.ID,
		MessageID: sent.ID,
		Data:      data,
	}
}

// buildRejectedEvent fires when a reviewer rejects a pending outbound.
// userID is the reviewer (a separate identity from the agent's owner
// when multi-reviewer ACLs land in a future slice — today they're
// always the same).
func (a *API) buildRejectedEvent(
	userID string,
	rejected *identity.Message,
	reason string,
) webhookpub.Event {
	data := map[string]interface{}{
		"message_id":          rejected.ID,
		"type":                rejected.Type,
		"rejection_reason":    reason,
		"reviewed_by_user_id": userID,
	}
	return webhookpub.Event{
		ID:        generateEventIDForAgent(),
		Type:      webhookpub.EventEmailRejected,
		CreatedAt: time.Now().UTC(),
		UserID:    userID,
		AgentID:   rejected.AgentID,
		MessageID: rejected.ID,
		Data:      data,
	}
}

// generateEventIDForAgent wraps webhookpub's id helper so this file
// doesn't need to import internal/webhookpub just for the constructor.
func generateEventIDForAgent() string {
	return webhookpub.NewEvent("", "", nil).ID
}

// --- helpers ---

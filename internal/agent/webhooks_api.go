package agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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

// Per-resource caps. Mirror the design's locked values.
const (
	webhookMaxAgentIDs        = 50
	webhookMaxConversationIDs = 50
	webhookMaxLabels          = 50
	webhookMaxFilterValueLen  = 200
)

// validateCreateUpdateRequest applies every validation rule the design
// mandates for both POST and PATCH (full-replace fields). Returns a
// human-readable error string (handler maps to 400) or "" if valid.
//
// The caller passes the already-resolved user (for the agent ownership
// check) and the canonical filter / events slices to validate. URL
// validation goes through the existing ValidateWebhookURL helper to
// reuse its SSRF protections.
func (a *API) validateWebhookFields(user *identity.User, url string, events []string, filters identity.WebhookFilters, description string) (msg string, status int) {
	if url != "" {
		if err := ValidateWebhookURL(url); err != nil {
			return fmt.Sprintf("invalid url: %v", err), http.StatusBadRequest
		}
		if len(url) > 2048 {
			return "url too long (max 2048 chars)", http.StatusBadRequest
		}
	}
	if len(events) == 0 {
		return "events must be a non-empty array", http.StatusBadRequest
	}
	seen := map[string]bool{}
	for _, e := range events {
		if !webhookpub.IsValidEventType(e) {
			return fmt.Sprintf("unknown event type %q (allowed: %s)", e, strings.Join(webhookpub.AllEventTypes, ", ")), http.StatusBadRequest
		}
		if seen[e] {
			continue
		}
		seen[e] = true
	}
	if len(description) > 200 {
		return "description too long (max 200 chars)", http.StatusBadRequest
	}
	if strings.ContainsAny(description, "\r\n") {
		return "description must not contain CR or LF", http.StatusBadRequest
	}

	if len(filters.AgentIDs) > webhookMaxAgentIDs {
		return fmt.Sprintf("filters.agent_ids exceeds cap of %d", webhookMaxAgentIDs), http.StatusBadRequest
	}
	if len(filters.ConversationIDs) > webhookMaxConversationIDs {
		return fmt.Sprintf("filters.conversation_ids exceeds cap of %d", webhookMaxConversationIDs), http.StatusBadRequest
	}
	if len(filters.Labels) > webhookMaxLabels {
		return fmt.Sprintf("filters.labels exceeds cap of %d", webhookMaxLabels), http.StatusBadRequest
	}

	for _, agentEmail := range filters.AgentIDs {
		// agent_ids must reference agents the caller owns. Use the
		// existing ListAgentsByUser query rather than introducing a
		// new one; at filter-list size ≤ 50 the cost is negligible.
		// The check fails on the first unowned id with a clear
		// "cross-user agent" error message.
		if agentEmail == "" {
			return "filters.agent_ids contains empty entry", http.StatusBadRequest
		}
		if len(agentEmail) > webhookMaxFilterValueLen {
			return "filters.agent_ids entry exceeds 200 chars", http.StatusBadRequest
		}
	}
	if msg, status := a.assertAgentsOwnedByUser(user.ID, filters.AgentIDs); msg != "" {
		return msg, status
	}

	for _, c := range filters.ConversationIDs {
		if c == "" || len(c) > webhookMaxFilterValueLen {
			return "filters.conversation_ids contains empty entry or one over 200 chars", http.StatusBadRequest
		}
		// Conversation IDs are case-sensitive, charset
		// [a-zA-Z0-9_-]+.
		for _, r := range c {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '-' || r == '_'
			if !ok {
				return fmt.Sprintf("filters.conversation_ids[%q]: invalid character", c), http.StatusBadRequest
			}
		}
	}
	for _, l := range filters.Labels {
		if l == "" || len(l) > 64 {
			return "filters.labels contains empty entry or one over 64 chars", http.StatusBadRequest
		}
		// Labels charset same as the labels feature: [a-z0-9:_-]+
		// (lowercased — the storage layer normalizes on writes but
		// since we don't normalize here, reject case-divergent
		// values rather than silently bait-and-switch).
		for _, r := range l {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= '0' && r <= '9') ||
				r == ':' || r == '-' || r == '_'
			if !ok {
				return fmt.Sprintf("filters.labels[%q]: invalid character (expected [a-z0-9:_-]+, lowercase)", l), http.StatusBadRequest
			}
		}
	}

	return "", 0
}

// assertAgentsOwnedByUser verifies that every email in the given slice
// is one of the user's agents. Returns "" on success. On a non-owned
// id, returns a clear error referencing the specific id so the caller
// can fix the filter without guessing.
func (a *API) assertAgentsOwnedByUser(userID string, agentEmails []string) (msg string, status int) {
	if len(agentEmails) == 0 {
		return "", 0
	}
	agents, err := a.store.ListAgentsByUser(context.Background(), userID)
	if err != nil {
		return "failed to verify agent ownership", http.StatusInternalServerError
	}
	owned := map[string]bool{}
	for _, ag := range agents {
		owned[ag.ID] = true
	}
	for _, email := range agentEmails {
		if !owned[email] {
			return fmt.Sprintf("filters.agent_ids[%q]: not owned by this user", email), http.StatusBadRequest
		}
	}
	return "", 0
}

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

// webhookResponseFromIdentity builds the wire response from a stored
// row. When includeSecret is true, the signing_secret plaintext is
// included (POST + rotate). Every other endpoint omits it.
func webhookResponseFromIdentity(wh *identity.Webhook, includeSecret bool) map[string]interface{} {
	out := map[string]interface{}{
		"id":          wh.ID,
		"url":         wh.URL,
		"description": wh.Description,
		"events":      wh.Events,
		"filters":     wh.Filters,
		"enabled":     wh.Enabled,
		"created_at":  wh.CreatedAt.UTC().Format(time.RFC3339),
	}
	if includeSecret {
		out["signing_secret"] = wh.SigningSecret
	}
	if wh.AutoDisabledAt != nil {
		out["auto_disabled_at"] = wh.AutoDisabledAt.UTC().Format(time.RFC3339)
	}
	if wh.LastDeliveredAt != nil {
		out["last_delivered_at"] = wh.LastDeliveredAt.UTC().Format(time.RFC3339)
	}
	return out
}

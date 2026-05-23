package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// pendingMessageSummary is the shape returned by GET /api/v1/messages for
// HITL listing. Body and attachments are omitted — clients fetch detail per
// message via the ID-scoped endpoint.
type pendingMessageSummary struct {
	ID                string   `json:"id"`
	AgentID           string   `json:"agent_id"`
	Direction         string   `json:"direction"`
	Subject           string   `json:"subject"`
	Type              string   `json:"type,omitempty"`
	ConversationID    string   `json:"conversation_id,omitempty"`
	To                []string `json:"to"`
	CC                []string `json:"cc,omitempty"`
	BCC               []string `json:"bcc,omitempty"`
	Status            string   `json:"status"`
	ApprovalExpiresAt string   `json:"approval_expires_at,omitempty"`
	CreatedAt         string   `json:"created_at"`
}

// pendingMessageDetail adds body, attachments, and review metadata to the
// summary. Body columns are populated only while the row is pending; for
// terminal statuses they are scrubbed and omitted.
type pendingMessageDetail struct {
	pendingMessageSummary
	EmailMessageID    string                `json:"email_message_id,omitempty"` // inbound Message-ID being replied to
	BodyText          string                `json:"body_text,omitempty"`
	BodyHTML          string                `json:"body_html,omitempty"`
	Attachments       []outbound.Attachment `json:"attachments,omitempty"`
	Edited            bool                  `json:"edited,omitempty"`
	ReviewedAt        string                `json:"reviewed_at,omitempty"`
	ReviewedByUserID  *string               `json:"reviewed_by_user_id,omitempty"`
	ReviewedByName    *string               `json:"reviewed_by_name,omitempty"`
	RejectionReason   string                `json:"rejection_reason,omitempty"`
	ProviderMessageID string                `json:"provider_message_id,omitempty"`
	Method            string                `json:"method,omitempty"`
}

func messageToSummary(m identity.Message) pendingMessageSummary {
	s := pendingMessageSummary{
		ID:             m.ID,
		AgentID:        m.AgentID,
		Direction:      m.Direction,
		Subject:        m.Subject,
		Type:           m.Type,
		ConversationID: m.ConversationID,
		To:             m.ToRecipients,
		CC:             m.CC,
		BCC:            m.BCC,
		Status:         m.Status,
		CreatedAt:      m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if m.ApprovalExpiresAt != nil {
		s.ApprovalExpiresAt = m.ApprovalExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return s
}

func messageToDetail(m identity.Message) pendingMessageDetail {
	d := pendingMessageDetail{
		pendingMessageSummary: messageToSummary(m),
		EmailMessageID:        m.EmailMessageID,
		BodyText:              m.BodyText,
		BodyHTML:              m.BodyHTML,
		Edited:                m.Edited,
		ReviewedByUserID:      m.ReviewedByUserID,
		ReviewedByName:        m.ReviewedByName,
		RejectionReason:       m.RejectionReason,
		ProviderMessageID:     m.ProviderMessageID,
		Method:                m.Method,
	}
	if m.ReviewedAt != nil {
		d.ReviewedAt = m.ReviewedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if len(m.AttachmentsJSON) > 0 {
		var attachments []outbound.Attachment
		if err := json.Unmarshal(m.AttachmentsJSON, &attachments); err == nil {
			d.Attachments = attachments
		}
	}
	return d
}

// handleListMessages serves GET /api/v1/messages. Currently only
// ?status=pending_approval is supported — it returns the user's pending
// messages across all their agents, sorted by approval_expires_at ASC.
// @Summary      List messages waiting for HITL approval
// @Description  Returns all pending_approval messages across every agent owned by the authenticated user, sorted by expiring-soonest first. Body and attachments are omitted — use the detail endpoint for full content. This is the only status value supported on this endpoint today.
// @Tags         HITL
// @Produce      json
// @Security     BearerAuth
// @Param        status query string true "Filter by status (only pending_approval is supported)" Enums(pending_approval)
// @Success      200 {object} ListPendingMessagesResponse
// @Failure      400 {string} string "Unsupported status"
// @Failure      401 {string} string "Missing or invalid API key"
// @Router       /api/v1/messages [get]
func (a *API) handleListMessages(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = identity.MessageStatusPendingApproval
	}
	if status != identity.MessageStatusPendingApproval {
		http.Error(w, "only status=pending_approval is supported on this endpoint", http.StatusBadRequest)
		return
	}

	msgs, err := a.store.ListPendingOutboundForUser(r.Context(), user.ID, 100)
	if err != nil {
		log.Printf("[api] list pending: %v", err)
		http.Error(w, "failed to list pending messages", http.StatusInternalServerError)
		return
	}

	out := make([]pendingMessageSummary, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageToSummary(m))
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{
		"messages": out,
	})
}

// handleGetOutboundMessage serves GET /api/v1/messages/{id}. Returns the
// full outbound-message detail — body and attachments while pending,
// scrubbed after terminal transitions.
// @Summary      Fetch a held outbound message
// @Description  Returns the full pending-approval detail — recipients, subject, body, attachments, and HITL metadata — for one of the authenticated user's outbound messages. After terminal transitions (sent, rejected, expired_*) the server scrubs body columns, so this endpoint returns the headers without content for non-pending rows.
// @Tags         HITL
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Message ID" example(msg_abc123)
// @Success      200 {object} PendingMessageDetail
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      404 {string} string "Message not found or not owned by this user"
// @Router       /api/v1/messages/{id} [get]
func (a *API) handleGetOutboundMessage(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	messageID := mux.Vars(r)["id"]
	msg, err := a.store.GetOutboundMessageForUser(r.Context(), messageID, user.ID)
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, messageToDetail(*msg))
}

// approveRequest is the JSON body accepted by the approve endpoint. Every
// field is optional; any field present overrides the stored value before
// the message is sent. Using pointer types distinguishes "field not
// provided" (nil) from "explicitly empty" (non-nil pointer to zero value).
type approveRequest struct {
	Subject     *string                `json:"subject,omitempty"`
	BodyText    *string                `json:"body_text,omitempty"`
	BodyHTML    *string                `json:"body_html,omitempty"`
	To          *[]string              `json:"to,omitempty"`
	CC          *[]string              `json:"cc,omitempty"`
	BCC         *[]string              `json:"bcc,omitempty"`
	Attachments *[]outbound.Attachment `json:"attachments,omitempty"`
}

func (req approveRequest) toEdit() (identity.PendingApprovalEdit, error) {
	e := identity.PendingApprovalEdit{
		Subject:  req.Subject,
		BodyText: req.BodyText,
		BodyHTML: req.BodyHTML,
	}
	if req.To != nil {
		e.To = *req.To
	}
	if req.CC != nil {
		e.CC = *req.CC
	}
	if req.BCC != nil {
		e.BCC = *req.BCC
	}
	if req.Attachments != nil {
		attJSON, err := json.Marshal(*req.Attachments)
		if err != nil {
			return identity.PendingApprovalEdit{}, err
		}
		e.AttachmentsJSON = attJSON
		e.AttachmentsSet = true
	}
	return e, nil
}

// handleApprovePendingMessage serves POST /api/v1/messages/{id}/approve.
// Applies any overrides from the request body, sends via SES, and
// transitions the row to 'sent' with body scrubbed.
// @Summary      Approve a held outbound message
// @Description  Sends a pending-approval message via the upstream SMTP relay and transitions it to `sent`. The request body is optional — passing any subset of subject / body_text / body_html / to / cc / bcc / attachments overrides the stored draft before send. An empty body approves the draft as-is. On successful send, the server scrubs body columns and records the provider-assigned Message-ID.
// @Tags         HITL
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Message ID" example(msg_abc123)
// @Param        request body ApprovePendingMessageRequest false "Optional field overrides"
// @Success      200 {object} ApprovePendingMessageResponse
// @Failure      400 {string} string "Invalid request or SMTP validation error"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      403 {string} string "Agent domain not verified"
// @Failure      404 {string} string "Message not found or not owned by this user"
// @Failure      409 {string} string "Message is no longer pending approval"
// @Router       /api/v1/messages/{id}/approve [post]
func (a *API) handleApprovePendingMessage(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	messageID := mux.Vars(r)["id"]

	var req approveRequest
	// Empty body is allowed (approve-as-is). Only error if body is present but malformed.
	if r.ContentLength > 0 {
		if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}
	edits, err := req.toEdit()
	if err != nil {
		http.Error(w, "invalid attachments", http.StatusBadRequest)
		return
	}

	// Pre-flight load: verify ownership + pending status + resolve the owning agent.
	preview, err := a.store.GetOutboundMessageForUser(r.Context(), messageID, user.ID)
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	if preview.Status != identity.MessageStatusPendingApproval {
		http.Error(w, "message is not pending approval", http.StatusConflict)
		return
	}

	agent, err := a.store.GetAgentByID(r.Context(), preview.AgentID)
	if err != nil {
		log.Printf("[api] approve: get agent %s: %v", preview.AgentID, err)
		http.Error(w, "agent lookup failed", http.StatusInternalServerError)
		return
	}
	if !agent.DomainVerified {
		http.Error(w, "agent domain must be verified before sending", http.StatusForbidden)
		return
	}

	sent, err := a.store.ApproveAndSend(r.Context(), messageID, user.ID, edits,
		func(locked *identity.Message) (identity.SendResult, error) {
			sendReq, err := buildSendRequestFromMessage(locked)
			if err != nil {
				return identity.SendResult{}, err
			}
			attachReferencesChain(r.Context(), a.store, agent.ID, &sendReq)
			// Self-sends bypass the SMTP relay — outbound.Sender would
			// strip the agent's own address from the recipient list and
			// error "no valid recipients". Loopback writes the inbound
			// row directly and reports method=loopback on the now-sent
			// outbound row, matching the non-HITL self-send shape.
			if isSelfSend(sendReq, agent.EmailAddress()) {
				return a.selfSendApprovalDelivery(r.Context(), agent, sendReq)
			}
			result, err := a.sender.Send(agent, sendReq)
			if err != nil {
				return identity.SendResult{}, err
			}
			return identity.SendResult{
				ProviderMessageID: result.MessageID,
				Method:            result.Method,
				To:                result.To,
				CC:                result.CC,
				BCC:               result.BCC,
			}, nil
		})
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrMessageNotFound):
			http.Error(w, "message not found", http.StatusNotFound)
		case errors.Is(err, identity.ErrNotPendingApproval):
			http.Error(w, "message is not pending approval", http.StatusConflict)
		default:
			var ve *outbound.ValidationError
			if errors.As(err, &ve) {
				http.Error(w, ve.Error(), http.StatusBadRequest)
				return
			}
			log.Printf("[api] approve-send failed: agent=%s msg=%s err=%v", agent.ID, messageID, err)
			http.Error(w, "send failed", http.StatusInternalServerError)
		}
		return
	}

	// Record usage only after the message actually leaves the gateway.
	if _, err := a.usage.RecordAndCheck(r.Context(), user.ID, agent.ID, agent.Domain, "outbound"); err != nil {
		log.Printf("[api] usage recording error: %v", err)
	}

	slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
	log.Printf("[mail:%s] dir=outbound type=%s status=sent from=%s to=%v slug=%s subject=%q edited=%v approved=user:%s",
		sent.ID, sent.Type, agent.EmailAddress(), sent.ToRecipients, slug, sent.Subject, sent.Edited, user.ID)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{
		"status":     sent.Status,
		"message_id": sent.ID,
		"provider_message_id": sent.ProviderMessageID,
		"method":     sent.Method,
		"edited":     sent.Edited,
	})
}

// attachReferencesChain rebuilds the References chain on a HITL-approved
// SendRequest by looking up the parent inbound's raw message via
// email_message_id. Required because the pending-outbound row only
// persists the parent's Message-ID, not its raw message — without
// re-deriving here, HITL-protected replies fall back to single-id
// References and fork Gmail threads in multi-party conversations.
//
// No-op when ReplyToMessageID is empty (a fresh /send, not a reply) or
// when the parent inbound has expired / been deleted. In the expiry
// case we silently fall back to legacy single-id behavior — better
// than refusing the send. Callers must keep ReplyToMessageID populated
// regardless; only References is filled in here.
func attachReferencesChain(ctx context.Context, store hitlInboundLookup, agentID string, req *outbound.SendRequest) {
	if req.ReplyToMessageID == "" {
		return
	}
	inbound, err := store.GetInboundByEmailMessageID(ctx, agentID, req.ReplyToMessageID)
	if err != nil || inbound == nil {
		return
	}
	req.References = outbound.BuildReferencesChain(inbound.RawMessage, req.ReplyToMessageID)
}

// hitlInboundLookup is the narrow store contract attachReferencesChain
// needs. Defined as an interface so tests can stub it without spinning
// up the full identity store.
type hitlInboundLookup interface {
	GetInboundByEmailMessageID(ctx context.Context, agentID, emailMessageID string) (*identity.Message, error)
}

// buildSendRequestFromMessage reconstructs a SendRequest from a stored
// pending-approval message (with any reviewer edits already applied).
func buildSendRequestFromMessage(m *identity.Message) (outbound.SendRequest, error) {
	var attachments []outbound.Attachment
	if len(m.AttachmentsJSON) > 0 {
		if err := json.Unmarshal(m.AttachmentsJSON, &attachments); err != nil {
			return outbound.SendRequest{}, err
		}
	}
	return outbound.SendRequest{
		To:               m.ToRecipients,
		CC:               m.CC,
		BCC:              m.BCC,
		Subject:          m.Subject,
		Body:             m.BodyText,
		HTMLBody:         m.BodyHTML,
		ReplyToMessageID: m.EmailMessageID,
		ConversationID:   m.ConversationID,
		Attachments:      attachments,
	}, nil
}

// rejectRequest is the JSON body accepted by the reject endpoint.
type rejectRequest struct {
	Reason string `json:"reason,omitempty"`
}

// handleRejectPendingMessage serves POST /api/v1/messages/{id}/reject.
// Transitions the row to 'rejected' and scrubs body columns. No SES call.
// @Summary      Reject a held outbound message
// @Description  Transitions a pending-approval message to `rejected`; the message is never sent. Body columns are scrubbed. An optional reason is stored for audit purposes. Returns 409 if the message has already been sent, rejected, or expired.
// @Tags         HITL
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Message ID" example(msg_abc123)
// @Param        request body RejectPendingMessageRequest false "Optional rejection reason"
// @Success      200 {object} RejectPendingMessageResponse
// @Failure      400 {string} string "Invalid request body"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      404 {string} string "Message not found or not owned by this user"
// @Failure      409 {string} string "Message is no longer pending approval"
// @Router       /api/v1/messages/{id}/reject [post]
func (a *API) handleRejectPendingMessage(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	messageID := mux.Vars(r)["id"]

	var req rejectRequest
	if r.ContentLength > 0 {
		if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	rejected, err := a.store.RejectPending(r.Context(), messageID, user.ID, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrMessageNotFound):
			http.Error(w, "message not found", http.StatusNotFound)
		case errors.Is(err, identity.ErrNotPendingApproval):
			http.Error(w, "message is not pending approval", http.StatusConflict)
		default:
			log.Printf("[api] reject: %v", err)
			http.Error(w, "failed to reject message", http.StatusInternalServerError)
		}
		return
	}

	log.Printf("[mail:%s] dir=outbound type=%s status=rejected agent=%s rejected_by=user:%s reason=%q",
		rejected.ID, rejected.Type, rejected.AgentID, user.ID, req.Reason)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{
		"status":           rejected.Status,
		"message_id":       rejected.ID,
		"rejection_reason": rejected.RejectionReason,
	})
}

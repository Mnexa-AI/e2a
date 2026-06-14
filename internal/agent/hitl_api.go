package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/gorilla/mux"
)

// verifyURLAgentEmail enforces the agent-scoped path's URL invariant.
// When the route includes an {email} segment (e.g.
// /api/v1/agents/{email}/messages/{id}/approve), we require that
// segment to match the loaded message's owning agent. A mismatch is
// returned as 404 — the same shape the flat-path handler uses for
// "not yours" — so the alias can't be used to enumerate other users'
// messages or to misroute approve/reject by typing the wrong email.
//
// Returns true when the URL has no {email} segment (legacy path) OR
// the segment matches msg's owning agent. Returns false and writes a
// 404 response on mismatch; the caller must `return` immediately.
func (a *API) verifyURLAgentEmail(ctx context.Context, w http.ResponseWriter, r *http.Request, agentID string) bool {
	urlEmail := mux.Vars(r)["email"]
	if urlEmail == "" {
		return true
	}
	agent, err := a.store.GetAgentByID(ctx, agentID)
	if err != nil {
		// If we can't load the agent the message ownership check above
		// already passed, so the row exists. A miss here is a real
		// internal error, but we return 404 for caller-facing symmetry
		// with the legacy flat path.
		log.Printf("[api] verifyURLAgentEmail: get agent %s: %v", agentID, err)
		http.Error(w, "message not found", http.StatusNotFound)
		return false
	}
	if agent.Email != urlEmail {
		http.Error(w, "message not found", http.StatusNotFound)
		return false
	}
	return true
}

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
	// InboundContext is attached when this is a reply or a forward
	// (i.e. when email_message_id is non-empty and the parent inbound
	// row is still in retention). Used by the review panel to render
	// the "In reply to" / "Forwarding" pane with SPF/DKIM/DMARC
	// provenance — without it, reviewers can't see what the original
	// message looked like or whether it was auth-validated.
	InboundContext *pendingMessageInboundContext `json:"inbound,omitempty"`
}

// pendingMessageInboundContext is the inlined inbound-row preview
// attached to a reply's pending detail. Body is intentionally elided
// here (raw_message is RFC 5322 bytes; we'd need to parse to extract
// the text part) — the panel currently renders the headers only.
type pendingMessageInboundContext struct {
	Sender      string            `json:"sender"`
	Subject     string            `json:"subject"`
	CreatedAt   string            `json:"created_at"`
	AuthHeaders map[string]string `json:"auth_headers,omitempty"`
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

// messageToDetail converts a stored outbound message into the public
// detail shape. When inbound is non-nil it is attached as
// InboundContext so the reviewer-facing Provenance pane has the parent
// message's sender, subject, timestamp, and SPF/DKIM/DMARC headers.
// Callers pass nil for non-reply messages and for replies whose parent
// inbound has aged out of retention (or was never persisted).
func messageToDetail(m identity.Message, inbound *identity.Message) pendingMessageDetail {
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
	if inbound != nil {
		d.InboundContext = &pendingMessageInboundContext{
			Sender:      inbound.Sender,
			Subject:     inbound.Subject,
			CreatedAt:   inbound.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			AuthHeaders: inbound.AuthHeaders,
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
// @Router       /api/v1/pending [get]
func (a *API) handleListMessages(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		// Emit the RFC 6750 §3 WWW-Authenticate challenge (and the
		// §3.1 OAuth error params when the failing credential was an
		// ate2a_ bearer) so Bearer clients know how to retry.
		a.writeAuthError(w, r, err)
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
	if !a.verifyURLAgentEmail(r.Context(), w, r, msg.AgentID) {
		return
	}

	// For replies, attach the parent inbound's headers so the review
	// panel can render the Provenance pane + quoted preview. The lookup
	// is best-effort: messages.expires_at scopes the inbound to the
	// retention window, and the store returns sql.ErrNoRows when the
	// parent has aged out. We swallow the error in that case — the UI
	// falls back to "No inbound context" cleanly. agent_id scoping in
	// GetInboundByEmailMessageID guards against cross-agent reach.
	var inbound *identity.Message
	if msg.EmailMessageID != "" {
		if got, err := a.store.GetInboundByEmailMessageID(r.Context(), msg.AgentID, msg.EmailMessageID); err == nil {
			inbound = got
		}
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, messageToDetail(*msg, inbound))
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

// handleApprovePendingMessage serves POST /api/v1/agents/{email}/messages/{id}/approve.
// Applies any overrides from the request body, sends via SES, and
// transitions the row to 'sent' with body scrubbed.
// @Summary      Approve a held outbound message
// @Description  Sends a pending-approval message via the upstream SMTP relay and transitions it to `sent`. The request body is optional — passing any subset of subject / body_text / body_html / to / cc / bcc / attachments overrides the stored draft before send. An empty body approves the draft as-is. On successful send, the server scrubs body columns and records the provider-assigned Message-ID.
// @Tags         HITL
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        email path string true "Owning agent email" example(bot@agents.e2a.dev)
// @Param        id path string true "Message ID" example(msg_abc123)
// @Param        request body ApprovePendingMessageRequest false "Optional field overrides"
// @Success      200 {object} ApprovePendingMessageResponse
// @Failure      400 {string} string "Invalid request or SMTP validation error"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      403 {string} string "Agent domain not verified"
// @Failure      404 {string} string "Message not found, not owned by this user, or {email} doesn't match the message's owning agent"
// @Failure      409 {string} string "Message is no longer pending approval, or another request with this Idempotency-Key is in progress"
// @Failure      422 {string} string "Idempotency-Key reused with a different request body"
// @Param        Idempotency-Key header string false "Caller-generated unique key (recommend UUIDv4). Approve fires a real outbound send (SES); on retry with the same key + same body the server replays the original response instead of double-sending. A different body returns 422."
// @Router       /api/v1/agents/{email}/messages/{id}/approve [post]
// ApproveOverrides are the optional reviewer edits applied on approve
// (exported alias of the internal body type so the v1 httpapi layer can build
// them).
type ApproveOverrides = approveRequest

// ApprovePendingCore is the HTTP-free core of the HITL approve→send: it
// verifies the held message (ownership-scoped + pending + optional
// expected-agent-email match + domain-verified), then runs ApproveAndSend
// with the shared send callback (self-send loopback / SES), records usage,
// and publishes the approved event. Both the legacy handler and the v1 layer
// call it. expectedAgentEmail (when non-empty) must equal the message's
// agent's email — mirrors the legacy verifyURLAgentEmail URL guard.
// On a nil-error return the SES send has committed; the idempotency key must
// be Completed (cached), never Released.
func (a *API) ApprovePendingCore(ctx context.Context, userID, messageID, expectedAgentEmail string, ovr ApproveOverrides) (*identity.Message, *OutboundError) {
	edits, err := ovr.toEdit()
	if err != nil {
		return nil, &OutboundError{http.StatusBadRequest, "invalid_request", "invalid attachments"}
	}
	preview, err := a.store.GetOutboundMessageForUser(ctx, messageID, userID)
	if err != nil {
		return nil, &OutboundError{http.StatusNotFound, "not_found", "message not found"}
	}
	if preview.Status != identity.MessageStatusPendingApproval {
		return nil, &OutboundError{http.StatusConflict, "message_not_pending", "message is not pending approval"}
	}
	agent, err := a.store.GetAgentByID(ctx, preview.AgentID)
	if err != nil {
		log.Printf("[api] approve: get agent %s: %v", preview.AgentID, err)
		return nil, &OutboundError{http.StatusInternalServerError, "internal_error", "agent lookup failed"}
	}
	if expectedAgentEmail != "" && agent.Email != expectedAgentEmail {
		return nil, &OutboundError{http.StatusNotFound, "not_found", "message not found"}
	}
	if !agent.DomainVerified {
		return nil, &OutboundError{http.StatusForbidden, "domain_not_verified", "agent domain must be verified before sending"}
	}

	sent, err := a.store.ApproveAndSend(ctx, messageID, userID, edits,
		func(locked *identity.Message) (identity.SendResult, error) {
			sendReq, err := buildSendRequestFromMessage(locked)
			if err != nil {
				return identity.SendResult{}, err
			}
			attachReferencesChain(ctx, a.store, agent.ID, &sendReq)
			if isSelfSend(sendReq, agent.EmailAddress()) {
				return a.selfSendApprovalDelivery(ctx, agent, sendReq)
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
			return nil, &OutboundError{http.StatusNotFound, "not_found", "message not found"}
		case errors.Is(err, identity.ErrNotPendingApproval):
			return nil, &OutboundError{http.StatusConflict, "message_not_pending", "message is not pending approval"}
		default:
			var ve *outbound.ValidationError
			if errors.As(err, &ve) {
				return nil, &OutboundError{http.StatusBadRequest, "invalid_request", ve.Error()}
			}
			log.Printf("[api] approve-send failed: agent=%s msg=%s err=%v", agent.ID, messageID, err)
			return nil, &OutboundError{http.StatusInternalServerError, "internal_error", "send failed"}
		}
	}

	if _, err := a.usage.RecordAndCheck(ctx, userID, agent.ID, agent.Domain, "outbound"); err != nil {
		log.Printf("[api] usage recording error: %v", err)
	}
	slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
	log.Printf("[mail:%s] dir=outbound type=%s status=sent from=%s to=%v slug=%s subject=%q edited=%v approved=user:%s",
		sent.ID, sent.Type, agent.EmailAddress(), sent.ToRecipients, slug, sent.Subject, sent.Edited, userID)
	a.publishApproved(ctx, a.buildApprovedEvent(agent, sent, userID), sent)
	return sent, nil
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
//
// ReplyToMessageID is only copied through for type="reply". Forwards
// also persist email_message_id (so the review panel can render the
// "what's being forwarded" pane via InboundContext), but a forward must
// ship as a new thread — copying email_message_id into ReplyToMessageID
// would emit In-Reply-To/References on the outbound and stitch the
// forward into the original thread.
func buildSendRequestFromMessage(m *identity.Message) (outbound.SendRequest, error) {
	var attachments []outbound.Attachment
	if len(m.AttachmentsJSON) > 0 {
		if err := json.Unmarshal(m.AttachmentsJSON, &attachments); err != nil {
			return outbound.SendRequest{}, err
		}
	}
	replyToMessageID := ""
	if m.Type == "reply" {
		replyToMessageID = m.EmailMessageID
	}
	return outbound.SendRequest{
		To:               m.ToRecipients,
		CC:               m.CC,
		BCC:              m.BCC,
		Subject:          m.Subject,
		Body:             m.BodyText,
		HTMLBody:         m.BodyHTML,
		ReplyToMessageID: replyToMessageID,
		ConversationID:   m.ConversationID,
		Attachments:      attachments,
	}, nil
}

// RejectPendingCore is the HTTP-free core of HITL reject: optional
// expected-agent-email match (mirrors the legacy URL guard), then
// RejectPending + publish. Shared by the legacy handler and the v1 layer.
func (a *API) RejectPendingCore(ctx context.Context, userID, messageID, expectedAgentEmail, reason string) (*identity.Message, *OutboundError) {
	if expectedAgentEmail != "" {
		preview, err := a.store.GetOutboundMessageForUser(ctx, messageID, userID)
		if err != nil {
			return nil, &OutboundError{http.StatusNotFound, "not_found", "message not found"}
		}
		agent, err := a.store.GetAgentByID(ctx, preview.AgentID)
		if err != nil || agent.Email != expectedAgentEmail {
			return nil, &OutboundError{http.StatusNotFound, "not_found", "message not found"}
		}
	}
	rejected, err := a.store.RejectPending(ctx, messageID, userID, reason)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrMessageNotFound):
			return nil, &OutboundError{http.StatusNotFound, "not_found", "message not found"}
		case errors.Is(err, identity.ErrNotPendingApproval):
			return nil, &OutboundError{http.StatusConflict, "message_not_pending", "message is not pending approval"}
		default:
			log.Printf("[api] reject: %v", err)
			return nil, &OutboundError{http.StatusInternalServerError, "internal_error", "failed to reject message"}
		}
	}
	log.Printf("[mail:%s] dir=outbound type=%s status=rejected agent=%s rejected_by=user:%s reason=%q",
		rejected.ID, rejected.Type, rejected.AgentID, userID, reason)
	a.publishRejected(ctx, a.buildRejectedEvent(userID, rejected, reason), rejected.ID)
	return rejected, nil
}

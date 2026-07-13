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
)

// approveRequest is the JSON body accepted by the approve endpoint. Every
// field is optional; any field present overrides the stored value before
// the message is sent. Using pointer types distinguishes "field not
// provided" (nil) from "explicitly empty" (non-nil pointer to zero value).
// The body field names match send/reply: the wire names are `text` and
// `html` (Go fields stay BodyText/BodyHTML).
type approveRequest struct {
	Subject     *string                `json:"subject,omitempty"`
	BodyText    *string                `json:"text,omitempty"`
	BodyHTML    *string                `json:"html,omitempty"`
	To          *[]string              `json:"to,omitempty" nullable:"false"`
	CC          *[]string              `json:"cc,omitempty" nullable:"false"`
	BCC         *[]string              `json:"bcc,omitempty" nullable:"false"`
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
// On a nil-error return the synchronous send or async enqueue has committed;
// the idempotency key must be Completed (cached), never Released. In async mode
// idemCompleteTx is invoked inside the approve-and-enqueue transaction.
func (a *API) ApprovePendingCore(ctx context.Context, userID, messageID, expectedAgentEmail string, ovr ApproveOverrides, idemCompleteTx ApproveIdemCompleter) (*identity.Message, *OutboundError) {
	edits, err := ovr.toEdit()
	if err != nil {
		return nil, &OutboundError{http.StatusBadRequest, "invalid_request", "invalid attachments"}
	}
	preview, err := a.store.GetOutboundMessageForUser(ctx, messageID, userID)
	if err != nil {
		return nil, &OutboundError{http.StatusNotFound, "not_found", "message not found"}
	}
	if preview.Status != identity.MessageStatusPendingReview {
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

	// Async mode: transition the hold to review_approved + delivery_status='accepted'
	// and enqueue an outbound_send job; the SendWorker performs the SMTP submit +
	// email.sent/failed + metering. The reviewer gets "accepted" back (the send is
	// durably queued). Self-sends fall through to the sync loopback path below.
	if a.outboundEnq != nil {
		sent, handled, aerr := a.approveOutboundAsync(ctx, agent, messageID, userID, preview, edits, idemCompleteTx)
		if aerr != nil {
			return nil, approveAsyncError(agent.ID, messageID, aerr)
		}
		if handled {
			slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
			log.Printf("[mail:%s] dir=outbound type=%s status=%s from=%s to=%v slug=%s subject=%q edited=%v approved=user:%s delivery=async",
				sent.ID, sent.Type, sent.Status, agent.EmailAddress(), sent.ToRecipients, slug, sent.Subject, sent.Edited, userID)
			a.publishApproved(ctx, a.buildApprovedEvent(agent, sent, userID), sent)
			// No metering here — the SendWorker meters on MarkSent.
			return sent, nil
		}
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
				Raw:               result.Raw,
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
	log.Printf("[mail:%s] dir=outbound type=%s status=%s from=%s to=%v slug=%s subject=%q edited=%v approved=user:%s",
		sent.ID, sent.Type, sent.Status, agent.EmailAddress(), sent.ToRecipients, slug, sent.Subject, sent.Edited, userID)
	a.publishApproved(ctx, a.buildApprovedEvent(agent, sent, userID), sent)
	return sent, nil
}

// approveOutboundAsync composes the (edited) draft and, for a non-self-send,
// transitions the hold to status='sent' + delivery_status='accepted' and enqueues an
// outbound_send job in one tx (via store.ApproveAndAccept). Returns (sent, true, nil)
// when queued; (nil, false, nil) when the message is a self-send (the caller uses the
// sync loopback path); (nil, false, err) on failure. Shared by the dashboard-approve
// (ApprovePendingCore) and magic-link (magicApprove) paths. draft is the loaded
// pending_review row; edits is empty for the magic-link path.
//
// The hold status becomes 'sent' — the same terminal the SYNC human approve
// (ApproveAndSend) uses: outbound has no separate "approved" hold status; the human
// resolution is recorded via reviewed_by_user_id + the review_approved event, and
// delivery_status ('accepted' → 'sent'/'failed') tracks the async send. (The TTL
// sweep uses review_expired_approved instead — see hitlworker.autoApproveAsync.)
func (a *API) approveOutboundAsync(ctx context.Context, agent *identity.AgentIdentity, messageID, userID string, draft *identity.Message, edits identity.PendingApprovalEdit, idemCompleteTx ApproveIdemCompleter) (*identity.Message, bool, error) {
	editedByReviewer := edits.Apply(draft)
	sendReq, err := buildSendRequestFromMessage(draft)
	if err != nil {
		return nil, false, err
	}
	attachReferencesChain(ctx, a.store, agent.ID, &sendReq)
	if isSelfSend(sendReq, agent.EmailAddress()) {
		return nil, false, nil // self-send — caller uses the sync loopback path
	}
	comp, err := a.sender.ComposeForAccept(agent, sendReq)
	if err != nil {
		return nil, false, err
	}
	acc := identity.AcceptedSend{
		To: comp.To, CC: comp.CC, BCC: comp.BCC, Subject: sendReq.Subject,
		Method: comp.Method, EnvelopeFrom: comp.EnvelopeFrom, SentAs: comp.SentAs, Raw: comp.Raw,
	}
	sent, err := a.store.ApproveAndAccept(ctx, messageID, userID, identity.MessageStatusSent, editedByReviewer, acc, a.outboundEnq.EnqueueSendTx, idemCompleteTx)
	if err != nil {
		return nil, false, err
	}
	return sent, true, nil
}

// approveAsyncError maps an approveOutboundAsync failure to an OutboundError,
// matching the sync approve path's status codes.
func approveAsyncError(agentID, messageID string, err error) *OutboundError {
	switch {
	case errors.Is(err, identity.ErrNotPendingApproval):
		return &OutboundError{http.StatusConflict, "message_not_pending", "message is not pending approval"}
	case errors.Is(err, identity.ErrMessageNotFound):
		return &OutboundError{http.StatusNotFound, "not_found", "message not found"}
	default:
		var ve *outbound.ValidationError
		if errors.As(err, &ve) {
			return &OutboundError{http.StatusBadRequest, "invalid_request", ve.Error()}
		}
		log.Printf("[api] approve-accept failed: agent=%s msg=%s err=%v", agentID, messageID, err)
		return &OutboundError{http.StatusInternalServerError, "internal_error", "send failed"}
	}
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
func attachReferencesChain(ctx context.Context, store hitlParentLookup, agentID string, req *outbound.SendRequest) {
	if req.ReplyToMessageID == "" {
		return
	}
	// Direction-agnostic: a held reply's parent may be an outbound the agent
	// sent (reply-to-own-message), not only a received inbound.
	parent, err := store.GetMessageByEmailMessageID(ctx, agentID, req.ReplyToMessageID)
	if err != nil || parent == nil {
		return
	}
	req.References = outbound.BuildReferencesChain(parent.RawMessage, req.ReplyToMessageID)
}

// hitlParentLookup is the narrow store contract attachReferencesChain
// needs. Defined as an interface so tests can stub it without spinning
// up the full identity store.
type hitlParentLookup interface {
	GetMessageByEmailMessageID(ctx context.Context, agentID, emailMessageID string) (*identity.Message, error)
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
	// A caller-supplied Reply-To override is persisted on the held row's reply_to
	// column (single element) so it survives the recompose at approval time —
	// without this the override would silently vanish on every reviewed send.
	var replyTo string
	if len(m.ReplyTo) > 0 {
		replyTo = m.ReplyTo[0]
	}
	return outbound.SendRequest{
		To:               m.ToRecipients,
		CC:               m.CC,
		BCC:              m.BCC,
		Subject:          m.Subject,
		Body:             m.BodyText,
		HTMLBody:         m.BodyHTML,
		ReplyTo:          replyTo,
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
	log.Printf("[mail:%s] dir=outbound type=%s status=%s agent=%s rejected_by=user:%s reason=%q",
		rejected.ID, rejected.Type, rejected.Status, rejected.AgentID, userID, reason)
	a.publishRejected(ctx, a.buildRejectedEvent(userID, rejected, reason), rejected.ID)
	return rejected, nil
}

// ApproveInboundReviewCore releases a held INBOUND message to its agent's inbox
// (status pending_review → review_approved, now readable) and fires
// email.review_approved — the inbound analogue of ApprovePendingCore. There is no
// SES send and no draft edit: an inbound hold is a screening decision, not a draft.
//
// msg is the dispatch view the account-scoped handler already resolved via
// GetReviewMessage (ownership + tenant isolation proven there); userID is the
// reviewing owner. The store transition is a compare-and-set on
// status='pending_review' AND agent_id, so a concurrent reviewer or the TTL sweep
// racing this call results in ErrNotPendingReview (409), never a double release.
func (a *API) ApproveInboundReviewCore(ctx context.Context, userID string, msg *identity.ReviewMessageMeta) *OutboundError {
	if err := a.store.ApproveInboundReview(ctx, msg.ID, msg.AgentID, userID); err != nil {
		if errors.Is(err, identity.ErrNotPendingReview) {
			return &OutboundError{http.StatusConflict, "message_not_pending", "message is not pending review"}
		}
		log.Printf("[api] approve inbound review %s: %v", msg.ID, err)
		return &OutboundError{http.StatusInternalServerError, "internal_error", "failed to approve message"}
	}
	log.Printf("[mail:%s] dir=inbound type=%s status=%s agent=%s approved_by=user:%s",
		msg.ID, msg.Type, identity.MessageStatusReviewApproved, msg.AgentID, userID)
	// Post-side-effect publish (the release row is already committed): reuse the
	// approved-event plumbing (deterministic id off the message id → MTA/retry
	// idempotent). A minimal *identity.Message carries the id publishApproved needs.
	a.publishApproved(ctx, a.buildInboundReleasedEvent(msg, a.reviewOwnerID(ctx, msg.AgentID, userID), userID), &identity.Message{ID: msg.ID, AgentID: msg.AgentID})
	return nil
}

// reviewOwnerID returns the agent's owner user id — the webhook routing key for
// an inbound review event. It equals the reviewer today (the endpoint is
// account-scoped + ownership-checked), so on any lookup failure we fall back to
// the reviewer id rather than fail the already-committed release.
func (a *API) reviewOwnerID(ctx context.Context, agentID, fallbackUserID string) string {
	ag, err := a.store.GetAgentByID(ctx, agentID)
	if err != nil || ag == nil {
		log.Printf("[api] review owner lookup for agent %s: %v (routing on reviewer)", agentID, err)
		return fallbackUserID
	}
	return ag.UserID
}

// RejectInboundReviewCore drops a held INBOUND message (status pending_review →
// review_rejected; it stays hidden from the agent, raw payload retained for
// forensics) and fires email.review_rejected. Compare-and-set semantics as in
// ApproveInboundReviewCore.
func (a *API) RejectInboundReviewCore(ctx context.Context, userID, reason string, msg *identity.ReviewMessageMeta) *OutboundError {
	if err := a.store.RejectInboundReview(ctx, msg.ID, msg.AgentID, userID, reason); err != nil {
		if errors.Is(err, identity.ErrNotPendingReview) {
			return &OutboundError{http.StatusConflict, "message_not_pending", "message is not pending review"}
		}
		log.Printf("[api] reject inbound review %s: %v", msg.ID, err)
		return &OutboundError{http.StatusInternalServerError, "internal_error", "failed to reject message"}
	}
	log.Printf("[mail:%s] dir=inbound type=%s status=%s agent=%s rejected_by=user:%s reason=%q",
		msg.ID, msg.Type, identity.MessageStatusReviewRejected, msg.AgentID, userID, reason)
	a.publishRejected(ctx, a.buildInboundRejectedEvent(msg, a.reviewOwnerID(ctx, msg.AgentID, userID), userID, reason), msg.ID)
	return nil
}

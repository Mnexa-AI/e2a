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
// The body field names match send/reply: the wire names are `body` and
// `html_body` (Go fields stay BodyText/BodyHTML).
type approveRequest struct {
	Subject     *string                `json:"subject,omitempty"`
	BodyText    *string                `json:"body,omitempty"`
	BodyHTML    *string                `json:"html_body,omitempty"`
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

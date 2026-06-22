package hitlworker

import (
	"context"
	"log"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// Event emission for TTL auto-resolution.
//
// When the sweep resolves a hold, it fires the SAME review-resolution event a
// human reviewer would (email.review_approved / email.review_rejected), so a
// subscriber driving a review queue sees a consistent signal regardless of who
// resolved it. This is load-bearing for inbound APPROVE: email.received was
// suppressed while the message was held and is not re-fired on release, so
// without this event a TTL-released inbound message is invisible to subscribers.
//
// The event shapes mirror the user-driven builders in internal/agent
// (build{Approved,Rejected,InboundReleased,InboundRejected}Event) — duplicated
// rather than imported because hitlworker is a low-level package that must not
// take an upward dependency on internal/agent (see attachReferencesChain). Two
// differences from the human path: there is no human reviewer, so
// reviewed_by_user_id is omitted and `auto_resolved: true` is set; and the
// routing key (Event.UserID) is the agent OWNER (the sweep is system-scoped).

// publish fires e through the legacy publisher, detached from the sweep's
// context so a cancelled sweep doesn't drop an already-committed resolution's
// notification. No-op when no publisher is wired. The deterministic id (set by
// the caller) makes a re-swept row's event idempotent.
func (w *Worker) publish(e webhookpub.Event) {
	if w.publisher == nil {
		return
	}
	go w.publisher.Publish(context.Background(), e)
}

// emitInboundResolved fires the review-resolution event for an inbound hold the
// sweep just released (approved=true) or dropped (approved=false). meta is the
// pre-transition dispatch view; ownerUserID is the routing key.
func (w *Worker) emitInboundResolved(meta *identity.ReviewMessageMeta, ownerUserID string, approved bool, reason string) {
	if w.publisher == nil {
		return
	}
	var (
		typ  string
		data map[string]interface{}
	)
	if approved {
		typ = webhookpub.EventEmailReviewApproved
		data = map[string]interface{}{
			"message_id":    meta.ID,
			"direction":     "inbound",
			"from":          meta.Sender,
			"subject":       meta.Subject,
			"type":          meta.Type,
			"auto_resolved": true,
		}
	} else {
		typ = webhookpub.EventEmailReviewRejected
		data = map[string]interface{}{
			"message_id":       meta.ID,
			"direction":        "inbound",
			"type":             meta.Type,
			"rejection_reason": reason,
			"auto_resolved":    true,
		}
	}
	e := webhookpub.NewEvent(typ, ownerUserID, data)
	e.AgentID = meta.AgentID
	e.MessageID = meta.ID
	e.ID = webhookpub.DeterministicEventID(meta.ID, typ)
	w.publish(e)
}

// emitOutboundApproved fires email.review_approved for an outbound hold the sweep
// auto-sent. sent is the now-sent message row; agent supplies the owner + from.
func (w *Worker) emitOutboundApproved(agent *identity.AgentIdentity, sent *identity.Message) {
	if w.publisher == nil {
		return
	}
	data := map[string]interface{}{
		"message_id":          sent.ID,
		"direction":           "outbound",
		"provider_message_id": sent.ProviderMessageID,
		"method":              sent.Method,
		"from":                agent.EmailAddress(),
		"to":                  sent.ToRecipients,
		"subject":             sent.Subject,
		"type":                sent.Type,
		"edited":              false,
		"auto_resolved":       true,
	}
	e := webhookpub.NewEvent(webhookpub.EventEmailReviewApproved, agent.UserID, data)
	e.AgentID = agent.ID
	e.MessageID = sent.ID
	e.ID = webhookpub.DeterministicEventID(sent.ID, webhookpub.EventEmailReviewApproved)
	w.publish(e)
}

// emitOutboundRejected fires email.review_rejected for an outbound hold the sweep
// dropped on expiry. It resolves the owner from the rejected row's agent.
func (w *Worker) emitOutboundRejected(ctx context.Context, rejected *identity.Message, reason string) {
	if w.publisher == nil {
		return
	}
	ownerUserID := ""
	if ag, err := w.store.GetAgentByID(ctx, rejected.AgentID); err == nil && ag != nil {
		ownerUserID = ag.UserID
	} else {
		log.Printf("[hitl-worker] reject-event owner lookup for agent %s: %v (event suppressed)", rejected.AgentID, err)
		return // without a routing key the event can't be delivered; don't emit an orphan
	}
	data := map[string]interface{}{
		"message_id":       rejected.ID,
		"direction":        "outbound",
		"type":             rejected.Type,
		"rejection_reason": reason,
		"auto_resolved":    true,
	}
	e := webhookpub.NewEvent(webhookpub.EventEmailReviewRejected, ownerUserID, data)
	e.AgentID = rejected.AgentID
	e.MessageID = rejected.ID
	e.ID = webhookpub.DeterministicEventID(rejected.ID, webhookpub.EventEmailReviewRejected)
	w.publish(e)
}

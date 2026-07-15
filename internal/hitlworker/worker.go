// Package hitlworker runs the periodic sweep that finalizes pending_review
// holds whose TTL has elapsed. Outbound holds become sent (auto-approved) or
// review_expired_rejected; inbound holds become review_expired_approved
// (released to the agent) or review_expired_rejected — per the owning agent's
// hitl_expiration_action column. Body columns are scrubbed in both cases.
package hitlworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/loopback"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// OutboundEnqueuer inserts an outbound_send job (QueueOutbound) in the caller's
// transaction. Satisfied by *outboundsend.Jobs. The sweep hands an approved
// outbound send to the queue-first pipeline — transitioning
// the hold to review_expired_approved + delivery_status='accepted' and enqueuing —
// instead of blocking on Sender.Send. Self-sends never use it (they loopback).
type OutboundEnqueuer interface {
	EnqueueSendTx(ctx context.Context, tx pgx.Tx, messageID string) (int64, error)
}

// DefaultBatchSize caps how many rows one sweep will try to finalize. The
// partial index on (approval_expires_at) WHERE status='pending_review'
// keeps the list query cheap regardless of total table size.
const DefaultBatchSize = 100

// Worker runs the TTL sweep. Construct with New; its RunOnce is driven on a
// schedule by the River maintenance periodic (see maintenance.go).
type Worker struct {
	store      *identity.Store
	sender     *outbound.Sender
	usage      usage.UsageTracker
	fromDomain string
	batchSize  int
	// publisher fires the review-resolution webhook when the sweep auto-resolves
	// a hold, so a TTL-resolved hold notifies subscribers exactly like a
	// human-resolved one (the user-driven path emits review_approved/rejected
	// from internal/agent). Optional — nil leaves the sweep silent (legacy
	// behavior). Wired via SetPublisher.
	publisher webhookpub.Publisher
	// outboundEnq routes an approved external send onto QueueOutbound. Main always
	// wires it; a nil value fails closed and leaves the hold pending. Self-sends use
	// the local loopback path.
	outboundEnq OutboundEnqueuer
}

// SetPublisher wires the webhook publisher used to emit review-resolution events
// on TTL auto-resolution. Without it the sweep transitions rows silently.
func (w *Worker) SetPublisher(p webhookpub.Publisher) { w.publisher = p }

// SetOutboundEnqueuer wires the mandatory outbound send enqueuer. Two-phase
// wiring: pass the *outboundsend.Jobs pointer; its shared River client is injected
// later via the jobs client's SetEnqueuer.
func (w *Worker) SetOutboundEnqueuer(e OutboundEnqueuer) { w.outboundEnq = e }

// New constructs a Worker. fromDomain is the deployment's outbound
// from-domain (cfg.OutboundSMTP.FromDomain) — used by the self-send
// loopback branch to stamp the synthetic Message-ID / Received headers
// the same way internal/agent does on the user-driven approve path.
// Pass "" if the deployment has no outbound relay configured; the
// loopback path falls back to "e2a.local" for the host portion.
func New(store *identity.Store, sender *outbound.Sender, usageTracker usage.UsageTracker, fromDomain string) *Worker {
	return &Worker{
		store:      store,
		sender:     sender,
		usage:      usageTracker,
		fromDomain: fromDomain,
		batchSize:  DefaultBatchSize,
	}
}

// RunOnce performs a single sweep of both queues (outbound holds, then inbound
// review holds). This is the sweep body the River maintenance periodic drives on
// a schedule (see maintenance.go); it's also called directly by tests for
// deterministic behavior. Returns nil — both sweeps log and swallow their own
// per-row/query errors internally (a transient DB blip should not spin River's
// retry machinery); the error return satisfies the Sweeper interface.
func (w *Worker) RunOnce(ctx context.Context) error {
	w.sweep(ctx)
	w.sweepReviews(ctx)
	return nil
}

// sweepReviews auto-resolves expired INBOUND review holds. Both directions share
// the pending_review status (unified — design 2026-06-22); ListExpiredReviews is
// direction='inbound'-scoped, so this never touches an outbound hold (those are the
// `sweep` path, where approve = send). Inbound: approve = release the held message
// to the agent's inbox (it becomes visible), reject = drop it. The compare-and-set
// status guard in the store methods makes concurrent/duplicate sweeps safe.
func (w *Worker) sweepReviews(ctx context.Context) {
	candidates, err := w.store.ListExpiredReviews(ctx, w.batchSize)
	if err != nil {
		log.Printf("[hitl-worker] list expired reviews: %v", err)
		return
	}
	for _, c := range candidates {
		// Capture the dispatch view + owner BEFORE the transition: a reject
		// makes the row terminal/hidden, and the resolution event mirrors the
		// human path's payload (sender/subject) and routes on the owner. A
		// lookup failure means we still resolve the hold but skip the event
		// (better than stranding the row).
		meta, mErr := w.store.GetReviewMessage(ctx, c.MessageID, c.AgentID)
		ownerUserID := ""
		if ag, aErr := w.store.GetAgentByID(ctx, c.AgentID); aErr == nil && ag != nil {
			ownerUserID = ag.UserID
		}
		canEmit := mErr == nil && meta != nil && ownerUserID != ""

		if c.ExpirationAction == identity.HITLExpirationApprove {
			if err := w.store.ExpireApproveReview(ctx, c.MessageID); err != nil {
				if err != identity.ErrNotPendingReview {
					log.Printf("[hitl-worker] expire-approve review %s: %v", c.MessageID, err)
				}
				continue // not transitioned by us → don't emit
			}
			if canEmit {
				w.emitInboundResolved(meta, ownerUserID, true, "")
			}
		} else {
			if err := w.store.ExpireRejectReview(ctx, c.MessageID, "ttl_expired"); err != nil {
				if err != identity.ErrNotPendingReview {
					log.Printf("[hitl-worker] expire-reject review %s: %v", c.MessageID, err)
				}
				continue
			}
			if canEmit {
				w.emitInboundResolved(meta, ownerUserID, false, "ttl_expired")
			}
		}
	}
}

func (w *Worker) sweep(ctx context.Context) {
	candidates, err := w.store.ListExpiredPending(ctx, w.batchSize)
	if err != nil {
		log.Printf("[hitl-worker] list expired: %v", err)
		return
	}
	for _, c := range candidates {
		w.processOne(ctx, c)
	}
}

func (w *Worker) processOne(ctx context.Context, c identity.ExpirationCandidate) {
	if c.ExpirationAction == identity.HITLExpirationApprove {
		w.autoApprove(ctx, c)
		return
	}
	w.autoReject(ctx, c.MessageID, "ttl_expired")
}

func (w *Worker) autoApprove(ctx context.Context, c identity.ExpirationCandidate) {
	agent, err := w.store.GetAgentByID(ctx, c.AgentID)
	if err != nil {
		// Not-found means the agent was hard-deleted or moved to the trash
		// between the sweep's candidate list and this load (GetAgentByID
		// excludes trashed agents — migration 063). SKIP, don't terminally
		// reject: a trashed inbox's holds must come back intact on restore
		// (RestoreAgent shifts their approval TTLs), and a hard-deleted
		// agent's rows are gone anyway.
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[hitl-worker] auto-approve %s: agent %s gone or trashed — skipping", c.MessageID, c.AgentID)
			return
		}
		log.Printf("[hitl-worker] auto-approve %s: agent lookup failed: %v", c.MessageID, err)
		w.autoReject(ctx, c.MessageID, fmt.Sprintf("auto-approve failed: agent lookup: %v", err))
		return
	}
	if !agent.DomainVerified {
		log.Printf("[hitl-worker] auto-approve %s: agent %s not verified", c.MessageID, agent.ID)
		w.autoReject(ctx, c.MessageID, "auto-approve failed: agent domain not verified")
		return
	}

	if w.outboundEnq == nil {
		log.Printf("[hitl-worker] auto-approve %s: outbound delivery queue unavailable", c.MessageID)
		return
	}
	// Hand external delivery to QueueOutbound. false means this is a self-send,
	// which uses the local loopback path below.
	if w.autoApproveAsync(ctx, agent, c) {
		return
	}
	w.autoApproveLoopback(ctx, agent, c)
}

// autoApproveAsync transitions the hold to review_expired_approved +
// delivery_status='accepted' and enqueues an outbound_send job; the SendWorker does
// the actual submit + email.sent/failed + metering. Returns false (handled nothing)
// ONLY when the message is a self-send — the caller then uses the local loopback
// path. Any other outcome (queued, already-resolved, transient failure left for the
// next cycle, or a permanent-draft reject) returns true.
func (w *Worker) autoApproveAsync(ctx context.Context, agent *identity.AgentIdentity, c identity.ExpirationCandidate) bool {
	msg, err := w.store.LoadOutboundDraft(ctx, c.MessageID)
	if err != nil {
		if errors.Is(err, identity.ErrMessageNotFound) {
			return true // gone — no-op
		}
		log.Printf("[hitl-worker] auto-approve %s: load draft: %v", c.MessageID, err)
		return true // transient — leave pending_review for the next cycle
	}
	if msg.Status != identity.MessageStatusPendingReview {
		return true // resolved by a human/other worker
	}
	req, err := sendRequestFromStoredMessage(msg)
	if err != nil {
		w.autoReject(ctx, c.MessageID, fmt.Sprintf("auto-approve failed: rebuild request: %v", err))
		return true
	}
	w.attachReferencesChain(ctx, agent.ID, &req)
	if loopback.IsSelfSend(req, agent.EmailAddress()) {
		return false // self-send — fall through to the local loopback path
	}
	comp, err := w.sender.ComposeForAccept(agent, req)
	if err != nil {
		// Compose failures are deterministic (bad addresses / no visible
		// recipients) — a retry can't fix them, so reject the draft.
		w.autoReject(ctx, c.MessageID, fmt.Sprintf("auto-approve failed: compose: %v", err))
		return true
	}
	acc := identity.AcceptedSend{
		To: comp.To, CC: comp.CC, BCC: comp.BCC, Subject: req.Subject,
		Method: comp.Method, EnvelopeFrom: comp.EnvelopeFrom, SentAs: comp.SentAs, Raw: comp.Raw,
	}
	sent, err := w.store.ApproveAndAccept(ctx, c.MessageID, "", identity.MessageStatusReviewExpiredApproved, false, acc, w.outboundEnq.EnqueueSendTx, nil)
	if err != nil {
		if errors.Is(err, identity.ErrNotPendingApproval) {
			return true // resolved between load and transition
		}
		// Transient tx/enqueue failure: leave the row pending_review for the next
		// cycle. Do NOT autoReject — no send happened, so this is not a "stuck" send.
		log.Printf("[hitl-worker] auto-approve %s: accept+enqueue: %v", c.MessageID, err)
		return true
	}
	log.Printf("[mail:%s] dir=outbound type=%s status=%s agent=%s to=%v auto_approved=true delivery=async",
		sent.ID, sent.Type, sent.Status, agent.ID, sent.ToRecipients)
	// review_approved fires now (hold resolved to approved); the delivery outcome
	// arrives later via email.sent/email.failed from the SendWorker. No metering
	// here — the SendWorker meters on MarkSent.
	w.emitOutboundApproved(agent, sent)
	return true
}

func (w *Worker) autoApproveLoopback(ctx context.Context, agent *identity.AgentIdentity, c identity.ExpirationCandidate) {
	sent, err := w.store.ExpireApproveAndSend(ctx, c.MessageID,
		func(msg *identity.Message) (identity.SendResult, error) {
			req, err := sendRequestFromStoredMessage(msg)
			if err != nil {
				return identity.SendResult{}, err
			}
			w.attachReferencesChain(ctx, agent.ID, &req)
			// Self-sends bypass the SMTP relay — outbound.Sender would
			// strip the agent's own address from the recipient list and
			// error "no valid recipients", which the worker would then
			// interpret as a send failure and auto-REJECT the row,
			// silently inverting the operator-configured
			// hitl_expiration_action="approve" policy. Loopback writes
			// the inbound row directly and reports method=loopback on
			// the now-sent outbound row, matching the user-driven
			// approve paths in internal/agent/hitl_api.go and
			// internal/agent/hitl_magic_api.go.
			if !loopback.IsSelfSend(req, agent.EmailAddress()) {
				return identity.SendResult{}, errors.New("external outbound approval must be queued")
			}
			return loopback.DeliverInbound(ctx, w.store, agent, req, w.fromDomain)
		})
	if err != nil {
		// ErrNotPendingApproval means another worker (or a human) handled
		// the row between list-and-lock. Treat as a no-op.
		if err == identity.ErrNotPendingApproval {
			return
		}
		// ErrSendInProgress means another worker is mid-send for this
		// row (send_attempts is 'attempting' and not yet stale). Don't
		// auto-reject — that would invert the operator-configured
		// expiration_action="approve" policy by terminally rejecting a
		// message that may have actually been sent. Skip silently; the
		// next poll either sees status='sent' (the in-flight worker
		// committed) or the row goes stale (10min window) and another
		// worker takes over.
		if err == identity.ErrSendInProgress {
			return
		}
		log.Printf("[hitl-worker] auto-approve %s: send failed: %v", c.MessageID, err)
		w.autoReject(ctx, c.MessageID, fmt.Sprintf("auto-approve send failed: %v", err))
		return
	}
	// External sends are metered by the outbound worker after provider success.
	// Loopback is terminal here, so preserve the same post-delivery accounting.
	if _, err := w.usage.RecordAndCheck(ctx, agent.UserID, agent.ID, agent.Domain, "outbound"); err != nil {
		log.Printf("[hitl-worker] usage recording error: %v", err)
	}

	log.Printf("[mail:%s] dir=outbound type=%s status=%s agent=%s to=%v auto_sent=true",
		sent.ID, sent.Type, sent.Status, agent.ID, sent.ToRecipients)
	// Mirror the user-driven approve: fire email.review_approved (the send
	// already happened; this is the post-side-effect notification).
	w.emitOutboundApproved(agent, sent)
}

func (w *Worker) autoReject(ctx context.Context, messageID, reason string) {
	rejected, err := w.store.ExpireReject(ctx, messageID, reason)
	if err != nil {
		if err == identity.ErrNotPendingApproval {
			return
		}
		// This is the worst-case path: auto-approve already failed (or
		// the policy was reject), and now the rejection write fails too.
		// The row is stuck in pending_review until an operator
		// intervenes. Tag the log line so monitors / alerting can match
		// on it specifically — distinct from routine "[hitl-worker]"
		// noise.
		log.Printf("[hitl-stuck] message=%s reason=%q reject_error=%v ACTION=needs_manual_intervention",
			messageID, reason, err)
		return
	}
	log.Printf("[mail:%s] dir=outbound type=%s status=%s agent=%s reason=%q auto_rejected=true",
		rejected.ID, rejected.Type, rejected.Status, rejected.AgentID, reason)
	w.emitOutboundRejected(ctx, rejected, reason)
}

// attachReferencesChain rebuilds the References chain on a HITL-approved
// SendRequest by looking up the parent message's raw message via
// email_message_id. The lookup is direction-agnostic: a held reply's parent
// may be an outbound the agent sent (reply-to-own-message), not only a
// received inbound. Duplicates the equivalent helper in internal/agent for the
// same reason sendRequestFromStoredMessage does — keep this low-level package
// free of upward imports. See that helper's docstring for the full rationale.
func (w *Worker) attachReferencesChain(ctx context.Context, agentID string, req *outbound.SendRequest) {
	if req.ReplyToMessageID == "" {
		return
	}
	parent, err := w.store.GetMessageByEmailMessageID(ctx, agentID, req.ReplyToMessageID)
	if err != nil || parent == nil {
		return
	}
	req.References = outbound.BuildReferencesChain(parent.RawMessage, req.ReplyToMessageID)
}

// sendRequestFromStoredMessage reconstructs a SendRequest from a locked
// pending-approval row. Duplicates the equivalent helper in internal/agent
// to avoid an upward import from this low-level package.
func sendRequestFromStoredMessage(m *identity.Message) (outbound.SendRequest, error) {
	var attachments []outbound.Attachment
	if len(m.AttachmentsJSON) > 0 {
		if err := json.Unmarshal(m.AttachmentsJSON, &attachments); err != nil {
			return outbound.SendRequest{}, err
		}
	}
	// Carry a caller-supplied Reply-To override (persisted single-element on the
	// held row's reply_to column) through the TTL auto-approve recompose, so an
	// expired-but-approved send keeps the same Reply-To a human approval would.
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
		ReplyToMessageID: m.EmailMessageID,
		ConversationID:   m.ConversationID,
		Attachments:      attachments,
	}, nil
}

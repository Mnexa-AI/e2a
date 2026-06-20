// Package hitlworker runs the periodic sweep that finalizes pending_approval
// messages whose TTL has elapsed. Each row becomes either expired_approved
// (sent as-is) or expired_rejected based on the owning agent's
// hitl_expiration_action column. Body columns are scrubbed in both cases.
package hitlworker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/loopback"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/usage"
)

// DefaultInterval is the sweep cadence. One minute matches the design doc
// target; short enough that TTL boundaries are honored within a minute,
// long enough to avoid hot-looping the DB when there's nothing to do.
const DefaultInterval = 60 * time.Second

// DefaultBatchSize caps how many rows one sweep will try to finalize. The
// partial index on (approval_expires_at) WHERE status='pending_approval'
// keeps the list query cheap regardless of total table size.
const DefaultBatchSize = 100

// Worker runs the TTL sweep. Construct with New, start with Run.
type Worker struct {
	store      *identity.Store
	sender     *outbound.Sender
	usage      usage.UsageTracker
	fromDomain string
	interval   time.Duration
	batchSize  int
}

// New constructs a Worker. fromDomain is the deployment's outbound
// from-domain (cfg.OutboundSMTP.FromDomain) — used by the self-send
// loopback branch to stamp the synthetic Message-ID / Received headers
// the same way internal/agent does on the user-driven approve path.
// Pass "" if the deployment has no outbound relay configured; the
// loopback path falls back to "e2a.local" for the host portion.
func New(store *identity.Store, sender *outbound.Sender, usage usage.UsageTracker, fromDomain string) *Worker {
	return &Worker{
		store:      store,
		sender:     sender,
		usage:      usage,
		fromDomain: fromDomain,
		interval:   DefaultInterval,
		batchSize:  DefaultBatchSize,
	}
}

// Run drives the periodic sweep until ctx is cancelled. Returns ctx.Err()
// on shutdown. Safe to run from its own goroutine; multiple instances
// across processes are fine because the per-row store operations use
// row-level locking + status guards.
func (w *Worker) Run(ctx context.Context) error {
	// One immediate sweep so a process restart doesn't leave a full
	// interval's worth of already-expired rows sitting.
	w.sweep(ctx)
	w.sweepReviews(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.sweep(ctx)
			w.sweepReviews(ctx)
		}
	}
}

// RunOnce performs a single sweep of both queues. Exposed for tests that want
// deterministic behavior without setting up a ticker.
func (w *Worker) RunOnce(ctx context.Context) {
	w.sweep(ctx)
	w.sweepReviews(ctx)
}

// sweepReviews auto-resolves expired inbound review holds (Slice 4b). Unlike the
// outbound pending_approval path, there is no send to perform: approve = release
// the held message to the agent's inbox (it becomes visible), reject = drop it.
// The compare-and-set status guard in the store methods makes concurrent/duplicate
// sweeps safe.
func (w *Worker) sweepReviews(ctx context.Context) {
	candidates, err := w.store.ListExpiredReviews(ctx, w.batchSize)
	if err != nil {
		log.Printf("[hitl-worker] list expired reviews: %v", err)
		return
	}
	for _, c := range candidates {
		if c.ExpirationAction == identity.HITLExpirationApprove {
			if err := w.store.ExpireApproveReview(ctx, c.MessageID); err != nil && err != identity.ErrNotPendingReview {
				log.Printf("[hitl-worker] expire-approve review %s: %v", c.MessageID, err)
			}
		} else {
			if err := w.store.ExpireRejectReview(ctx, c.MessageID, "ttl_expired"); err != nil && err != identity.ErrNotPendingReview {
				log.Printf("[hitl-worker] expire-reject review %s: %v", c.MessageID, err)
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
		log.Printf("[hitl-worker] auto-approve %s: agent lookup failed: %v", c.MessageID, err)
		w.autoReject(ctx, c.MessageID, fmt.Sprintf("auto-approve failed: agent lookup: %v", err))
		return
	}
	if !agent.DomainVerified {
		log.Printf("[hitl-worker] auto-approve %s: agent %s not verified", c.MessageID, agent.ID)
		w.autoReject(ctx, c.MessageID, "auto-approve failed: agent domain not verified")
		return
	}

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
			if loopback.IsSelfSend(req, agent.EmailAddress()) {
				return loopback.DeliverInbound(ctx, w.store, agent, req, w.fromDomain)
			}
			result, err := w.sender.Send(agent, req)
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

	// Record usage only after the send actually succeeded.
	if _, err := w.usage.RecordAndCheck(ctx, agent.UserID, agent.ID, agent.Domain, "outbound"); err != nil {
		log.Printf("[hitl-worker] usage recording error: %v", err)
	}
	log.Printf("[mail:%s] dir=outbound type=%s status=expired_approved agent=%s to=%v auto_sent=true",
		sent.ID, sent.Type, agent.ID, sent.ToRecipients)
}

func (w *Worker) autoReject(ctx context.Context, messageID, reason string) {
	rejected, err := w.store.ExpireReject(ctx, messageID, reason)
	if err != nil {
		if err == identity.ErrNotPendingApproval {
			return
		}
		// This is the worst-case path: auto-approve already failed (or
		// the policy was reject), and now the rejection write fails too.
		// The row is stuck in pending_approval until an operator
		// intervenes. Tag the log line so monitors / alerting can match
		// on it specifically — distinct from routine "[hitl-worker]"
		// noise.
		log.Printf("[hitl-stuck] message=%s reason=%q reject_error=%v ACTION=needs_manual_intervention",
			messageID, reason, err)
		return
	}
	log.Printf("[mail:%s] dir=outbound type=%s status=expired_rejected agent=%s reason=%q auto_rejected=true",
		rejected.ID, rejected.Type, rejected.AgentID, reason)
}

// attachReferencesChain rebuilds the References chain on a HITL-approved
// SendRequest by looking up the parent inbound's raw message via
// email_message_id. Duplicates the equivalent helper in internal/agent
// for the same reason sendRequestFromStoredMessage does — keep this
// low-level package free of upward imports. See that helper's docstring
// for the full rationale.
func (w *Worker) attachReferencesChain(ctx context.Context, agentID string, req *outbound.SendRequest) {
	if req.ReplyToMessageID == "" {
		return
	}
	inbound, err := w.store.GetInboundByEmailMessageID(ctx, agentID, req.ReplyToMessageID)
	if err != nil || inbound == nil {
		return
	}
	req.References = outbound.BuildReferencesChain(inbound.RawMessage, req.ReplyToMessageID)
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

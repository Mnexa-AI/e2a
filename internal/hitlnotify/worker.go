// worker.go is Layer 3 of the durable HITL approval-notification pipeline
// (docs/design/hitl-notify-river.md): the River execution stage that takes a
// pending_review message, recomposes the reviewer's approve/reject email, and
// submits it ONCE off the request path. It mirrors internal/outboundsend — a
// River Worker on the shared `notify` queue, with River owning claim/retry/rescue.
//
// At-least-once from the 202 response: the hitl_notify job is enqueued in the same
// tx as the pending_review row (the hold accept-tx) before the API answers, so an
// accepted hold's notification is never lost. The worker's notified_at stamp
// (written only AFTER a successful send) makes a crash-after-send re-drive a no-op;
// loss is impossible, a rare duplicate "please review" email is benign.
package hitlnotify

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/tokencanopy/e2a/internal/identity"
)

// notifyRetryBackoffs is the per-attempt delay for a failed notification send.
// Shorter than the outbound sender's: a review email is worthless once the hold
// passes its TTL (the worker short-circuits on approval_expires_at), so there is
// no point in the multi-hour tail an at-least-once customer send needs.
var notifyRetryBackoffs = []time.Duration{
	15 * time.Second,
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
}

// MaxNotifyAttempts caps the retry tail before River discards the job.
const MaxNotifyAttempts = 6

// notifyOutageSnooze defers a job when the relay is unreachable, without burning
// an attempt (mirrors the outbound outage snooze).
const notifyOutageSnooze = 5 * time.Minute

// HITLNotifyArgs drives one approval-notification job. Args carry only the message
// id; the worker re-reads the message + agent (the source of truth) each attempt,
// so the job row stays tiny and always reflects the current hold state.
type HITLNotifyArgs struct {
	MessageID string `json:"message_id"`
}

func (HITLNotifyArgs) Kind() string { return "hitl_notify" }

// DeliverOutcome is the classified result of one notification send. Permanent and
// Outage split the retry decision exactly as the outbound worker's does, using the
// shared internal/outbound SMTP classifiers.
type DeliverOutcome struct {
	Err       error
	Permanent bool // 5xx / validation — no retry
	Outage    bool // relay unreachable — snooze without spending an attempt
}

// Deliverer composes and sends the approval email for one held message. Implemented
// by *Notifier (compose + SMTPRelay.SendOnce + classify).
type Deliverer interface {
	Deliver(ctx context.Context, pn *identity.PendingNotify) DeliverOutcome
}

// Store is the message surface the worker + reconciler need. Implemented over
// internal/identity (*identity.Store).
type Store interface {
	// LoadPendingNotify returns the held message + owning agent, or (nil, nil) when
	// there is nothing to notify about (message or agent gone) — a no-op.
	LoadPendingNotify(ctx context.Context, messageID string) (*identity.PendingNotify, error)
	// MarkMessageNotified stamps notified_at after a successful send (the dedup marker).
	MarkMessageNotified(ctx context.Context, messageID string) error
	// StampNotifyJobIDTx records the job id on a reconciled row (accept-tx + reconciler).
	StampNotifyJobIDTx(ctx context.Context, tx pgx.Tx, messageID string, jobID int64) error
}

// NotifyWorker sends the approval notification for one pending_review message.
// Mirrors outboundsend.SendWorker.
type NotifyWorker struct {
	river.WorkerDefaults[HITLNotifyArgs]
	store     Store
	deliverer Deliverer
}

func NewNotifyWorker(store Store, deliverer Deliverer) *NotifyWorker {
	return &NotifyWorker{store: store, deliverer: deliverer}
}

// NextRetry overrides River's default backoff with the decided notify envelope.
func (w *NotifyWorker) NextRetry(job *river.Job[HITLNotifyArgs]) time.Time {
	i := job.Attempt
	if i < 0 || i >= len(notifyRetryBackoffs) {
		return time.Time{} // fall back to River's default at the tail
	}
	return time.Now().Add(notifyRetryBackoffs[i])
}

// Work intentionally has no Timeout() override — a single SMTP submit of the approval
// notification fits River's 60s default JobTimeout.
func (w *NotifyWorker) Work(ctx context.Context, job *river.Job[HITLNotifyArgs]) error {
	pn, err := w.store.LoadPendingNotify(ctx, job.Args.MessageID)
	if err != nil {
		return err // DB error — retryable
	}
	if pn == nil {
		return nil // message or owning agent gone — nothing to notify
	}
	msg := pn.Message

	// Pointlessness / idempotency guards — each a no-op (return nil):
	if msg.Status != identity.MessageStatusPendingReview {
		return nil // resolved (approved/rejected/expired) before we notified
	}
	if msg.ApprovalExpiresAt != nil && msg.ApprovalExpiresAt.Before(time.Now()) {
		return nil // hold already past TTL — a review email is now useless
	}
	if pn.Notified {
		return nil // a prior attempt already sent it (crash-after-send re-drive)
	}
	if pn.Agent != nil && pn.Agent.SuppressNotifications {
		return nil // agent opted out of approval notifications
	}

	out := w.deliverer.Deliver(ctx, pn)
	if out.Err == nil {
		if merr := w.store.MarkMessageNotified(ctx, msg.ID); merr != nil {
			// The email is already out; only the dedup marker failed to persist. Do
			// NOT return an error — a retry would re-send. Completing the job leaves
			// notify_job_id set, so the reconciler won't re-enqueue it either.
			log.Printf("[hitl-notify] sent %s but mark-notified failed: %v", msg.ID, merr)
		}
		return nil
	}
	if out.Permanent {
		// e.g. the owner address is rejected 5xx. Unavoidable — the hold still
		// finalizes on its TTL. Cancel (no retry) rather than churn the tail.
		log.Printf("[hitl-notify] permanent send failure for %s (no retry): %v", msg.ID, out.Err)
		return river.JobCancel(out.Err)
	}
	if out.Outage {
		// Relay unreachable. Snooze without burning an attempt. If the hold has
		// since passed its TTL, the next attempt's expiry guard above short-circuits
		// to a no-op — no need to special-case it here.
		return river.JobSnooze(notifyOutageSnooze)
	}
	// Transient (relay throttle, owner lookup blip, compose error): let River
	// reschedule per NextRetry until MaxNotifyAttempts, then discard.
	return fmt.Errorf("hitl notify attempt %d failed: %w", job.Attempt, out.Err)
}

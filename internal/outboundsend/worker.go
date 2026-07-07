// Package outboundsend is Layer 3 of the outbound pipeline
// (docs/design/async-message-pipeline.md): the River execution stage that submits
// an accepted message to the upstream provider (SES) and records the terminal
// outcome. It mirrors internal/webhookdelivery — a River Worker on the shared
// `outbound` queue, with River owning claim / retry / rescue.
//
// Delivery is at-least-once: River re-drives a crashed job, so the provider may
// receive a duplicate if the SMTP submit is accepted but the worker crashes before
// marking the message sent. This is the irreducible residual. NOTE: the
// X-E2A-Message-ID header + SNS-based reconciliation of that duplicate (and the
// terminal-failure guard / delivery.Merge exception) are POST-GA — not implemented
// here; see async-message-pipeline.md §7 "out of scope".
//
// One SMTP attempt per job attempt — River owns the multi-attempt envelope via
// NextRetry, so Work() stays short (the deliverer does a single submit, not an
// internal retry loop). See the design's "claim + rescue, not a lease" note.
package outboundsend

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/delivery"
)

// sendRetryBackoffs is the per-attempt delay schedule for a failed outbound send —
// the decided envelope (design §4). River drives it via NextRetry; indexed by
// attempt. Provider-outage errors snooze instead of counting an attempt (slice D).
var sendRetryBackoffs = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
	4 * time.Hour,
}

// MaxSendAttempts caps app/permanent-error retries (bounded 4xx/unknown tail).
const MaxSendAttempts = 6

// outageSnoozeInterval is how long a provider-outage job snoozes between probes.
// JobSnooze does NOT count an attempt, so an outage defers rather than exhausting
// MaxSendAttempts (design §8 circuit breaker).
const outageSnoozeInterval = 5 * time.Minute

// sendRetryHorizon bounds the outage-tolerant tail: past this age (from accept) an
// outage-snoozing job stops deferring and is declared terminally failed. 72h matches
// the industry MTA retry horizon (and the webhook deliverer's envelope) — long enough
// to ride out a multi-hour regional SES incident, not forever.
const sendRetryHorizon = 72 * time.Hour

// OutboundSendArgs drives one outbound send. Args carry only the message id; the
// worker re-reads the messages row (the source of truth) each attempt.
type OutboundSendArgs struct {
	MessageID string `json:"message_id"`
}

func (OutboundSendArgs) Kind() string { return "outbound_send" }

// SendJob is the send payload the worker loads from the messages row (Store.LoadForSend).
type SendJob struct {
	MessageID    string
	Status       string // messages.delivery_status
	EnvelopeFrom string
	Recipients   []string
	RawMessage   []byte // composed MIME
	SentAs       string // From identity decided at accept ("own_address"|"relay")
	// AcceptedAt is messages.created_at — the outage tail's clock, so a job that has
	// been snoozing through an outage past sendRetryHorizon can be terminated.
	AcceptedAt time.Time
}

// pastRetryHorizon reports whether the accept is older than the outage-tolerant
// retry horizon. Zero AcceptedAt (unknown) is treated as not-past so an outage keeps
// deferring rather than being falsely terminated on a missing timestamp.
func (j *SendJob) pastRetryHorizon() bool {
	return !j.AcceptedAt.IsZero() && time.Since(j.AcceptedAt) > sendRetryHorizon
}

// alreadyDone reports whether the message has already been submitted to the
// provider — its delivery_status has moved past the pre-send states
// (`accepted`/`sending`) to `sent` or any later/terminal value — and so must not
// be re-sent. This is the idempotent-re-drive gate (a crash re-drive of a `sent`
// row is a no-op). Note delivery.Status.Terminal() is NOT the right check: it
// reports the final SNS outcome (delivered/bounced/complained/failed) and treats
// `sent` as non-terminal, but `sent` still means "already submitted, don't resend".
func (j *SendJob) alreadyDone() bool {
	s := delivery.Status(j.Status)
	return s != delivery.StatusAccepted && s != delivery.StatusSending
}

// DeliverOutcome is the result of one SMTP submit attempt.
type DeliverOutcome struct {
	ProviderMessageID string
	SentAs            string
	Err               error
	// Permanent marks a non-retryable failure (validation / permanent 5xx): the
	// worker fails the message terminally instead of retrying.
	Permanent bool
	// Outage marks a provider-connection failure (relay unreachable/misconfigured):
	// the worker snoozes without burning an attempt (design §8), up to the retry
	// horizon. Mutually exclusive with Permanent in practice.
	Outage bool
}

// Deliverer performs a SINGLE SMTP submit — River owns re-attempts. Implemented in
// the binary over internal/outbound's single-attempt path (slice B).
type Deliverer interface {
	Deliver(ctx context.Context, j *SendJob) DeliverOutcome
}

// Store is the messages-store surface the worker needs. Implemented over
// internal/identity in the binary (slice C). MarkSent/MarkFailed each own their own
// transaction and emit email.sent / email.failed via the webhook outbox in it.
type Store interface {
	// LoadForSend returns the send payload, or (nil, nil) if the message is gone
	// (agent-delete cascade / TTL) — the worker treats that as a no-op.
	LoadForSend(ctx context.Context, messageID string) (*SendJob, error)
	// MarkSent sets delivery_status='sent' + provider_message_id and emits
	// email.sent, in one transaction.
	MarkSent(ctx context.Context, messageID, providerMessageID, sentAs string) error
	// MarkFailed sets delivery_status='failed' + detail and emits email.failed, in
	// one transaction.
	MarkFailed(ctx context.Context, messageID string, attempt int, detail string) error
}

// SendWorker submits an accepted message and records the terminal outcome. Mirrors
// webhookdelivery.DeliverWorker.
type SendWorker struct {
	river.WorkerDefaults[OutboundSendArgs]
	store     Store
	deliverer Deliverer
}

func NewSendWorker(store Store, deliverer Deliverer) *SendWorker {
	return &SendWorker{store: store, deliverer: deliverer}
}

// NextRetry overrides River's default backoff with the decided send envelope.
func (w *SendWorker) NextRetry(job *river.Job[OutboundSendArgs]) time.Time {
	i := job.Attempt
	if i < 0 || i >= len(sendRetryBackoffs) {
		return time.Time{} // fall back to River's default at the tail
	}
	return time.Now().Add(sendRetryBackoffs[i])
}

func (w *SendWorker) Work(ctx context.Context, job *river.Job[OutboundSendArgs]) error {
	j, err := w.store.LoadForSend(ctx, job.Args.MessageID)
	if err != nil {
		return err // DB error — retryable
	}
	if j == nil {
		return nil // message gone (cascade/TTL) — nothing to send
	}
	if j.alreadyDone() {
		return nil // already submitted (sent+) — idempotent re-drive
	}

	out := w.deliverer.Deliver(ctx, j)
	if out.Err == nil {
		// Success — one tx (in the store): mark sent + provider id + email.sent.
		return w.store.MarkSent(ctx, j.MessageID, out.ProviderMessageID, out.SentAs)
	}

	// Permanent failure (validation / permanent 5xx) — terminal now, no retries.
	if out.Permanent {
		w.markFailed(ctx, j.MessageID, job.Attempt, out.Err.Error())
		return river.JobCancel(out.Err)
	}
	// Provider outage (relay unreachable) — snooze WITHOUT burning an attempt so a
	// multi-hour SES incident defers instead of exhausting MaxSendAttempts and
	// mass-firing false email.failed (§8 circuit breaker). Bounded by the retry
	// horizon: once the accept is older than sendRetryHorizon, give up terminally.
	if out.Outage {
		if j.pastRetryHorizon() {
			w.markFailed(ctx, j.MessageID, job.Attempt, out.Err.Error())
			return fmt.Errorf("outbound send failed (provider outage past %s horizon): %w", sendRetryHorizon, out.Err)
		}
		return river.JobSnooze(outageSnoozeInterval)
	}
	// Last attempt — River discards after this, so the terminal 'failed' write is
	// the row's last chance (markFailed retries it).
	if job.Attempt >= MaxSendAttempts {
		w.markFailed(ctx, j.MessageID, job.Attempt, out.Err.Error())
		return fmt.Errorf("outbound send failed (final attempt %d): %w", job.Attempt, out.Err)
	}
	// Retryable — River reschedules per NextRetry.
	return fmt.Errorf("outbound send attempt %d failed: %w", job.Attempt, out.Err)
}

// markFailed writes the terminal 'failed' status, retrying a transient DB error a
// few times so the row's status never desyncs from a discarded River job (mirrors
// webhookdelivery.markFailedReliably).
func (w *SendWorker) markFailed(ctx context.Context, messageID string, attempt int, detail string) {
	var err error
	for i := 0; i < 3; i++ {
		if err = w.store.MarkFailed(ctx, messageID, attempt, detail); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(i+1) * 150 * time.Millisecond):
		}
	}
	log.Printf("[outbound-send] CRITICAL: terminal 'failed' write for %s failed after retries: %v", messageID, err)
}

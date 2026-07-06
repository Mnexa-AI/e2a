// Package webhookdelivery is Layer 3 of the webhook pipeline
// (docs/design/webhook-delivery-river-migration.md): the River execution stage
// that POSTs a webhook_subscriber_deliveries (Layer 2) row to the customer
// endpoint and retries it. It replaces the hand-rolled SKIP-LOCKED claim + retry
// worker (internal/webhook/subscriber_retry.go) with a River Worker on the shared
// `webhook` queue; River owns claim/lease/retry/backoff, and Layer 2 becomes pure
// delivery state written here for the history API.
//
// Delivery is at-least-once (the industry standard — Stripe/Svix/Postmark):
// River re-drives a crashed job, so an endpoint may receive a duplicate if the
// POST succeeds but the worker crashes before writing `delivered`. Consumers
// dedup on the event id in the signed payload.
package webhookdelivery

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

// retryBackoffs replicates internal/webhook/retry.go's schedule exactly (~72h
// over 8 attempts) so customer-facing retry timing is unchanged. Indexed by the
// completed attempt number: after River's attempt N fails, the next retry is
// now+retryBackoffs[N] (matching the legacy nextRetryAt(newAttempts)). The legacy
// path likewise never used index 0 — preserved deliberately, not a new bug. That
// file is deleted with the legacy worker at cutover, so the schedule lives here.
var retryBackoffs = []time.Duration{
	1 * time.Minute, 5 * time.Minute, 15 * time.Minute, 1 * time.Hour,
	4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour,
}

// MaxDeliveryAttempts is River's MaxAttempts for a delivery job — after this many
// failed attempts River discards and the worker marks Layer 2 'failed'.
const MaxDeliveryAttempts = 8

// disabledSnooze is how long a delivery whose webhook is currently disabled is
// rescheduled (no attempt burned) — mirrors the legacy disabledDeferral.
const disabledSnooze = time.Hour

// WebhookReader is the narrow webhook-lookup surface the worker needs.
// *identity.Store satisfies it.
type WebhookReader interface {
	GetWebhookByIDInternal(ctx context.Context, webhookID string) (*identity.Webhook, error)
}

// Deliverer is the POST surface. *webhook.SubscriberDeliverer satisfies it.
type Deliverer interface {
	Deliver(ctx context.Context, url string, body []byte, secret, secretPrev string) webhook.DeliveryOutcome
}

// WebhookDeliverArgs carries only the Layer 2 delivery id — the worker reads the
// payload + webhook from the DB (keeps river_job rows tiny; Layer 2 is source of
// truth).
type WebhookDeliverArgs struct {
	DeliveryID string `json:"delivery_id"`
}

func (WebhookDeliverArgs) Kind() string { return "webhook_deliver" }

// DeliverWorker delivers one Layer 2 row. Mirrors the legacy processOne: re-fetch
// the webhook per attempt (disabled → snooze, deleted → cancel), POST, write
// Layer 2 state. River owns retry/backoff via NextRetry below.
type DeliverWorker struct {
	river.WorkerDefaults[WebhookDeliverArgs]
	subStore  *webhook.SubscriberStore
	deliverer Deliverer
	webhooks  WebhookReader
}

// NewDeliverWorker constructs the worker. Used by the Registrar and by tests.
func NewDeliverWorker(subStore *webhook.SubscriberStore, deliverer Deliverer, webhooks WebhookReader) *DeliverWorker {
	return &DeliverWorker{subStore: subStore, deliverer: deliverer, webhooks: webhooks}
}

// NextRetry overrides River's client-wide policy for webhook jobs only, returning
// the exact 72h envelope. job.Attempt is the just-failed attempt (1-based); River
// discards at MaxAttempts so this is called for attempts 1..MaxDeliveryAttempts-1.
func (w *DeliverWorker) NextRetry(job *river.Job[WebhookDeliverArgs]) time.Time {
	i := job.Attempt
	if i < 0 || i >= len(retryBackoffs) {
		return time.Time{} // fall back to client policy; River discards at MaxAttempts anyway
	}
	return time.Now().Add(retryBackoffs[i])
}

func (w *DeliverWorker) Work(ctx context.Context, job *river.Job[WebhookDeliverArgs]) error {
	d, err := w.subStore.GetSubscriberDeliveryByID(ctx, job.Args.DeliveryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // delivery row gone (webhook deleted, cascaded) — nothing to do
	}
	if err != nil {
		return err // DB error — retryable
	}
	if d.Status == "delivered" {
		return nil // already delivered (re-drive after a crash post-MarkDelivered) — idempotent no-op
	}

	wh, err := w.webhooks.GetWebhookByIDInternal(ctx, d.WebhookID)
	if err != nil {
		// Webhook deleted — terminal. Write the terminal status; if that write
		// fails, return the error (retryable) rather than cancelling with an
		// unmarked row — a JobCancel here would strand the row 'pending' with a
		// dead job that the reconciler (keyed on job_id IS NULL) can't recover.
		if merr := w.markFailedReliably(ctx, d.ID, job.Attempt, "webhook not found", 0); merr != nil {
			return merr
		}
		return river.JobCancel(fmt.Errorf("webhook %s not found: %w", d.WebhookID, err))
	}
	if !wh.Enabled {
		// Disabled — reschedule without burning an attempt (matches legacy defer).
		return river.JobSnooze(disabledSnooze)
	}

	// Honor the previous signing secret only within its rotation grace window.
	prevSecret := wh.SigningSecretPrev
	if wh.SigningSecretPrevExpiresAt != nil && time.Now().After(*wh.SigningSecretPrevExpiresAt) {
		prevSecret = ""
	}

	out := w.deliverer.Deliver(ctx, wh.URL, d.EventPayload, wh.SigningSecret, prevSecret)
	if out.Success {
		return w.subStore.MarkDelivered(ctx, d.ID, out.StatusCode) // nil → River completes the job
	}

	if job.Attempt >= MaxDeliveryAttempts {
		// Last attempt — River discards after this regardless of return value, so the
		// terminal 'failed' write is the row's last chance. markFailedReliably retries
		// it; only a sustained DB outage in this exact window leaves the row stranded
		// (logged CRITICAL — not reachable via any normal River transition).
		if merr := w.markFailedReliably(ctx, d.ID, job.Attempt, out.Error, out.StatusCode); merr != nil {
			log.Printf("[webhook-deliver] CRITICAL: terminal 'failed' write for delivery %s failed after retries (row stays pending, needs manual reconcile): %v", d.ID, merr)
		}
		return fmt.Errorf("webhook delivery failed (final attempt %d, status %d): %s", job.Attempt, out.StatusCode, out.Error)
	}
	// Retryable failure — record the attempt (status stays pending) and return an
	// error so River retries per NextRetry.
	if rerr := w.subStore.RecordSubscriberAttempt(ctx, d.ID, job.Attempt, out.Error, out.StatusCode); rerr != nil {
		return rerr
	}
	return fmt.Errorf("webhook delivery attempt %d failed (status %d): %s", job.Attempt, out.StatusCode, out.Error)
}

// markFailedReliably writes the terminal 'failed' status, retrying a transient DB
// error a few times. The terminal write is what keeps a row's status in sync with
// its (about-to-be-terminal) River job; if it were lost, the row would sit
// 'pending' with a dead job_id — invisible to the reconciler, which keys on
// job_id IS NULL. Bounded, short backoff so the (rare) failure case adds < ~1s.
func (w *DeliverWorker) markFailedReliably(ctx context.Context, id string, attempt int, errMsg string, statusCode int) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = w.subStore.MarkSubscriberFailed(ctx, id, attempt, errMsg, statusCode); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * 150 * time.Millisecond):
		}
	}
	return err
}

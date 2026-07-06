package webhookdelivery

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

// Jobs is the webhook-delivery integration on the shared River client: a
// jobs.Registrar (contributes DeliverWorker) plus the transactional enqueue entry
// point the outbox drain + redelivery API call. The shared client is injected via
// SetEnqueuer after jobs.New builds it (two-phase wiring, same as senderidentity).
type Jobs struct {
	subStore  *webhook.SubscriberStore
	deliverer Deliverer
	webhooks  WebhookReader
	enq       jobs.Enqueuer
}

// NewJobs builds the integration with its dependencies (no client yet).
func NewJobs(subStore *webhook.SubscriberStore, deliverer Deliverer, webhooks WebhookReader) *Jobs {
	return &Jobs{subStore: subStore, deliverer: deliverer, webhooks: webhooks}
}

// SetEnqueuer injects the shared client so EnqueueDeliveryTx can insert jobs.
func (j *Jobs) SetEnqueuer(e jobs.Enqueuer) { j.enq = e }

// RegisterJobs adds the DeliverWorker to the shared client's bundle. Implements
// jobs.Registrar. No periodic jobs (auto-disable folds in with slice 3/4).
func (j *Jobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, NewDeliverWorker(j.subStore, j.deliverer, j.webhooks))
	return nil
}

// CutoverPending is the one-shot migration from the legacy queue (design §8):
// enqueue a River delivery job for every pending Layer 2 row that has no job yet
// (job_id IS NULL). Idempotent — the per-row FOR UPDATE + job_id IS NULL guard
// means a re-run (or a crashed-and-restarted startup) never double-enqueues.
//
// CORRECTNESS-CRITICAL ORDERING: this MUST run after the legacy
// SubscriberRetryWorker is stopped (not wired). If both the legacy worker and the
// enqueued River jobs deliver the same rows, every in-flight event double-delivers.
// Returns the number of rows enqueued.
func (j *Jobs) CutoverPending(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx,
		`SELECT id FROM webhook_subscriber_deliveries WHERE status='pending' AND job_id IS NULL`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, id := range ids {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// Re-check under a row lock: another process (or a prior run) may have
			// enqueued it already. Skip if job_id is now set.
			var jobID *int64
			if err := tx.QueryRow(ctx,
				`SELECT job_id FROM webhook_subscriber_deliveries WHERE id=$1 FOR UPDATE`, id,
			).Scan(&jobID); err != nil {
				return err
			}
			if jobID != nil {
				return nil // already enqueued
			}
			newJobID, err := j.EnqueueDeliveryTx(ctx, tx, id)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE webhook_subscriber_deliveries SET job_id=$2 WHERE id=$1`, id, newJobID)
			if err == nil {
				n++
			}
			return err
		}); err != nil {
			log.Printf("[webhook-delivery] cutover enqueue %s: %v", id, err)
		}
	}
	return n, nil
}

// EnqueueDelivery enqueues a River delivery job for an ALREADY-INSERTED pending
// Layer 2 row, in its own transaction, and stamps job_id. This is for the direct-
// insert API surfaces that bypass the outbox drain — the /test webhook endpoint
// and the redelivery API — which create a subscriber_deliveries row targeting a
// single webhook. Without this, those rows have no River job and (post
// SubscriberRetryWorker deletion) would never deliver. Idempotent per row via the
// job_id IS NULL guard under a row lock.
func (j *Jobs) EnqueueDelivery(ctx context.Context, pool *pgxpool.Pool, deliveryID string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var jobID *int64
		if err := tx.QueryRow(ctx,
			`SELECT job_id FROM webhook_subscriber_deliveries WHERE id=$1 FOR UPDATE`, deliveryID,
		).Scan(&jobID); err != nil {
			return err
		}
		if jobID != nil {
			return nil // already enqueued
		}
		newJobID, err := j.EnqueueDeliveryTx(ctx, tx, deliveryID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE webhook_subscriber_deliveries SET job_id=$2 WHERE id=$1`, deliveryID, newJobID)
		return err
	})
}

// EnqueueDeliveryTx enqueues a delivery job WITHIN the caller's transaction — the
// outbox pattern: the Layer 2 row insert and this job commit together, so a
// delivery record can never exist without a job (or vice versa). The caller only
// calls this when the Layer 2 insert actually inserted a row (dedup ON CONFLICT
// returned an id), so a deduped event enqueues nothing. Returns the river_job id
// for the caller to stamp on the Layer 2 row's job_id.
func (j *Jobs) EnqueueDeliveryTx(ctx context.Context, tx pgx.Tx, deliveryID string) (int64, error) {
	res, err := j.enq.InsertTx(ctx, tx, WebhookDeliverArgs{DeliveryID: deliveryID}, &river.InsertOpts{
		Queue:       jobs.QueueWebhook,
		MaxAttempts: MaxDeliveryAttempts,
	})
	if err != nil {
		return 0, err
	}
	return res.Job.ID, nil
}

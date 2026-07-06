package webhookdelivery

import (
	"context"

	"github.com/jackc/pgx/v5"
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

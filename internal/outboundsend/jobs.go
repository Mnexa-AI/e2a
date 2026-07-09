package outboundsend

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// Jobs is the outbound-send integration on the shared River client: a
// jobs.Registrar (contributes SendWorker) plus the transactional enqueue entry
// point the accept-tx calls. The shared client is injected via SetEnqueuer after
// jobs.New builds it (two-phase wiring, same as webhookdelivery / senderidentity).
type Jobs struct {
	store     Store
	deliverer Deliverer
	enq       jobs.Enqueuer
}

// NewJobs builds the integration with its dependencies (no client yet).
func NewJobs(store Store, deliverer Deliverer) *Jobs {
	return &Jobs{store: store, deliverer: deliverer}
}

// SetEnqueuer injects the shared client so EnqueueSendTx can insert jobs.
func (j *Jobs) SetEnqueuer(e jobs.Enqueuer) { j.enq = e }

// RegisterJobs adds the SendWorker to the shared client's bundle. Implements
// jobs.Registrar.
//
// No live periodic reconciler yet (startup cutover only). The one residual it would close: if a
// job's terminal write (markFailed) fails on all its retries, the worker still
// cancels/discards the River job, leaving the row `accepted` with a stamped-but-dead
// job — which ReconcilePending (keyed on send_job_id IS NULL) does not catch. That
// needs 3+ consecutive DB failures on the terminal write (very rare); a slice-D
// periodic keyed on `accepted AND the job is terminal/absent` closes it.
func (j *Jobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, NewSendWorker(j.store, j.deliverer))
	return nil
}

// ReconcilePending enqueues an outbound_send job for every accepted message that
// has no send job yet (send_job_id IS NULL). Run ONCE at startup as the cutover.
//
// Because the accept-tx is a single transaction (message insert + job enqueue +
// send_job_id stamp all commit together), a committed `accepted` row in steady
// state ALWAYS has send_job_id set — so the send_job_id IS NULL set is normally
// empty. This exists to enqueue (a) any pre-async `accepted` rows at the moment the
// mode is first flipped on, and (b) rows from a future accept-tx variant that
// doesn't stamp atomically. Idempotent: the per-row FOR UPDATE + send_job_id IS NULL
// guard means a re-run (or concurrent replica) never
// double-enqueues. Mirrors webhookdelivery.ReconcilePending. Returns the count
// enqueued.
func (j *Jobs) ReconcilePending(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	return jobs.ReconcilePending(ctx, pool, jobs.ReconcileSpec{
		Table:     "messages",
		JobColumn: "send_job_id",
		Where:     "direction='outbound' AND delivery_status='accepted'",
		LogPrefix: "[outbound-reconcile]",
	}, j.EnqueueSendTx)
}

// EnqueueSendTx enqueues a send job WITHIN the caller's transaction — the outbox
// pattern: the accept-tx's messages-row insert and this job commit together, so an
// `accepted` message can never exist without a send job (or vice versa). The
// accept-tx stamps the returned river_job id on messages.send_job_id so
// the reconciler can find stranded rows (`accepted` with no job). Mirrors
// webhookdelivery.EnqueueDeliveryTx.
func (j *Jobs) EnqueueSendTx(ctx context.Context, tx pgx.Tx, messageID string) (int64, error) {
	res, err := j.enq.InsertTx(ctx, tx, OutboundSendArgs{MessageID: messageID}, &river.InsertOpts{
		Queue:       jobs.QueueOutbound,
		MaxAttempts: MaxSendAttempts,
	})
	if err != nil {
		return 0, err
	}
	return res.Job.ID, nil
}

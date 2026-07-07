package outboundsend

import (
	"context"

	"github.com/jackc/pgx/v5"
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
// jobs.Registrar. (The periodic reconciler is slice D.)
func (j *Jobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, NewSendWorker(j.store, j.deliverer))
	return nil
}

// EnqueueSendTx enqueues a send job WITHIN the caller's transaction — the outbox
// pattern: the accept-tx's messages-row insert and this job commit together, so an
// `accepted` message can never exist without a send job (or vice versa). The
// accept-tx stamps the returned river_job id on messages.send_job_id (slice C) so
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

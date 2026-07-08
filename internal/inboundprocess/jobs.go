package inboundprocess

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// Jobs is the inbound-processing integration on the shared River client: a
// jobs.Registrar (contributes InboundProcessWorker) plus the transactional enqueue
// entry point the SMTP accept-tx calls. The shared client is injected via
// SetEnqueuer after jobs.New builds it (two-phase wiring, same as outboundsend /
// webhookdelivery / senderidentity).
type Jobs struct {
	store     Store
	processor Processor
	enq       jobs.Enqueuer
}

// NewJobs builds the integration with its dependencies (no client yet).
func NewJobs(store Store, processor Processor) *Jobs {
	return &Jobs{store: store, processor: processor}
}

// SetEnqueuer injects the shared client so EnqueueInboundProcessTx can insert jobs.
func (j *Jobs) SetEnqueuer(e jobs.Enqueuer) { j.enq = e }

// RegisterJobs adds the InboundProcessWorker to the shared client's bundle.
// Implements jobs.Registrar. The live periodic reconciler is slice 5.
func (j *Jobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, NewInboundProcessWorker(j.store, j.processor))
	return nil
}

// EnqueueInboundProcessTx inserts the inbound_process job in the caller's accept-tx
// (the same tx as the inbound_intake insert), returning the River job id to stamp on
// the intake row so a committed accepted row always has its job.
func (j *Jobs) EnqueueInboundProcessTx(ctx context.Context, tx pgx.Tx, intakeID string) (int64, error) {
	res, err := j.enq.InsertTx(ctx, tx, InboundProcessArgs{IntakeID: intakeID}, &river.InsertOpts{
		Queue:       jobs.QueueInbound,
		MaxAttempts: MaxInboundAttempts,
	})
	if err != nil {
		return 0, err
	}
	return res.Job.ID, nil
}

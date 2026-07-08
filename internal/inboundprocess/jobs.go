package inboundprocess

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// Jobs is the inbound-processing integration on the shared River client: a
// jobs.Registrar (contributes InboundProcessWorker) plus the transactional enqueue
// entry point the SMTP accept-tx calls. The shared client + the Processor are both
// injected AFTER construction (two-phase wiring) — the client via SetEnqueuer, the
// Processor (the relay Server) via SetProcessor, because the relay is built after
// jobs.New. Jobs itself is the worker's Processor, late-binding to the concrete one.
type Jobs struct {
	store Store
	enq   jobs.Enqueuer

	mu        sync.RWMutex
	processor Processor
}

// NewJobs builds the integration with just its store (no client, no processor yet).
func NewJobs(store Store) *Jobs {
	return &Jobs{store: store}
}

// SetEnqueuer injects the shared client so EnqueueInboundProcessTx can insert jobs.
func (j *Jobs) SetEnqueuer(e jobs.Enqueuer) { j.enq = e }

// SetProcessor injects the concrete Processor (the relay Server), built after
// jobs.New. Guarded so the River worker goroutines read it race-free.
func (j *Jobs) SetProcessor(p Processor) {
	j.mu.Lock()
	j.processor = p
	j.mu.Unlock()
}

// ProcessIntake makes Jobs itself the worker's Processor, delegating to the concrete
// one set via SetProcessor. Until that is wired (the brief startup window before the
// relay is built) it returns a retryable error, so a pending job simply retries
// rather than panicking on a nil processor.
func (j *Jobs) ProcessIntake(ctx context.Context, it *identity.InboundIntake) error {
	j.mu.RLock()
	p := j.processor
	j.mu.RUnlock()
	if p == nil {
		return errors.New("inbound processor not wired yet — retrying")
	}
	return p.ProcessIntake(ctx, it)
}

// RegisterJobs adds the InboundProcessWorker to the shared client's bundle, with Jobs
// as the (late-binding) Processor. Implements jobs.Registrar. The live periodic
// reconciler is slice 5.
func (j *Jobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, NewInboundProcessWorker(j.store, j))
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

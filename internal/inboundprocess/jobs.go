package inboundprocess

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// reconcileBatch bounds one cutover scan of stranded accepted intake rows — mirrors
// outboundsend.reconcileBatch. In steady state the stranded set is ~empty (the
// accept-tx stamps process_job_id atomically); this caps how many rows one pass
// re-drives if a systemic enqueue failure ever left a backlog.
const reconcileBatch = 1000

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

// RegisterJobs adds the InboundProcessWorker (with Jobs as the late-binding
// Processor) + the RetentionWorker, and schedules the retention sweep as a periodic
// on QueueMaintenance. Implements jobs.Registrar.
func (j *Jobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, NewInboundProcessWorker(j.store, j))
	river.AddWorker(w, &RetentionWorker{pruner: j.store})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(retentionInterval),
			func() (river.JobArgs, *river.InsertOpts) {
				return InboundRetentionArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		),
	}
}

// ReconcilePending enqueues an inbound_process job for every accepted intake row that
// has no job yet (process_job_id IS NULL). Run ONCE at startup as the cutover.
//
// Because the accept-tx is a single transaction (intake insert + job enqueue + job-id
// stamp commit together), a committed accepted row in steady state ALWAYS has its
// job — so this set is normally empty. It exists to enqueue (a) any accepted rows at
// the moment the mode is first flipped on, and (b) rows stranded by a crash between
// insert and enqueue. Idempotent: the per-row FOR UPDATE + process_job_id IS NULL
// guard means a re-run (or a concurrent replica) never double-enqueues.
//
// NOTE (follow-up, matching outboundsend): no live periodic reconciler yet. The one
// residual it would close is an intake left accepted-with-a-terminal/dead-job if the
// worker's MarkInboundIntakeFailed somehow never lands — rare, and the startup pass
// re-drives NULL-job rows on the next deploy.
func (j *Jobs) ReconcilePending(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx,
		`SELECT id FROM inbound_intake
		  WHERE status='accepted' AND process_job_id IS NULL
		  LIMIT $1`, reconcileBatch)
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
			// enqueued it already. Skip if process_job_id is now set.
			var jobID *int64
			if err := tx.QueryRow(ctx,
				`SELECT process_job_id FROM inbound_intake WHERE id=$1 FOR UPDATE`, id,
			).Scan(&jobID); err != nil {
				return err
			}
			if jobID != nil {
				return nil // already enqueued
			}
			newJobID, err := j.EnqueueInboundProcessTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := j.store.StampInboundIntakeJobIDTx(ctx, tx, id, newJobID); err != nil {
				return err
			}
			n++
			return nil
		}); err != nil {
			log.Printf("[inbound-reconcile] enqueue %s: %v", id, err)
		}
	}
	return n, nil
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

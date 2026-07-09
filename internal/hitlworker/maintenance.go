package hitlworker

import (
	"context"
	"log"
	"time"

	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// maintenanceInterval is the TTL-sweep cadence — preserves the prior hand-rolled
// ticker's 60s (was DefaultInterval). Short enough that TTL boundaries are honored
// within a minute, long enough to avoid hot-looping the DB when there's nothing to do.
const maintenanceInterval = 60 * time.Second

// Sweeper runs one TTL-expiration sweep of both hold queues (outbound holds +
// inbound review holds). Satisfied by *Worker — the River worker just drives its
// RunOnce on a schedule instead of a hand-rolled time.Ticker.
type Sweeper interface {
	RunOnce(ctx context.Context) error
}

// HITLMaintenanceArgs drives the periodic HITL TTL sweep. No fields — the worker
// sweeps every expired hold each run.
type HITLMaintenanceArgs struct{}

func (HITLMaintenanceArgs) Kind() string { return "hitl_ttl_sweep" }

// MaintenanceWorker runs the TTL sweep once per scheduled job. Any sweep error is
// logged and swallowed (Work returns nil) so a transient DB blip never spins
// River's retry machinery for a best-effort idempotent sweep — the next interval
// picks it up. Mirrors webhookdelivery.MaintenanceWorker.
type MaintenanceWorker struct {
	river.WorkerDefaults[HITLMaintenanceArgs]
	sweeper Sweeper
}

// NewMaintenanceWorker builds the worker around a Sweeper. Exported so tests can
// drive Work directly (RegisterJobs builds an identical one for the client).
func NewMaintenanceWorker(sweeper Sweeper) *MaintenanceWorker {
	return &MaintenanceWorker{sweeper: sweeper}
}

// Timeout disables River's per-job timeout for the sweep (River's client default is
// 1 minute). Unlike a typical maintenance job — a quick handful of DELETEs — one
// sweep can legitimately run for many minutes: it performs up to DefaultBatchSize
// (100) SYNCHRONOUS, retrying SMTP sends for expired auto-approve holds, and the
// relay send is not context-cancellable (its own per-send network deadline bounds
// it). Under the 60s default a backlogged sweep is cancelled mid-iteration; the
// store's ctx-derived hold transitions then fail and surface as FALSE
// "[hitl-stuck] needs_manual_intervention" alarms on the auto-send path — every
// cycle, precisely during SMTP/SES degradation. The old hand-rolled ticker ran
// under an unbounded context; returning a negative duration restores that (River
// treats <0 as "no timeout"). The structural fix that would let this return to a
// bounded timeout is moving each auto-send onto its own QueueOutbound job (tracked
// follow-up) so a sweep does DB-only work again.
func (w *MaintenanceWorker) Timeout(*river.Job[HITLMaintenanceArgs]) time.Duration {
	return -1
}

func (w *MaintenanceWorker) Work(ctx context.Context, _ *river.Job[HITLMaintenanceArgs]) error {
	if err := w.sweeper.RunOnce(ctx); err != nil {
		log.Printf("[hitl-sweep] TTL sweep error (swallowed; next tick retries): %v", err)
	}
	return nil
}

// MaintenanceJobs is the jobs.Registrar for the HITL TTL sweep: it contributes the
// MaintenanceWorker and a periodic that fires it on QueueMaintenance. No enqueuer
// needed — the schedule is the only trigger.
type MaintenanceJobs struct{ sweeper Sweeper }

// NewMaintenanceJobs builds the registrar around a Sweeper (the *Worker).
func NewMaintenanceJobs(sweeper Sweeper) *MaintenanceJobs { return &MaintenanceJobs{sweeper: sweeper} }

// RegisterJobs adds the maintenance worker + its periodic schedule. Mirrors the
// webhook janitor + inbound retention periodics: routed to QueueMaintenance, no
// UniqueOpts (River's periodic scheduler already inserts at most one per interval
// and a completed run must not dedup-block the next), RunOnStart:false (first sweep
// after one interval — a conscious minor change from the old ticker's immediate
// first sweep, consistent with the other periodics; the per-row store operations are
// idempotent + cross-replica-safe so scheduling is the only concern). Implements
// jobs.Registrar.
func (m *MaintenanceJobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, &MaintenanceWorker{sweeper: m.sweeper})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(maintenanceInterval),
			maintenanceJobConstructor,
			&river.PeriodicJobOpts{RunOnStart: false},
		),
	}
}

// maintenanceJobConstructor builds each tick's insert: a HITLMaintenanceArgs
// routed to QueueMaintenance. Factored out (vs. an inline closure) so a test can
// assert the queue routing, which River's *PeriodicJob doesn't expose.
func maintenanceJobConstructor() (river.JobArgs, *river.InsertOpts) {
	return HITLMaintenanceArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}
}

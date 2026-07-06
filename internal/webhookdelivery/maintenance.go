package webhookdelivery

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// maintenanceInterval is the janitor cadence — matches the prior AutoDisableWorker
// ticker (5 min). Low urgency: both passes are cheap and idempotent.
const maintenanceInterval = 5 * time.Minute

// Sweeper runs the webhook maintenance passes (auto-disable chronically-failing
// webhooks + clear expired signing_secret_prev rows). Satisfied by
// *webhook.AutoDisableWorker — the River worker just drives its Tick on a schedule
// instead of a hand-rolled time.Ticker.
type Sweeper interface {
	Tick(ctx context.Context)
}

// WebhookMaintenanceArgs drives the periodic webhook janitor. No fields — the
// worker sweeps the whole table each run.
type WebhookMaintenanceArgs struct{}

func (WebhookMaintenanceArgs) Kind() string { return "webhook_maintenance" }

// MaintenanceWorker runs the janitor passes once per scheduled job. Errors are
// swallowed inside Tick (logged there); Work returns nil so a transient DB blip
// never spins River's retry machinery for a best-effort idempotent sweep — the
// next interval picks it up.
type MaintenanceWorker struct {
	river.WorkerDefaults[WebhookMaintenanceArgs]
	sweeper Sweeper
}

// NewMaintenanceWorker builds the worker around a Sweeper. Exported so tests can
// drive Work directly (RegisterJobs builds an identical one for the client).
func NewMaintenanceWorker(sweeper Sweeper) *MaintenanceWorker {
	return &MaintenanceWorker{sweeper: sweeper}
}

func (w *MaintenanceWorker) Work(ctx context.Context, _ *river.Job[WebhookMaintenanceArgs]) error {
	w.sweeper.Tick(ctx)
	return nil
}

// MaintenanceJobs is the jobs.Registrar for the webhook janitor: it contributes
// the MaintenanceWorker and a periodic that fires it on QueueMaintenance. No
// enqueuer needed — the schedule is the only trigger.
type MaintenanceJobs struct{ sweeper Sweeper }

// NewMaintenanceJobs builds the registrar around a Sweeper (the AutoDisableWorker).
func NewMaintenanceJobs(sweeper Sweeper) *MaintenanceJobs { return &MaintenanceJobs{sweeper: sweeper} }

// RegisterJobs adds the maintenance worker + its periodic schedule. Mirrors
// senderidentity's reaper: routed to QueueMaintenance, no UniqueOpts (River's
// periodic scheduler already inserts at most one per interval and a completed run
// must not dedup-block the next), RunOnStart:false (first sweep after one interval).
func (m *MaintenanceJobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, &MaintenanceWorker{sweeper: m.sweeper})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(maintenanceInterval),
			func() (river.JobArgs, *river.InsertOpts) {
				return WebhookMaintenanceArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		),
	}
}

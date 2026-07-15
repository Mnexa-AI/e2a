package hitlworker

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// fakeSweeper records how many times RunOnce ran and returns a canned error, so
// the worker's swallow behavior can be exercised without a database.
type fakeSweeper struct {
	runs int
	err  error
}

func (f *fakeSweeper) RunOnce(_ context.Context) error {
	f.runs++
	return f.err
}

// TestMaintenanceWorker_WorkSwallowsSweepError: the periodic worker drives the
// sweeper's RunOnce once per job and returns nil even when RunOnce errors — a
// transient DB blip must not spin River's retry machinery for a best-effort
// idempotent sweep; the next interval picks it up.
func TestMaintenanceWorker_WorkSwallowsSweepError(t *testing.T) {
	sw := &fakeSweeper{err: errors.New("boom")}
	w := NewMaintenanceWorker(sw)
	if err := w.Work(context.Background(), &river.Job[HITLMaintenanceArgs]{}); err != nil {
		t.Fatalf("Work returned %v, want nil (error must be swallowed)", err)
	}
	if sw.runs != 1 {
		t.Errorf("sweeper ran %d times, want 1", sw.runs)
	}
}

// TestMaintenanceWorker_WorkRunsSweep: the happy path also drives RunOnce once
// and returns nil.
func TestMaintenanceWorker_WorkRunsSweep(t *testing.T) {
	sw := &fakeSweeper{}
	w := NewMaintenanceWorker(sw)
	if err := w.Work(context.Background(), &river.Job[HITLMaintenanceArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if sw.runs != 1 {
		t.Errorf("sweeper ran %d times, want 1", sw.runs)
	}
}

// TestMaintenanceWorker_Timeout: auto-approve is queue-first, so the sweep is
// DB-only and its timeout can remain bounded.
func TestMaintenanceWorker_Timeout(t *testing.T) {
	if got := NewMaintenanceWorker(&fakeSweeper{}).Timeout(&river.Job[HITLMaintenanceArgs]{}); got != sweepTimeout {
		t.Errorf("Timeout() = %v, want %v (bounded)", got, sweepTimeout)
	}
}

// TestMaintenanceJobs_RegistersOnePeriodic: RegisterJobs contributes exactly one
// periodic (the TTL sweep schedule) and wires its worker.
func TestMaintenanceJobs_RegistersOnePeriodic(t *testing.T) {
	m := NewMaintenanceJobs(&fakeSweeper{})
	periodics := m.RegisterJobs(river.NewWorkers())
	if len(periodics) != 1 {
		t.Fatalf("RegisterJobs returned %d periodic jobs, want 1", len(periodics))
	}
}

// TestMaintenanceJobConstructor_RoutesToMaintenanceQueue: each scheduled tick
// inserts a hitl_ttl_sweep job onto QueueMaintenance. River's *PeriodicJob keeps
// its constructor unexported, so this asserts the factored constructor directly.
func TestMaintenanceJobConstructor_RoutesToMaintenanceQueue(t *testing.T) {
	args, opts := maintenanceJobConstructor()
	if got := args.Kind(); got != "hitl_ttl_sweep" {
		t.Errorf("args.Kind() = %q, want %q", got, "hitl_ttl_sweep")
	}
	if opts == nil || opts.Queue != jobs.QueueMaintenance {
		t.Errorf("insert queue = %q, want %q", queueOf(opts), jobs.QueueMaintenance)
	}
}

func queueOf(o *river.InsertOpts) string {
	if o == nil {
		return "<nil>"
	}
	return o.Queue
}

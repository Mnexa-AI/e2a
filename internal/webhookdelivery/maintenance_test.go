package webhookdelivery_test

import (
	"context"
	"testing"

	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/webhookdelivery"
)

type fakeSweeper struct{ ticks int }

func (f *fakeSweeper) Tick(_ context.Context) { f.ticks++ }

// TestMaintenanceWorker_WorkRunsSweep: the periodic worker drives the sweeper's
// Tick once per job and returns nil (best-effort — no River retry on a sweep).
func TestMaintenanceWorker_WorkRunsSweep(t *testing.T) {
	sw := &fakeSweeper{}
	w := webhookdelivery.NewMaintenanceWorker(sw)
	if err := w.Work(context.Background(), &river.Job[webhookdelivery.WebhookMaintenanceArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if sw.ticks != 1 {
		t.Errorf("sweeper ticked %d times, want 1", sw.ticks)
	}
}

// TestMaintenanceJobs_RegistersPeriodic: RegisterJobs contributes exactly one
// periodic (the janitor schedule) and wires its worker.
func TestMaintenanceJobs_RegistersPeriodic(t *testing.T) {
	m := webhookdelivery.NewMaintenanceJobs(&fakeSweeper{})
	periodics := m.RegisterJobs(river.NewWorkers())
	if len(periodics) != 1 {
		t.Fatalf("RegisterJobs returned %d periodic jobs, want 1", len(periodics))
	}
}

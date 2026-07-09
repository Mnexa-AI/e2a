package janitor

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// TestPeriodicConstructor_RoutesToMaintenance: the periodic's per-fire
// constructor routes each sweep to QueueMaintenance with the janitor args.
// White-box because river.PeriodicJob keeps its constructor unexported, so this
// is the only way to assert the queue routing directly.
func TestPeriodicConstructor_RoutesToMaintenance(t *testing.T) {
	args, opts := janitorPeriodicConstructor()

	if opts == nil {
		t.Fatal("constructor returned nil InsertOpts")
	}
	if opts.Queue != jobs.QueueMaintenance {
		t.Errorf("periodic routed to queue %q, want %q", opts.Queue, jobs.QueueMaintenance)
	}
	if _, ok := args.(JanitorArgs); !ok {
		t.Errorf("constructor returned args of type %T, want JanitorArgs", args)
	}
	if got := args.Kind(); got != "janitor_sweep" {
		t.Errorf("JanitorArgs.Kind() = %q, want %q", got, "janitor_sweep")
	}
}

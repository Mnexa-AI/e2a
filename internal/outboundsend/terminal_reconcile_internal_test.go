package outboundsend

import (
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

func TestTerminalReconcilePeriodicConstructor_RoutesToMaintenance(t *testing.T) {
	if terminalReconcileInterval != time.Minute {
		t.Errorf("terminalReconcileInterval = %s, want %s", terminalReconcileInterval, time.Minute)
	}
	args, opts := terminalReconcilePeriodicConstructor()
	if opts == nil {
		t.Fatal("constructor returned nil InsertOpts")
	}
	if opts.Queue != jobs.QueueMaintenance {
		t.Errorf("periodic routed to queue %q, want %q", opts.Queue, jobs.QueueMaintenance)
	}
	if _, ok := args.(TerminalReconcileArgs); !ok {
		t.Errorf("constructor returned args of type %T, want TerminalReconcileArgs", args)
	}
	if got := args.Kind(); got != "outbound_terminal_reconcile" {
		t.Errorf("TerminalReconcileArgs.Kind() = %q, want outbound_terminal_reconcile", got)
	}
}

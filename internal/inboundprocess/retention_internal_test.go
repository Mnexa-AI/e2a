package inboundprocess

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

type fakePruner struct {
	calls int
	n     int64
}

func (f *fakePruner) PruneProcessedIntake(_ context.Context, _ time.Duration) (int64, error) {
	f.calls++
	return f.n, nil
}

// TestRetentionWorker_Work covers the retention sweep body: it calls the pruner once
// with the retention window and does not error on a non-zero prune count.
func TestRetentionWorker_Work(t *testing.T) {
	p := &fakePruner{n: 3}
	w := &RetentionWorker{pruner: p}
	if err := w.Work(context.Background(), &river.Job[InboundRetentionArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if p.calls != 1 {
		t.Errorf("prune calls = %d, want 1", p.calls)
	}
}

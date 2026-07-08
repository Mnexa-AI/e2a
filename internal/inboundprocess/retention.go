package inboundprocess

import (
	"context"
	"log"
	"time"

	"github.com/riverqueue/river"
)

const (
	// retentionInterval is how often the processed-intake sweep runs.
	retentionInterval = 6 * time.Hour
	// retentionWindow keeps processed intake for a short debugging / re-drive window.
	// The raw MIME also lives in messages.raw_message once processed, so pruning past
	// this is non-destructive. Failed rows are retained (not pruned) for inspection.
	retentionWindow = 72 * time.Hour
)

// InboundRetentionArgs drives one retention sweep (no payload — the worker prunes the
// whole processed-and-old set).
type InboundRetentionArgs struct{}

func (InboundRetentionArgs) Kind() string { return "inbound_retention" }

// Pruner is the retention surface (a subset of Store).
type Pruner interface {
	PruneProcessedIntake(ctx context.Context, olderThan time.Duration) (int64, error)
}

// RetentionWorker prunes processed intake older than retentionWindow. Scheduled as a
// River periodic on QueueMaintenance (mirrors the webhook auto-disable janitor).
type RetentionWorker struct {
	river.WorkerDefaults[InboundRetentionArgs]
	pruner Pruner
}

func (w *RetentionWorker) Work(ctx context.Context, _ *river.Job[InboundRetentionArgs]) error {
	n, err := w.pruner.PruneProcessedIntake(ctx, retentionWindow)
	if err != nil {
		return err // retryable — the periodic re-runs next interval regardless
	}
	if n > 0 {
		log.Printf("[inbound-retention] pruned %d processed intake rows older than %s", n, retentionWindow)
	}
	return nil
}

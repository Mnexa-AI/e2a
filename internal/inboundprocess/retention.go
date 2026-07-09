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

// Work intentionally has no Timeout() override — it relies on River's 60s default
// JobTimeout. PruneProcessedIntake is a single unbounded DELETE; inbound_intake is
// short-lived (processed rows pruned every tick), so the set is normally tiny. On a
// large backlog a 60s cut is safe (the DELETE is ctx-aware — it rolls back and the next
// tick resumes), just slow. TODO: batch it like the janitor's ctid-LIMIT prunes (#397)
// if a backlog ever makes the single DELETE a problem.
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

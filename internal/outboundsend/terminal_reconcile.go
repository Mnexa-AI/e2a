package outboundsend

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

const terminalReconcileInterval = time.Minute

// TerminalReconcileArgs drives the periodic safety net for outbound messages
// whose stamped send job reached a terminal state before recording delivery.
type TerminalReconcileArgs struct{}

func (TerminalReconcileArgs) Kind() string { return "outbound_terminal_reconcile" }

// TerminalReconcileWorker marks accepted/sending outbound messages failed after
// their stamped River job is terminal or has already been pruned. SendWorker is
// still the primary owner; the compare-and-set store transition makes races safe.
type TerminalReconcileWorker struct {
	river.WorkerDefaults[TerminalReconcileArgs]
	pool  *pgxpool.Pool
	store Store
}

// NewTerminalReconcileWorker builds the periodic safety-net worker.
func NewTerminalReconcileWorker(pool *pgxpool.Pool, store Store) *TerminalReconcileWorker {
	return &TerminalReconcileWorker{pool: pool, store: store}
}

type terminalCandidate struct {
	messageID string
	attempt   int
	state     string
}

func (w *TerminalReconcileWorker) Work(ctx context.Context, _ *river.Job[TerminalReconcileArgs]) error {
	rows, err := w.pool.Query(ctx,
		`SELECT m.id,
		        COALESCE(r.attempt, 0),
		        CASE WHEN r.id IS NULL THEN 'missing' ELSE r.state::text END
		   FROM messages m
		   LEFT JOIN river_job r ON r.id = m.send_job_id
		  WHERE m.direction = 'outbound'
		    AND m.delivery_status IN ('accepted', 'sending')
		    AND m.send_job_id IS NOT NULL
		    AND (r.id IS NULL OR r.state IN ('cancelled', 'discarded', 'completed'))
		  ORDER BY m.created_at ASC, m.id ASC
		  LIMIT $1`,
		jobs.DefaultReconcileBatch,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	candidates := make([]terminalCandidate, 0)
	for rows.Next() {
		var candidate terminalCandidate
		if err := rows.Scan(&candidate.messageID, &candidate.attempt, &candidate.state); err != nil {
			return err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	processed := 0
	for _, candidate := range candidates {
		detail := fmt.Sprintf("outbound send job %s before terminal delivery status was recorded", candidate.state)
		if err := w.store.MarkFailed(ctx, candidate.messageID, candidate.attempt, detail); err != nil {
			if processed > 0 {
				log.Printf("[outbound-terminal-reconcile] processed %d candidates", processed)
			}
			return err
		}
		processed++
	}
	if processed > 0 {
		log.Printf("[outbound-terminal-reconcile] processed %d candidates", processed)
	}
	return nil
}

func terminalReconcilePeriodicConstructor() (river.JobArgs, *river.InsertOpts) {
	return TerminalReconcileArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}
}

package jobs

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultReconcileBatch bounds one reconcile scan. In steady state the stranded set is
// ~empty; under a systemic enqueue failure it caps how many rows one pass re-drives
// (one tx each) so an unhealthy River isn't amplified by fanning the whole backlog
// every tick — the remainder is picked up on the next pass.
const DefaultReconcileBatch = 1000

// ReconcileSpec describes one table's stranded-row reconcile.
//
// SECURITY: Table, JobColumn, and Where are COMPILE-TIME CONSTANTS supplied by the
// calling package — never runtime or user input. They are interpolated directly into
// SQL (there is no parameter form for identifiers), so callers must pass only literal
// strings. The only runtime value (the row id) is always a bound parameter.
type ReconcileSpec struct {
	// Table is the row table (e.g. "messages", "webhook_events").
	Table string
	// JobColumn is the nullable bigint column holding the enqueued River job id
	// (e.g. "send_job_id", "fanout_job_id"). A row is "stranded" when it matches
	// Where AND JobColumn IS NULL.
	JobColumn string
	// Where is the domain predicate identifying rows that SHOULD carry a job
	// (e.g. "direction='outbound' AND delivery_status='accepted'"). ANDed with
	// "<JobColumn> IS NULL"; pass no leading/trailing "AND".
	Where string
	// LogPrefix tags per-row enqueue-failure logs (e.g. "[outbound-reconcile]").
	LogPrefix string
	// Batch caps rows scanned per pass; 0 uses DefaultReconcileBatch.
	Batch int
}

// ReconcilePending re-drives every row matching spec.Where whose spec.JobColumn IS
// NULL: it scans up to spec.Batch ids, then per id opens a tx, re-checks the job column
// under FOR UPDATE (skipping rows another process or a prior pass already enqueued),
// calls enqueueTx to insert the River job in that tx, and stamps the returned job id
// back onto the row — all atomically. Idempotent: the FOR UPDATE + IS NULL guard means
// a re-run (or a concurrent replica) never double-enqueues. A per-row failure is logged
// and skipped (the next pass retries it); the returned count is the rows enqueued.
//
// This is the shared body behind every domain's startup cutover + live reconcile
// worker (outboundsend, inboundprocess, hitlnotify, webhookdelivery, webhookpub
// fan-out). Each domain supplies a ReconcileSpec + its own EnqueueXTx.
func ReconcilePending(ctx context.Context, pool *pgxpool.Pool, spec ReconcileSpec,
	enqueueTx func(ctx context.Context, tx pgx.Tx, id string) (int64, error)) (int, error) {

	batch := spec.Batch
	if batch <= 0 {
		batch = DefaultReconcileBatch
	}

	rows, err := pool.Query(ctx,
		fmt.Sprintf(`SELECT id FROM %s WHERE %s AND %s IS NULL LIMIT $1`,
			spec.Table, spec.Where, spec.JobColumn),
		batch)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	recheckSQL := fmt.Sprintf(`SELECT %s FROM %s WHERE id=$1 FOR UPDATE`, spec.JobColumn, spec.Table)
	stampSQL := fmt.Sprintf(`UPDATE %s SET %s=$2 WHERE id=$1`, spec.Table, spec.JobColumn)

	n := 0
	for _, id := range ids {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// Re-check under a row lock: another process (or a prior run) may have
			// enqueued it already. Skip if the job column is now set.
			var jobID *int64
			if err := tx.QueryRow(ctx, recheckSQL, id).Scan(&jobID); err != nil {
				return err
			}
			if jobID != nil {
				return nil // already enqueued
			}
			newJobID, err := enqueueTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, stampSQL, id, newJobID); err != nil {
				return err
			}
			n++
			return nil
		}); err != nil {
			log.Printf("%s enqueue %s: %v", spec.LogPrefix, id, err)
		}
	}
	return n, nil
}

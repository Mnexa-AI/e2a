package outboundsend

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// reconcileBatch bounds one cutover scan of stranded accepted rows — mirrors
// webhookdelivery.reconcileBatch. In steady state the stranded set is ~empty (the
// accept-tx stamps send_job_id atomically); this caps how many rows one pass
// re-drives if a systemic enqueue failure ever stranded a backlog.
const reconcileBatch = 1000

// Jobs is the outbound-send integration on the shared River client: a
// jobs.Registrar (contributes SendWorker) plus the transactional enqueue entry
// point the accept-tx calls. The shared client is injected via SetEnqueuer after
// jobs.New builds it (two-phase wiring, same as webhookdelivery / senderidentity).
type Jobs struct {
	store     Store
	deliverer Deliverer
	enq       jobs.Enqueuer
}

// NewJobs builds the integration with its dependencies (no client yet).
func NewJobs(store Store, deliverer Deliverer) *Jobs {
	return &Jobs{store: store, deliverer: deliverer}
}

// SetEnqueuer injects the shared client so EnqueueSendTx can insert jobs.
func (j *Jobs) SetEnqueuer(e jobs.Enqueuer) { j.enq = e }

// RegisterJobs adds the SendWorker to the shared client's bundle. Implements
// jobs.Registrar.
//
// No live periodic reconciler yet (slice D). The one residual it would close: if a
// job's terminal write (markFailed) fails on all its retries, the worker still
// cancels/discards the River job, leaving the row `accepted` with a stamped-but-dead
// job — which ReconcilePending (keyed on send_job_id IS NULL) does not catch. That
// needs 3+ consecutive DB failures on the terminal write (very rare); a slice-D
// periodic keyed on `accepted AND the job is terminal/absent` closes it.
func (j *Jobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, NewSendWorker(j.store, j.deliverer))
	return nil
}

// ReconcilePending enqueues an outbound_send job for every accepted message that
// has no send job yet (send_job_id IS NULL). Run ONCE at startup as the cutover.
//
// Because the accept-tx is a single transaction (message insert + job enqueue +
// send_job_id stamp all commit together), a committed `accepted` row in steady
// state ALWAYS has send_job_id set — so the send_job_id IS NULL set is normally
// empty. This exists to enqueue (a) any pre-async `accepted` rows at the moment the
// mode is first flipped on, and (b) rows from a future accept-tx variant that
// doesn't stamp atomically. Idempotent: the per-row FOR UPDATE + send_job_id IS NULL
// guard means a re-run (or concurrent replica) never
// double-enqueues. Mirrors webhookdelivery.ReconcilePending. Returns the count
// enqueued.
func (j *Jobs) ReconcilePending(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx,
		`SELECT id FROM messages
		  WHERE direction='outbound' AND delivery_status='accepted' AND send_job_id IS NULL
		  LIMIT $1`, reconcileBatch)
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

	n := 0
	for _, id := range ids {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// Re-check under a row lock: another process (or a prior run) may have
			// enqueued it already. Skip if send_job_id is now set.
			var jobID *int64
			if err := tx.QueryRow(ctx,
				`SELECT send_job_id FROM messages WHERE id=$1 FOR UPDATE`, id,
			).Scan(&jobID); err != nil {
				return err
			}
			if jobID != nil {
				return nil // already enqueued
			}
			newJobID, err := j.EnqueueSendTx(ctx, tx, id)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE messages SET send_job_id=$2 WHERE id=$1`, id, newJobID)
			if err == nil {
				n++
			}
			return err
		}); err != nil {
			log.Printf("[outbound-reconcile] enqueue %s: %v", id, err)
		}
	}
	return n, nil
}

// EnqueueSendTx enqueues a send job WITHIN the caller's transaction — the outbox
// pattern: the accept-tx's messages-row insert and this job commit together, so an
// `accepted` message can never exist without a send job (or vice versa). The
// accept-tx stamps the returned river_job id on messages.send_job_id (slice C) so
// the reconciler can find stranded rows (`accepted` with no job). Mirrors
// webhookdelivery.EnqueueDeliveryTx.
func (j *Jobs) EnqueueSendTx(ctx context.Context, tx pgx.Tx, messageID string) (int64, error) {
	res, err := j.enq.InsertTx(ctx, tx, OutboundSendArgs{MessageID: messageID}, &river.InsertOpts{
		Queue:       jobs.QueueOutbound,
		MaxAttempts: MaxSendAttempts,
	})
	if err != nil {
		return 0, err
	}
	return res.Job.ID, nil
}

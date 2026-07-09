package hitlnotify

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/jobs"
)

// reconcileBatch bounds one cutover scan of pending_review rows without a
// notification job — mirrors outboundsend/inboundprocess. In steady state the set
// is ~empty (the accept-tx stamps notify_job_id atomically); this caps how many
// rows one pass re-drives if the feature was just enabled or a crash left a backlog.
const reconcileBatch = 1000

// Jobs is the HITL-notification integration on the shared River client: a
// jobs.Registrar (contributes NotifyWorker) plus the transactional enqueue entry
// point the hold accept-tx calls. Both the shared client and the concrete Deliverer
// are injected AFTER construction (two-phase wiring) — the client via SetEnqueuer,
// the Deliverer (the Notifier, which needs the relay + signer resolved) via
// SetDeliverer. Jobs itself is the worker's Deliverer, late-binding to the concrete
// one, mirroring inboundprocess's late-bound Processor.
type Jobs struct {
	store Store
	enq   jobs.Enqueuer

	mu        sync.RWMutex
	deliverer Deliverer
}

// NewJobs builds the integration with just its store (no client, no deliverer yet).
func NewJobs(store Store) *Jobs { return &Jobs{store: store} }

// SetEnqueuer injects the shared client so EnqueueNotifyTx can insert jobs.
func (j *Jobs) SetEnqueuer(e jobs.Enqueuer) { j.enq = e }

// SetDeliverer injects the concrete Deliverer (the Notifier), built after the
// relay/signer gating resolves. Guarded so the River worker goroutines read it
// race-free.
func (j *Jobs) SetDeliverer(d Deliverer) {
	j.mu.Lock()
	j.deliverer = d
	j.mu.Unlock()
}

// Deliver makes Jobs itself the worker's Deliverer, delegating to the concrete one
// set via SetDeliverer. Until that is wired (the brief startup window before the
// notifier is built) it returns a retryable outcome, so a pending job simply
// retries rather than dropping on a nil deliverer.
func (j *Jobs) Deliver(ctx context.Context, pn *identity.PendingNotify) DeliverOutcome {
	j.mu.RLock()
	d := j.deliverer
	j.mu.RUnlock()
	if d == nil {
		return DeliverOutcome{Err: errors.New("hitl notifier not wired yet — retrying")}
	}
	return d.Deliver(ctx, pn)
}

// RegisterJobs adds the NotifyWorker (with Jobs as the late-binding Deliverer).
// No periodics — the reconciler is a one-shot startup cutover. Implements
// jobs.Registrar.
func (j *Jobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, NewNotifyWorker(j.store, j))
	return nil
}

// EnqueueNotifyTx inserts the hitl_notify job in the caller's hold accept-tx (the
// same tx as the pending_review insert), returning the River job id to stamp on the
// message so a committed pending_review row always has its notification job.
func (j *Jobs) EnqueueNotifyTx(ctx context.Context, tx pgx.Tx, messageID string) (int64, error) {
	res, err := j.enq.InsertTx(ctx, tx, HITLNotifyArgs{MessageID: messageID}, &river.InsertOpts{
		Queue:       jobs.QueueNotify,
		MaxAttempts: MaxNotifyAttempts,
	})
	if err != nil {
		return 0, err
	}
	return res.Job.ID, nil
}

// ReconcilePending enqueues a hitl_notify job for every pending_review message that
// has no job yet AND was never notified (notify_job_id IS NULL AND notified_at IS
// NULL). Run ONCE at startup as the cutover.
//
// Because the accept-tx is a single transaction (message insert + job enqueue +
// job-id stamp commit together), a committed pending_review row in steady state
// ALWAYS has its job — so this set is normally empty. It exists to enqueue holds
// created on the no-notifier plain path (notified_at NULL) if a relay is later
// configured, plus any row stranded by a crash between insert and enqueue.
//
// The `notified_at IS NULL` guard is what makes the feature's very first deploy
// safe: every hold already pending_review at cutover was notified by the old code
// path, and migration 057 stamps notified_at on exactly those rows, so this scan
// skips them — no owner is emailed twice. Idempotent: the per-row FOR UPDATE +
// notify_job_id IS NULL re-check means a re-run (or a concurrent replica) never
// double-enqueues.
func (j *Jobs) ReconcilePending(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx,
		`SELECT id FROM messages
		  WHERE status='pending_review' AND notify_job_id IS NULL AND notified_at IS NULL
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
			// enqueued it already. Skip if notify_job_id is now set.
			var jobID *int64
			if err := tx.QueryRow(ctx,
				`SELECT notify_job_id FROM messages WHERE id=$1 FOR UPDATE`, id,
			).Scan(&jobID); err != nil {
				return err
			}
			if jobID != nil {
				return nil // already enqueued
			}
			newJobID, err := j.EnqueueNotifyTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := j.store.StampNotifyJobIDTx(ctx, tx, id, newJobID); err != nil {
				return err
			}
			n++
			return nil
		}); err != nil {
			log.Printf("[hitl-notify] reconcile enqueue %s: %v", id, err)
		}
	}
	return n, nil
}

package webhookpub

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
	"github.com/Mnexa-AI/e2a/internal/telemetry"
)

// FanOutArgs drives one webhook fan-out: match the event user's enabled webhooks,
// insert the matching webhook_subscriber_deliveries rows, and enqueue their delivery
// jobs. Carries only the event id — the worker re-reads the durable webhook_events row
// (the three-layer pattern: the job is the trigger, the row is the source of truth),
// so args stay tiny and a schema change to the event never invalidates enqueued jobs.
//
// This is the River replacement for the legacy webhookpub.OutboxWorker drain
// (LISTEN/NOTIFY + poll + SKIP-LOCKED lease). See docs/design/webhook-fanout-river-migration.md.
type FanOutArgs struct {
	EventID string `json:"event_id"`
}

func (FanOutArgs) Kind() string { return "webhook_fan_out" }

const (
	// maxFanOutAttempts bounds River's retries of a fan-out job. Fan-out failures are
	// transient (DB blip, identity read) — a handful of retries rides them out. A
	// persistent failure (e.g. a matching bug) should surface as a discarded job, not
	// retry forever the way the legacy pending-row poll did.
	maxFanOutAttempts = 10

	// fanOutReconcileInterval / fanOutReconcileBatch mirror the delivery reconciler:
	// frequent + cheap (a partial index backs the status='pending' AND fanout_job_id
	// IS NULL scan) and bounded per tick so a systemic enqueue failure can't be
	// amplified by fanning the whole backlog every minute.
	fanOutReconcileInterval = 1 * time.Minute
	fanOutReconcileBatch    = 1000
)

// FanOutJobs is the webhook fan-out integration on the shared River client: a
// jobs.Registrar (contributes FanOutWorker + the reconcile periodic) plus the
// transactional enqueue entry point (EnqueueFanOutTx) that PublishTx /
// PublishBestEffortTx call in the event's own tx. The shared client is injected via
// SetEnqueuer after jobs.New builds it (two-phase wiring, same as webhookdelivery).
type FanOutJobs struct {
	pool          *pgxpool.Pool
	identityStore identityReader
	deliveryEnq   DeliveryEnqueuer
	metrics       telemetry.Metrics
	enq           jobs.Enqueuer
}

// NewFanOutJobs builds the integration with its dependencies (no client yet). pool
// backs the reconciler's scan; deliveryEnq is the SAME delivery enqueuer the legacy
// OutboxWorker uses — fan-out enqueues Layer-2→3 delivery jobs exactly as before.
func NewFanOutJobs(pool *pgxpool.Pool, identityStore identityReader, deliveryEnq DeliveryEnqueuer, metrics telemetry.Metrics) *FanOutJobs {
	if metrics == nil {
		metrics = telemetry.NoOp{}
	}
	return &FanOutJobs{pool: pool, identityStore: identityStore, deliveryEnq: deliveryEnq, metrics: metrics}
}

// SetEnqueuer injects the shared client so EnqueueFanOutTx can insert fan-out jobs.
func (j *FanOutJobs) SetEnqueuer(e jobs.Enqueuer) { j.enq = e }

// RegisterJobs adds the FanOutWorker + the reconcile worker and returns the reconcile
// periodic. Implements jobs.Registrar.
func (j *FanOutJobs) RegisterJobs(w *river.Workers) []*river.PeriodicJob {
	river.AddWorker(w, &FanOutWorker{pool: j.pool, identityStore: j.identityStore, deliveryEnq: j.deliveryEnq, metrics: j.metrics})
	river.AddWorker(w, &FanOutReconcileWorker{jobs: j})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(fanOutReconcileInterval),
			func() (river.JobArgs, *river.InsertOpts) {
				return FanOutReconcileArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		),
	}
}

// EnqueueFanOutTx enqueues a fan-out job WITHIN the caller's transaction — the outbox
// pattern between Layer 1 (webhook_events) and its fan-out: the event-row write and
// this job commit together, so a pending event can never exist without a job (modulo
// the best-effort publish path, which the reconciler backstops). Returns the river_job
// id for the caller to stamp on webhook_events.fanout_job_id. Routed to QueueWebhook.
func (j *FanOutJobs) EnqueueFanOutTx(ctx context.Context, tx pgx.Tx, eventID string) (int64, error) {
	res, err := j.enq.InsertTx(ctx, tx, FanOutArgs{EventID: eventID}, &river.InsertOpts{
		Queue:       jobs.QueueWebhook,
		MaxAttempts: maxFanOutAttempts,
	})
	if err != nil {
		return 0, err
	}
	return res.Job.ID, nil
}

// FanOutWorker fans out one webhook_events row on River, replacing the legacy
// OutboxWorker drain. It re-reads the event by id, skips it if it is gone (30d GC) or
// no longer 'pending' (a duplicate at-least-once job — already fanned out), and
// otherwise runs the shared fanOutEventCore. Idempotent: the (event_id, webhook_id)
// unique index dedups delivery-row inserts and the status='pending' guard on the
// terminal UPDATE makes a re-run a no-op.
type FanOutWorker struct {
	river.WorkerDefaults[FanOutArgs]
	pool          *pgxpool.Pool
	identityStore identityReader
	deliveryEnq   DeliveryEnqueuer
	metrics       telemetry.Metrics
}

// NewFanOutWorker builds a FanOutWorker. RegisterJobs builds an identical one for the
// client; this is exported so tests can drive Work directly without a River client.
func NewFanOutWorker(pool *pgxpool.Pool, identityStore identityReader, deliveryEnq DeliveryEnqueuer, metrics telemetry.Metrics) *FanOutWorker {
	if metrics == nil {
		metrics = telemetry.NoOp{}
	}
	return &FanOutWorker{pool: pool, identityStore: identityStore, deliveryEnq: deliveryEnq, metrics: metrics}
}

func (w *FanOutWorker) Work(ctx context.Context, job *river.Job[FanOutArgs]) error {
	ev, err := loadEventForFanOut(ctx, w.pool, job.Args.EventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // event GC'd (30d retention) before fan-out — nothing to do
		}
		return err
	}
	if ev.status != "pending" {
		return nil // already fanned out (processed/no_match) — idempotent re-run
	}
	return fanOutEventCore(ctx, w.pool, w.identityStore, w.deliveryEnq, w.metrics, ev.leasedEvent)
}

// Timeout bounds a fan-out job. It is a handful of queries + N inserts — never the long
// synchronous drain the janitor's 60s-cut hazard came from — but a bounded timeout
// keeps a pathologically slow DB from pinning a QueueWebhook worker indefinitely.
func (w *FanOutWorker) Timeout(*river.Job[FanOutArgs]) time.Duration { return 2 * time.Minute }

// loadedEvent is a leasedEvent plus its current status, for the fan-out re-read.
type loadedEvent struct {
	leasedEvent
	status string
}

// loadEventForFanOut re-reads the columns fanOutEventCore needs from webhook_events by
// id, plus status for the idempotency guard. Returns pgx.ErrNoRows if the row is gone.
func loadEventForFanOut(ctx context.Context, pool *pgxpool.Pool, eventID string) (loadedEvent, error) {
	var ev loadedEvent
	err := pool.QueryRow(ctx,
		`SELECT id, user_id, type, envelope, agent_id, conversation_id, message_id, status
		   FROM webhook_events WHERE id = $1`,
		eventID,
	).Scan(&ev.id, &ev.userID, &ev.eventType, &ev.envelope,
		&ev.agentID, &ev.conversationID, &ev.messageID, &ev.status)
	if err != nil {
		return loadedEvent{}, err
	}
	return ev, nil
}

// FanOutReconcileArgs drives the periodic stranded-event reconciler.
type FanOutReconcileArgs struct{}

func (FanOutReconcileArgs) Kind() string { return "webhook_fanout_reconcile" }

// FanOutReconcileWorker re-enqueues any pending event with no fan-out job. It is the
// LIVE backstop for the best-effort publish path (PublishBestEffortTx must not fail the
// caller's tx, so an event can commit with its enqueue lost) and any crash window
// between the event commit and the job insert — turning "recovered only on restart"
// into "recovered within fanOutReconcileInterval". Idempotent (fanout_job_id IS NULL
// guard). Mirrors webhookdelivery.ReconcileWorker.
type FanOutReconcileWorker struct {
	river.WorkerDefaults[FanOutReconcileArgs]
	jobs *FanOutJobs
}

func (w *FanOutReconcileWorker) Work(ctx context.Context, _ *river.Job[FanOutReconcileArgs]) error {
	n, err := w.jobs.ReconcilePending(ctx, w.jobs.pool)
	if err != nil {
		return err // River retries the reconcile job — a transient DB blip is fine
	}
	if n > 0 {
		log.Printf("[webhook-fanout-reconcile] re-enqueued %d stranded events", n)
	}
	return nil
}

// ReconcilePending enqueues a fan-out job for every pending webhook_events row with no
// job yet (fanout_job_id IS NULL). Runs at startup (cutover) AND on the live schedule
// (FanOutReconcileWorker). Idempotent: the per-row FOR UPDATE + fanout_job_id IS NULL
// guard means a re-run (or a concurrent replica) never double-enqueues. Returns the
// number of events enqueued.
func (j *FanOutJobs) ReconcilePending(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx,
		`SELECT id FROM webhook_events WHERE status='pending' AND fanout_job_id IS NULL LIMIT $1`, fanOutReconcileBatch)
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
			// enqueued it already. Skip if fanout_job_id is now set.
			var jobID *int64
			if err := tx.QueryRow(ctx,
				`SELECT fanout_job_id FROM webhook_events WHERE id=$1 FOR UPDATE`, id,
			).Scan(&jobID); err != nil {
				return err
			}
			if jobID != nil {
				return nil // already enqueued
			}
			newJobID, err := j.EnqueueFanOutTx(ctx, tx, id)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE webhook_events SET fanout_job_id=$2 WHERE id=$1`, id, newJobID)
			if err == nil {
				n++
			}
			return err
		}); err != nil {
			log.Printf("[webhook-fanout-reconcile] enqueue %s: %v", id, err)
		}
	}
	return n, nil
}

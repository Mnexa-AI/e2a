package webhookpub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxWorker drains webhook_events. For each pending row it reads
// enabled webhooks for the user, applies filter matching in Go, and
// inserts one webhook_subscriber_deliveries row per match, enqueuing a
// River delivery job for each in the same transaction. The River
// DeliverWorker then POSTs each row to the customer endpoint. See design
// §4.4 for the full architecture.
//
// Wakeup paths:
//   1. LISTEN webhook_events_new — dedicated connection, sub-50ms
//      latency from trigger COMMIT to fan-out.
//   2. 1s fallback poll — catches notifications missed during deploy
//      or LISTEN reconnect.
//
// Multi-replica safety:
//   - FOR UPDATE SKIP LOCKED + next_poll_at bump in GetPending leases
//     rows so concurrent replicas don't fan out the same event.
//   - The per-row partial unique index on (event_id, webhook_id) in
//     webhook_subscriber_deliveries is the backstop for lease-expiry
//     races: a slow worker B can finish after a fast worker A's
//     lease has expired and a new worker C has taken over; per-row
//     ON CONFLICT DO NOTHING swallows the duplicates rather than
//     aborting B's transaction.
//   - The final UPDATE outbox row uses WHERE status='pending' so a
//     stale-and-late worker can't overwrite a fast-finisher's
//     matched_webhook_ids snapshot.
type OutboxWorker struct {
	pool          *pgxpool.Pool
	identityStore identityReader
	pollInterval  time.Duration
	leaseDuration time.Duration
	batchSize     int
	concurrency   int
	metrics       telemetry.Metrics

	// notifyCh signals when LISTEN has received a notification. Buffer
	// is 1 with drop-on-full — bursts wake the worker once per tick,
	// not 1000 times.
	notifyCh chan struct{}

	// notifySaw tracks whether a NOTIFY fired since the last Tick.
	// Used to attribute fallback-poll wakeups vs. NOTIFY wakeups.
	notifySawLastTick bool

	// enqueuer enqueues a River delivery job for each newly-inserted Layer 2 row
	// IN THE SAME transaction as the insert (the outbox pattern between Layer 2
	// and Layer 3). Always wired in production — River is the sole delivery
	// engine. nil only in narrow tests that drive the DeliverWorker by hand.
	enqueuer DeliveryEnqueuer
}

// DeliveryEnqueuer enqueues a webhook delivery job transactionally.
// *webhookdelivery.Jobs satisfies it. Injected so webhookpub stays decoupled
// from the River-delivery package and the choice is a wiring decision.
type DeliveryEnqueuer interface {
	EnqueueDeliveryTx(ctx context.Context, tx pgx.Tx, deliveryID string) (int64, error)
}

// WithDeliveryEnqueuer wires River delivery (the production path). Without it,
// inserted Layer 2 rows have no delivery job — used only in tests that deliver
// pending rows by hand.
func (w *OutboxWorker) WithDeliveryEnqueuer(e DeliveryEnqueuer) *OutboxWorker {
	w.enqueuer = e
	return w
}

// identityReader is the subset of identity.Store the worker needs.
// Kept as an interface so tests can pass a fake.
type identityReader interface {
	ListEnabledWebhooksForRouting(ctx context.Context, userID, eventType string) ([]identity.Webhook, error)
}

// NewOutboxWorker constructs the slice-2 worker. Production wiring
// passes the real pool and identity.Store; tests can pass fakes.
func NewOutboxWorker(pool *pgxpool.Pool, store identityReader) *OutboxWorker {
	return &OutboxWorker{
		pool:          pool,
		identityStore: store,
		pollInterval:  1 * time.Second,
		leaseDuration: 5 * time.Minute,
		batchSize:     32,
		concurrency:   8,
		metrics:       telemetry.NoOp{},
		notifyCh:      make(chan struct{}, 1),
	}
}

// WithMetrics swaps in a real metrics backend. Default is NoOp so
// tests don't have to wire anything.
func (w *OutboxWorker) WithMetrics(m telemetry.Metrics) *OutboxWorker {
	if m == nil {
		m = telemetry.NoOp{}
	}
	w.metrics = m
	return w
}

// Start blocks on ctx — call in its own goroutine. Spawns the LISTEN
// reader in a sibling goroutine and runs the tick loop on the calling
// goroutine. Both stop when ctx is cancelled.
func (w *OutboxWorker) Start(ctx context.Context) {
	go w.listenLoop(ctx)
	w.tickLoop(ctx)
}

// listenLoop owns a dedicated, non-pool connection and re-issues
// LISTEN webhook_events_new on each reconnect. pgx's pool connections
// can't be used for LISTEN because subscription state is per-connection
// and gets lost when the connection returns to the pool.
//
// Reconnect backoff: 1s, 2s, 5s, 10s, 30s cap. During reconnect, the
// 1s fallback poll keeps the worker alive — no events lost.
func (w *OutboxWorker) listenLoop(ctx context.Context) {
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	attempt := 0

	for {
		if ctx.Err() != nil {
			return
		}

		conn, err := w.pool.Acquire(ctx)
		if err != nil {
			delay := backoff[min(attempt, len(backoff)-1)]
			log.Printf("[outbox-listen] acquire conn err (attempt %d, backing off %s): %v", attempt, delay, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
				attempt++
				continue
			}
		}

		if _, err := conn.Exec(ctx, "LISTEN webhook_events_new"); err != nil {
			conn.Release()
			log.Printf("[outbox-listen] LISTEN err: %v", err)
			attempt++
			continue
		}

		attempt = 0 // success — reset backoff
		log.Printf("[outbox-listen] subscribed to webhook_events_new")

		// Inner loop reads notifications until the connection drops.
		for {
			_, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				if ctx.Err() != nil {
					conn.Release()
					return
				}
				log.Printf("[outbox-listen] WaitForNotification err: %v (will reconnect)", err)
				conn.Release()
				break
			}
			// Best-effort signal: drop-on-full means a notification
			// storm doesn't fire the worker 1000 times. The worker
			// processes the batch on the next tick.
			select {
			case w.notifyCh <- struct{}{}:
				w.notifySawLastTick = true
			default:
			}
		}
	}
}

// tickLoop runs on the calling goroutine. Wakes on notifyCh OR on the
// pollInterval timer (whichever first). Drains a batch each wake.
func (w *OutboxWorker) tickLoop(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.notifyCh:
			w.Tick(ctx)
		case <-ticker.C:
			w.Tick(ctx)
		}
	}
}

// Tick processes one batch of pending events. Exposed (not just the
// private processBatch) so integration tests can drive the worker
// synchronously instead of waiting on the timer.
func (w *OutboxWorker) Tick(ctx context.Context) {
	tickStart := time.Now()
	notifyWake := w.notifySawLastTick
	w.notifySawLastTick = false

	// Publisher-lag gauge: age of the oldest pending row.
	var oldestAge float64
	if err := w.pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (now() - min(created_at)))
		 FROM webhook_events WHERE status = 'pending'`,
	).Scan(&oldestAge); err == nil {
		w.metrics.SetPublisherLag(oldestAge)
	}

	events, err := w.leasePending(ctx)
	if err != nil {
		log.Printf("[outbox-worker] leasePending err: %v", err)
		w.metrics.OutboxFailures("lease")
		return
	}
	if len(events) == 0 {
		return
	}
	// If we picked up events without a NOTIFY wakeup, the fallback
	// poll saved us. Non-zero rate signals LISTEN churn.
	if !notifyWake {
		w.metrics.NotifyMissed()
	}
	// Slice 10 telemetry hook: log batch size + age of oldest row so
	// publisher lag can be derived from access logs. A future
	// follow-up wires real Prometheus/OTLP counters.
	var oldest time.Time
	for _, ev := range events {
		_ = ev
	}
	log.Printf("[outbox-worker-metrics] tick batch=%d oldest_age_estimate=lease-bound elapsed_ms_so_far=%d",
		len(events), time.Since(tickStart).Milliseconds())
	_ = oldest
	_ = oldestAge // already emitted via SetPublisherLag

	sem := make(chan struct{}, w.concurrency)
	var wg sync.WaitGroup
	for _, ev := range events {
		ev := ev
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.fanOutOne(ctx, ev)
		}()
	}
	wg.Wait()
}

// leasedEvent is the worker's row-shape for an in-progress event.
type leasedEvent struct {
	id             string
	userID         string
	eventType      string
	envelope       []byte
	agentID        *string
	conversationID *string
	messageID      *string
	attempts       int
}

// leasePending claims up to batchSize pending events with FOR UPDATE
// SKIP LOCKED and bumps next_poll_at forward by leaseDuration in the
// same statement. Mirrors the SubscriberStore.GetPending pattern.
func (w *OutboxWorker) leasePending(ctx context.Context) ([]leasedEvent, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`WITH candidates AS (
		    SELECT id FROM webhook_events
		    WHERE status = 'pending' AND next_poll_at <= now()
		    ORDER BY created_at ASC
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED
		 )
		 UPDATE webhook_events e
		 SET next_poll_at = now() + ($2 * interval '1 second')
		 FROM candidates c
		 WHERE e.id = c.id
		 RETURNING e.id, e.user_id, e.type, e.envelope,
		           e.agent_id, e.conversation_id, e.message_id, e.attempts`,
		w.batchSize, int(w.leaseDuration.Seconds()),
	)
	if err != nil {
		return nil, err
	}

	var out []leasedEvent
	for rows.Next() {
		var ev leasedEvent
		if err := rows.Scan(&ev.id, &ev.userID, &ev.eventType, &ev.envelope,
			&ev.agentID, &ev.conversationID, &ev.messageID, &ev.attempts); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// fanOutOne handles a single leased event: read enabled subscribers,
// apply filter matching, insert delivery rows, mark outbox row
// processed — all in one transaction so partial fan-out is impossible.
func (w *OutboxWorker) fanOutOne(ctx context.Context, ev leasedEvent) {
	// The fan-out body is shared with the River FanOutWorker (fanout_worker.go) via
	// fanOutEventCore. The legacy worker maps a failure to recordFailure — the event
	// row stays 'pending' and the next lease retries; the River worker returns the
	// error for River to retry. Behavior here is unchanged from before the extraction:
	// the failure metrics are emitted inside the core at the same points and with the
	// same labels, so recordFailure remains the only thing this wrapper adds.
	if err := fanOutEventCore(ctx, w.pool, w.identityStore, w.enqueuer, w.metrics, ev); err != nil {
		w.recordFailure(ctx, ev.id, err.Error())
	}
}

// fanOutEventCore fans out a single loaded event in ONE transaction: match the user's
// enabled subscribers, insert one webhook_subscriber_deliveries row per match (ON
// CONFLICT dedup), enqueue a River delivery job per real insert, and mark the event
// row terminal (processed/no_match) under a status='pending' guard so a concurrent
// finisher (an expired-lease replica, or a duplicate at-least-once River job) can't be
// clobbered. Returns an error instead of recording failure so both the legacy
// OutboxWorker and the River FanOutWorker share the exact same body.
//
// Failure-metric parity with the pre-extraction fanOutOne: a list-subscribers error
// records NO OutboxFailures label (it only wraps the message, which recordFailure then
// logs); a tx error emits OutboxFailures("update_status"). Callers add their own
// recovery on top (recordFailure for the legacy poll, River retry for the worker).
func fanOutEventCore(ctx context.Context, pool *pgxpool.Pool, identityStore identityReader, enqueuer DeliveryEnqueuer, metrics telemetry.Metrics, ev leasedEvent) error {
	webhooks, err := identityStore.ListEnabledWebhooksForRouting(ctx, ev.userID, ev.eventType)
	if err != nil {
		return fmt.Errorf("list subscribers: %w", err)
	}

	// Reconstruct an Event-shaped struct from the leasedEvent to feed `matches`.
	eventForMatching := Event{
		Type:           ev.eventType,
		UserID:         ev.userID,
		AgentID:        derefString(ev.agentID),
		ConversationID: derefString(ev.conversationID),
		// Labels: not currently tracked on webhook_events; deferred.
		MessageID: derefString(ev.messageID),
	}

	// matched starts as an empty slice (not nil) so pgx serializes it as the empty
	// Postgres array '{}', not NULL. matched_webhook_ids is TEXT[] NOT NULL DEFAULT
	// '{}' — a NULL would fail the NOT NULL constraint and the UPDATE would error.
	matched := make([]string, 0, len(webhooks))
	for _, wh := range webhooks {
		if matches(eventForMatching, wh.Filters) {
			matched = append(matched, wh.ID)
		}
	}

	if len(matched) == 0 {
		metrics.OutboxEventsNoMatch(ev.eventType)
	} else {
		metrics.OutboxEventsFanOut(ev.eventType, len(matched))
	}
	if err := poolBeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if len(matched) > 0 {
			inserted, err := insertPendingBatchTx(ctx, tx, ev.id, matched, ev.eventType, ev.messageID, ev.envelope)
			if err != nil {
				return err
			}
			// Enqueue a River delivery job for each row that ACTUALLY inserted (dedup
			// no-ops are absent from `inserted`), in this same tx — the Layer 2 row and
			// its job commit atomically, so there is never a record without a job, and a
			// deduped event enqueues nothing. Stamp the row's job_id for the cutover
			// discriminator + observability.
			if enqueuer != nil {
				for _, d := range inserted {
					jobID, err := enqueuer.EnqueueDeliveryTx(ctx, tx, d)
					if err != nil {
						return err
					}
					if _, err := tx.Exec(ctx,
						`UPDATE webhook_subscriber_deliveries SET job_id = $2 WHERE id = $1`, d, jobID,
					); err != nil {
						return err
					}
				}
			}
		}
		// Conditional UPDATE: if another worker already processed this event row (our
		// lease expired during a long fan-out and replica B took over and finished, or
		// — under River — a duplicate at-least-once job ran), the status='pending'
		// predicate matches zero rows and this UPDATE no-ops.
		newStatus := "processed"
		if len(matched) == 0 {
			newStatus = "no_match"
		}
		_, err := tx.Exec(ctx,
			`UPDATE webhook_events
			 SET status = $1, processed_at = now(), matched_webhook_ids = $3
			 WHERE id = $2 AND status = 'pending'`,
			newStatus, ev.id, matched,
		)
		return err
	}); err != nil {
		metrics.OutboxFailures("update_status")
		return err
	}
	return nil
}

// insertPendingBatchTx writes one webhook_subscriber_deliveries row
// per matched webhook in a single multi-row INSERT. Per-row ON
// CONFLICT (event_id, webhook_id) WHERE event_id IS NOT NULL AND
// replay_id IS NULL DO NOTHING swallows duplicate inserts that come
// from a multi-replica lease race (worker A inserted, worker B's
// lease expired, retries, both see the row) without aborting the tx.
//
// The WHERE predicate is verbatim-matched to the partial unique index
// in migration 028 — Postgres requires exact predicate matching for
// ON CONFLICT to bind to a partial index.
// It RETURNs the ids of the rows that were ACTUALLY inserted (ON CONFLICT skips
// duplicates from a multi-replica lease race, and those are absent from
// RETURNING) — so the caller enqueues exactly one River delivery job per real
// insert, never for a dedup no-op.
func insertPendingBatchTx(ctx context.Context, tx pgx.Tx, eventID string, webhookIDs []string, eventType string, messageID *string, envelope []byte) ([]string, error) {
	if len(webhookIDs) == 0 {
		return nil, nil
	}

	// Build multi-row VALUES list. pgx supports parameterized arrays,
	// but the simplest portable shape is to fan out the parameters.
	args := make([]any, 0, 5+len(webhookIDs)*2)
	values := make([]string, 0, len(webhookIDs))
	args = append(args, eventID, eventType, messageID, envelope)
	for i, whID := range webhookIDs {
		// id, webhook_id placeholders. The other 4 fields are shared
		// across all rows so reference them by their fixed indexes.
		base := 4 + i*2
		values = append(values, fmt.Sprintf("($%d, $%d, $1, $2, $4, $3, 'pending')", base+1, base+2))
		args = append(args, generateDeliveryID(), whID)
	}

	sql := `INSERT INTO webhook_subscriber_deliveries
	            (id, webhook_id, event_id, event_type, event_payload, message_id, status)
	        VALUES ` + strings.Join(values, ", ") + `
	        ON CONFLICT (event_id, webhook_id)
	            WHERE event_id IS NOT NULL AND replay_id IS NULL
	            DO NOTHING
	        RETURNING id`

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var inserted []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		inserted = append(inserted, id)
	}
	return inserted, rows.Err()
}

// generateDeliveryID mints a whd_<32-hex> delivery id — the one id format for
// every webhook_subscriber_deliveries row (outbox fan-out, /test, redelivery).
func generateDeliveryID() string {
	// 16 bytes of entropy. The format is enforced by the rest of the
	// codebase; cryptographic strength is overkill but keeps prefix
	// width consistent with evt_/whd_/etc.
	return "whd_" + randHex16()
}

// recordFailure records a fan-out failure and schedules an aggressive
// retry. Per design §4.4 there is NO terminal 'failed' state on the
// outbox — at-least-once requires that we retry indefinitely. After
// many attempts we'd page ops; today we log and let the row stay
// pending until human intervention or a successful retry.
func (w *OutboxWorker) recordFailure(ctx context.Context, eventID string, errMsg string) {
	// Truncate to the CHECK constraint cap to avoid an INSERT failure
	// inside the failure-recording path.
	if len(errMsg) > 4000 {
		errMsg = errMsg[:4000]
	}
	cap := time.Duration(60) * time.Second
	if w.leaseDuration < cap {
		cap = w.leaseDuration
	}
	_, err := w.pool.Exec(ctx,
		`UPDATE webhook_events
		 SET attempts = attempts + 1,
		     last_error = $2,
		     next_poll_at = now() + ($3 * interval '1 second')
		 WHERE id = $1`,
		eventID, errMsg, int(cap.Seconds()),
	)
	if err != nil {
		log.Printf("[outbox-worker] recordFailure err: id=%s err=%v", eventID, err)
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// BeginFunc helper for pgxpool.Pool — pgx v5 doesn't export the v4
// shorthand, so we replicate it here. Used by fanOutOne so the tx
// boundary is implicit in the lambda's lifetime.
//
// Defined on *pgxpool.Pool via an alias is not Go-legal; instead this
// is a free function we call as w.pool.BeginFunc(ctx, fn). Wait —
// that's also not legal. Let me inline this where needed... actually
// pgx v5 does have BeginFunc-style helpers. Just use Begin + manual
// commit/rollback.
var _ = errors.New
var _ pgconn.CommandTag

// poolBeginFunc emulates pgx v4's BeginFunc against a *pgxpool.Pool
// (pgx v5 dropped the helper; we wrote our own).
func poolBeginFunc(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Compile guard to remind us we removed the BeginFunc method
// reference above (kept the helper for clarity).
var _ = json.Marshal

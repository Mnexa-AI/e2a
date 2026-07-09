package webhookpub

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Mnexa-AI/e2a/internal/telemetry"
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
//  1. LISTEN webhook_events_new — dedicated connection, sub-50ms
//     latency from trigger COMMIT to fan-out.
//  2. 1s fallback poll — catches notifications missed during deploy
//     or LISTEN reconnect.
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

// WithDeliveryEnqueuer wires River delivery (the production path). Without it,
// inserted Layer 2 rows have no delivery job — used only in tests that deliver
// pending rows by hand. DeliveryEnqueuer is defined in fanout_core.go (shared).
func (w *OutboxWorker) WithDeliveryEnqueuer(e DeliveryEnqueuer) *OutboxWorker {
	w.enqueuer = e
	return w
}

// NewOutboxWorker constructs the legacy LISTEN/NOTIFY fan-out drain. Production wiring
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
	// Publisher-lag is emitted via SetPublisherLag above; this line logs batch size +
	// tick latency so lag can also be derived from access logs.
	log.Printf("[outbox-worker-metrics] tick batch=%d elapsed_ms_so_far=%d",
		len(events), time.Since(tickStart).Milliseconds())

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

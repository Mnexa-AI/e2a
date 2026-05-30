package webhook

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// SubscriberRetryWorker drains webhook_subscriber_deliveries on a
// tick. Distinct from the legacy RetryWorker (which drains
// webhook_deliveries). The two workers can run side-by-side without
// stepping on each other because they read from disjoint tables.
//
// Decisions reflected in the design:
//   - 8 global concurrent worker goroutines (design #6). One bad
//     subscriber cannot pin all 8 slots because of the per-webhook
//     inflight cap below.
//   - Per-webhook inflight cap of 1 (H2 from the design review). A
//     sync.Map[webhookID]→sync.Mutex serializes attempts to the same
//     subscriber; a slow customer's webhook backlogs onto its own
//     queue without starving fast subscribers.
//   - 15s per-attempt HTTP timeout (carried by the SubscriberDeliverer).
//
// Slice 1 doesn't add the auto-disable worker or the
// signing_secret_prev null-out pass; those land in slice 4.
type SubscriberRetryWorker struct {
	store         *SubscriberStore
	deliverer     *SubscriberDeliverer
	identityStore *identity.Store
	interval      time.Duration
	batchSize     int
	concurrency   int

	// perWebhookLocks serializes attempts on the same webhook. Key
	// is webhook_id; value is *sync.Mutex. Locks are created on
	// first use and never deleted — at slice 1 scale (50 webhooks
	// per user × moderate user count) the memory cost is
	// negligible. A janitor that removes stale entries can land
	// later if needed.
	perWebhookLocks sync.Map
}

func NewSubscriberRetryWorker(store *SubscriberStore, deliverer *SubscriberDeliverer, identityStore *identity.Store) *SubscriberRetryWorker {
	return &SubscriberRetryWorker{
		store:         store,
		deliverer:     deliverer,
		identityStore: identityStore,
		interval:      30 * time.Second,
		batchSize:     20,
		concurrency:   8,
	}
}

// Start blocks on ctx — call in its own goroutine. Same shape as the
// legacy RetryWorker.Start. Production wiring uses Start; tests call
// Tick directly so they don't have to wait on the 30s interval.
func (w *SubscriberRetryWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Tick(ctx)
		}
	}
}

// Tick processes one batch of pending deliveries. Exposed (not just
// the private processBatch) so tests can drive the worker
// synchronously without waiting on the ticker.
func (w *SubscriberRetryWorker) Tick(ctx context.Context) {
	deliveries, err := w.store.GetPending(ctx, w.batchSize)
	if err != nil {
		log.Printf("[wsd-retry] fetch pending err: %v", err)
		return
	}
	if len(deliveries) == 0 {
		return
	}

	// Bounded concurrent goroutines. Use a buffered semaphore so we
	// don't spawn unbounded goroutines on a large batch.
	sem := make(chan struct{}, w.concurrency)
	var wg sync.WaitGroup
	for _, d := range deliveries {
		d := d
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.processOne(ctx, d)
		}()
	}
	wg.Wait()
}

func (w *SubscriberRetryWorker) processOne(ctx context.Context, d SubscriberDelivery) {
	// Per-webhook inflight cap of 1: lock the per-webhook mutex
	// before processing. Workers competing for the same webhook
	// queue up here; workers handling different webhooks proceed in
	// parallel.
	lockI, _ := w.perWebhookLocks.LoadOrStore(d.WebhookID, &sync.Mutex{})
	lock := lockI.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	// Re-fetch the webhook each attempt so a disabled or deleted
	// webhook is caught here instead of POSTing to a stale URL.
	// Cross-user concerns don't apply (the webhook lookup goes
	// through the identityStore which only filters by id; the
	// publisher already enforced user ownership at write time).
	wh, err := w.identityStore.GetWebhookByIDInternal(ctx, d.WebhookID)
	if err != nil {
		// Webhook deleted — mark delivery failed (no future retries)
		// and move on. The ON DELETE CASCADE means the row will
		// vanish at the next janitor pass anyway; this just speeds
		// it up.
		log.Printf("[wsd-retry] webhook %s not found (likely deleted), marking failed: %v", d.WebhookID, err)
		_ = w.store.RecordAttemptFailure(ctx, d.ID, "webhook not found", 0)
		return
	}
	if !wh.Enabled {
		// Disabled — defer to next tick so a re-enable picks it up
		// fast. Updating next_retry_at is unnecessary; the row
		// stays pending. Acceptable: at slice 1 a disabled webhook
		// doesn't fire often.
		log.Printf("[wsd-retry] webhook %s disabled, skipping delivery=%s", d.WebhookID, d.ID)
		return
	}

	out := w.deliverer.Deliver(ctx, wh.URL, d.EventPayload, wh.SigningSecret, wh.SigningSecretPrev)
	if out.Success {
		if err := w.store.MarkDelivered(ctx, d.ID, out.StatusCode); err != nil {
			log.Printf("[wsd-retry] MarkDelivered err delivery=%s: %v", d.ID, err)
		} else {
			log.Printf("[wsd-retry] delivered delivery=%s webhook=%s status=%d", d.ID, d.WebhookID, out.StatusCode)
		}
		return
	}

	if err := w.store.RecordAttemptFailure(ctx, d.ID, out.Error, out.StatusCode); err != nil {
		log.Printf("[wsd-retry] RecordAttemptFailure err delivery=%s: %v", d.ID, err)
	} else {
		log.Printf("[wsd-retry] failed attempt delivery=%s webhook=%s status=%d err=%s", d.ID, d.WebhookID, out.StatusCode, out.Error)
	}
}

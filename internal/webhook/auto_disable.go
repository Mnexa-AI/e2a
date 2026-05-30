package webhook

import (
	"context"
	"log"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// AutoDisableWorker scans for chronically-failing webhooks and
// disables them, and clears expired signing_secret_prev rows past
// their 24h grace window. Decision #12 in the design.
//
// The two passes share a worker because they're both cheap,
// idempotent, and run on the same low cadence.
type AutoDisableWorker struct {
	store    *identity.Store
	interval time.Duration
}

// NewAutoDisableWorker constructs the worker with the design's
// default 5-min cadence. Tests can use Tick directly.
func NewAutoDisableWorker(store *identity.Store) *AutoDisableWorker {
	return &AutoDisableWorker{
		store:    store,
		interval: 5 * time.Minute,
	}
}

// Start blocks on ctx — call in its own goroutine.
func (w *AutoDisableWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Tick(ctx)
		}
	}
}

// Tick runs both maintenance passes once. Exposed so tests can drive
// the worker synchronously without waiting on the ticker.
func (w *AutoDisableWorker) Tick(ctx context.Context) {
	if n, err := w.store.AutoDisableFailingWebhooks(ctx); err != nil {
		log.Printf("[wsd-autodisable] AutoDisableFailingWebhooks err: %v", err)
	} else if n > 0 {
		log.Printf("[wsd-autodisable] disabled %d failing webhook(s)", n)
	}
	if n, err := w.store.ClearExpiredPrevSecrets(ctx); err != nil {
		log.Printf("[wsd-autodisable] ClearExpiredPrevSecrets err: %v", err)
	} else if n > 0 {
		log.Printf("[wsd-autodisable] cleared %d expired prev secret(s)", n)
	}
}

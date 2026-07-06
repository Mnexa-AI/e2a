package webhook

import (
	"context"
	"log"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// AutoDisableWorker scans for chronically-failing webhooks and
// disables them, and clears expired signing_secret_prev rows past
// their 24h grace window. Decision #12 in the design.
//
// The two passes share a worker because they're both cheap,
// idempotent, and run on the same low cadence. The schedule is owned
// by River (webhookdelivery.MaintenanceJobs, a periodic on
// QueueMaintenance) which drives Tick; this type is the sweep body.
type AutoDisableWorker struct {
	store *identity.Store
}

// NewAutoDisableWorker constructs the sweep. River drives Tick on a
// periodic schedule; tests can call Tick directly.
func NewAutoDisableWorker(store *identity.Store) *AutoDisableWorker {
	return &AutoDisableWorker{store: store}
}

// Tick runs both maintenance passes once. Driven by the River periodic
// (and directly by tests).
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

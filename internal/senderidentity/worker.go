package senderidentity

import (
	"context"
	"errors"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// DefaultMaxReconcileAttempts bounds how long a domain may sit in `pending`
// before the reconciler gives up and marks it `failed` (the design's "no
// infinite poll" TTL). The wall-clock TTL is the sum of River's retry
// backoffs across this many attempts.
const DefaultMaxReconcileAttempts = 12

// errStillPending is returned by the reconcile worker to make River retry
// with backoff while SES is still verifying. It is an expected control-flow
// signal, not a real failure.
var errStillPending = errors.New("sending identity still pending verification")

// Store is the narrow persistence surface the workers need. *identity.Store
// satisfies it. Kept minimal so the workers don't depend on the whole store.
type Store interface {
	// SendingProvisionInputs returns the per-domain DKIM selector + PKCS#1
	// DER private key used for BYODKIM. ok=false means the domain has no key
	// material (can't provision). err is for real DB failures (retryable).
	SendingProvisionInputs(ctx context.Context, domain string) (selector string, privateKeyDER []byte, ok bool, err error)
	// SetSendingStatus writes a terminal/transition status (+ the per-axis
	// dkim/mailFrom breakdown + error + DNS records) and stamps
	// sending_last_checked_at. dkimStatus/mailFromStatus may be empty ("")
	// when the caller has no per-axis signal (e.g. provision, or a terminal
	// failure with no SES poll); persisting empty lets the read path fall back
	// to the all-or-nothing rollup.
	SetSendingStatus(ctx context.Context, domain string, status, dkimStatus, mailFromStatus Status, errMsg string, records []DNSRecord) error
	// TouchSendingChecked stamps sending_last_checked_at without changing the
	// status — used on a still-pending poll.
	TouchSendingChecked(ctx context.Context, domain string) error
	// GetSendingStatus reads the current status. Returns pgx.ErrNoRows when
	// the domain row is gone (deleted mid-flight) — workers treat that as
	// "nothing to do".
	GetSendingStatus(ctx context.Context, domain string) (Status, error)
	// DomainOwner returns the user_id owning the domain, for event routing.
	// Empty string (e.g. the system shared domain) means "no owner" → no event.
	DomainOwner(ctx context.Context, domain string) (string, error)
	// DomainExists reports whether a live domain row exists. Used by the
	// orphan reaper to decide if a provider identity is backed.
	DomainExists(ctx context.Context, domain string) (bool, error)
}

// EventFirer publishes a domain.sending_verified / domain.sending_failed
// event. Injected as a closure so this package doesn't depend on webhookpub.
// userID is the domain owner; a nil firer (tests) is a no-op.
type EventFirer func(ctx context.Context, domain, userID string, status Status, errMsg string)

// --- job args ---

type ProvisionArgs struct {
	Domain string `json:"domain"`
}

func (ProvisionArgs) Kind() string { return "sender_identity_provision" }

type ReconcileArgs struct {
	Domain string `json:"domain"`
}

func (ReconcileArgs) Kind() string { return "sender_identity_reconcile" }

type DeprovisionArgs struct {
	Domain string `json:"domain"`
}

func (DeprovisionArgs) Kind() string { return "sender_identity_deprovision" }

// --- workers ---

// ProvisionWorker registers the SES sending identity (BYODKIM) for a domain
// and, on success, enqueues a reconcile job to poll it to verified.
type ProvisionWorker struct {
	river.WorkerDefaults[ProvisionArgs]
	store               Store
	provider            Provider
	fire                EventFirer
	maxReconcileAttempt int
}

func (w *ProvisionWorker) Work(ctx context.Context, job *river.Job[ProvisionArgs]) error {
	domain := job.Args.Domain
	// Idempotency guard: POST /domains/{domain}/verify re-enqueues provisioning
	// even for an already-verified domain (forced re-check). Re-running
	// provider.Provision there returns AlreadyExists→pending, which would
	// otherwise flap a live verified domain back to pending (dropping
	// own-address From). Skip when already verified — there is nothing to do.
	if st, serr := w.store.GetSendingStatus(ctx, domain); serr == nil && st == StatusVerified {
		return nil
	} else if errors.Is(serr, pgx.ErrNoRows) {
		return nil // domain deleted mid-flight
	}
	selector, privKey, ok, err := w.store.SendingProvisionInputs(ctx, domain)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // domain deleted mid-flight; nothing to do
		}
		return err // real DB error — retry
	}
	if !ok {
		// No DKIM key material: BYODKIM is impossible. Terminal failure
		// with an actionable reason rather than an infinite retry.
		setFailedFire(ctx, w.store, w.fire, domain, "no DKIM key material for domain; re-register the domain")
		return nil
	}
	res, err := w.provider.Provision(ctx, domain, selector, privKey)
	if err != nil {
		return err // transient SES/network error — River retries
	}
	if err := w.store.SetSendingStatus(ctx, domain, res.Status, res.DkimStatus, res.MailFromStatus, res.Error, res.DNSRecords); err != nil {
		return err
	}
	switch res.Status {
	case StatusVerified:
		w.fireOwner(ctx, domain, StatusVerified, "")
		return nil
	case StatusFailed:
		w.fireOwner(ctx, domain, StatusFailed, res.Error)
		return nil
	default: // pending — enqueue the reconcile poller
		// ClientFromContextSafely (not ClientFromContext, which PANICS when
		// absent): River guarantees a client in a worked job's context, but
		// fall back to "leave pending for a forced re-check" rather than crash
		// if it's somehow missing.
		client, cerr := river.ClientFromContextSafely[pgx.Tx](ctx)
		if cerr != nil || client == nil {
			return nil
		}
		// Not unique (see Manager.EnqueueProvision): a completed reconcile from
		// a prior cycle must not block a fresh poller. ReconcileWorker is
		// idempotent — it no-ops unless the domain is still pending.
		_, err := client.Insert(ctx, ReconcileArgs{Domain: domain}, &river.InsertOpts{
			MaxAttempts: maxAttempts(w.maxReconcileAttempt),
		})
		return err
	}
}

func (w *ProvisionWorker) fireOwner(ctx context.Context, domain string, st Status, errMsg string) {
	fireOwner(ctx, w.store, w.fire, domain, st, errMsg)
}

// ReconcileWorker polls SES for a pending domain and transitions it to
// verified/failed. While still pending it returns errStillPending so River
// retries with backoff; once the attempt budget is exhausted it marks the
// domain failed (bounded TTL — no infinite poll).
type ReconcileWorker struct {
	river.WorkerDefaults[ReconcileArgs]
	store    Store
	provider Provider
	fire     EventFirer
}

func (w *ReconcileWorker) Work(ctx context.Context, job *river.Job[ReconcileArgs]) error {
	domain := job.Args.Domain
	st, err := w.store.GetSendingStatus(ctx, domain)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // domain deleted; nothing to reconcile
		}
		return err
	}
	if st != StatusPending {
		return nil // already resolved (forced re-check, dup job, etc.)
	}

	res, err := w.provider.Status(ctx, domain)
	if errors.Is(err, ErrIdentityNotFound) {
		setFailedFire(ctx, w.store, w.fire, domain, "sending identity not found at provider")
		return nil
	}
	if err != nil {
		// Transient SES/network error. Retry — UNLESS this was the last
		// attempt, in which case returning err would let River discard the
		// job and strand the domain in `pending` forever. Mark failed so the
		// TTL is absolute even when the final poll errors.
		if job.Attempt >= job.MaxAttempts {
			setFailedFire(ctx, w.store, w.fire, domain, "verification timed out")
			return nil
		}
		return err // retry (consumes an attempt)
	}

	switch res.Status {
	case StatusVerified:
		if err := w.store.SetSendingStatus(ctx, domain, StatusVerified, res.DkimStatus, res.MailFromStatus, "", res.DNSRecords); err != nil {
			return err
		}
		fireOwner(ctx, w.store, w.fire, domain, StatusVerified, "")
		return nil
	case StatusFailed:
		if err := w.store.SetSendingStatus(ctx, domain, StatusFailed, res.DkimStatus, res.MailFromStatus, res.Error, res.DNSRecords); err != nil {
			return err
		}
		fireOwner(ctx, w.store, w.fire, domain, StatusFailed, res.Error)
		return nil
	default: // still pending
		if err := w.store.TouchSendingChecked(ctx, domain); err != nil {
			return err
		}
		if job.Attempt >= job.MaxAttempts {
			setFailedFire(ctx, w.store, w.fire, domain, "verification timed out")
			return nil
		}
		return errStillPending
	}
}

// DeprovisionWorker removes the SES sending identity on domain/account
// delete. Idempotent: the provider treats a missing identity as success.
type DeprovisionWorker struct {
	river.WorkerDefaults[DeprovisionArgs]
	provider Provider
}

func (w *DeprovisionWorker) Work(ctx context.Context, job *river.Job[DeprovisionArgs]) error {
	// The domain row is already gone (deleted in the enqueuing tx); only the
	// remote identity remains. NotFound is success inside the provider.
	return w.provider.Deprovision(ctx, job.Args.Domain)
}

// --- helpers ---

func maxAttempts(n int) int {
	if n <= 0 {
		return DefaultMaxReconcileAttempts
	}
	return n
}

// setFailedFire writes a failed status and fires domain.sending_failed.
func setFailedFire(ctx context.Context, store Store, fire EventFirer, domain, reason string) {
	// Terminal failures here (no key material, identity not found at provider,
	// verification timed out) carry no per-axis signal — persist empty axes so
	// the read path falls back to the rollup (all three records read failed).
	if err := store.SetSendingStatus(ctx, domain, StatusFailed, "", "", reason, nil); err != nil {
		log.Printf("[senderidentity] set failed for %s: %v", domain, err)
		return
	}
	fireOwner(ctx, store, fire, domain, StatusFailed, reason)
}

// fireOwner looks up the domain owner and fires the event (no-op if no firer,
// no owner, or lookup fails — events are best-effort).
func fireOwner(ctx context.Context, store Store, fire EventFirer, domain string, st Status, errMsg string) {
	if fire == nil {
		return
	}
	owner, err := store.DomainOwner(ctx, domain)
	if err != nil || owner == "" {
		return
	}
	fire(ctx, domain, owner, st, errMsg)
}

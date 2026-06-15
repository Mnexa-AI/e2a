package senderidentity

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
)

// workCtxWithClient returns a context carrying a real (but DB-less) River
// client, shaped like the one River hands a live Work() call. The
// ProvisionWorker pending branch needs a client in context to enqueue the
// reconcile job. The returned cleanup closes the pool.
//
// The production pending branch uses river.ClientFromContextSafely (NOT
// ClientFromContext, which PANICS when no client is present): a missing client
// falls back to a clean no-enqueue pass — exercised by the
// "client-less ctx falls back cleanly" subtest below, which passes a bare
// context.Background().
func workCtxWithClient(t *testing.T) context.Context {
	t.Helper()
	// An unreachable DSN: we never connect, we only need a constructed client
	// so ClientFromContext returns non-nil and the pending branch proceeds.
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	client, err := river.NewClient[pgx.Tx](riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatalf("river client: %v", err)
	}
	return rivertest.WorkContext[pgx.Tx](context.Background(), client)
}

func reconcileJob(domain string, attempt, maxAttempts int) *river.Job[ReconcileArgs] {
	return &river.Job[ReconcileArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: maxAttempts, Kind: ReconcileArgs{}.Kind()},
		Args:   ReconcileArgs{Domain: domain},
	}
}

func TestReconcileWorker_Work(t *testing.T) {
	const domain = "example.com"
	const owner = "user_1"

	t.Run("already verified is a no-op", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusVerified)
		store.setOwner(domain, owner)
		prov := NewFakeProvider()
		firer := &recordingFirer{}
		w := &ReconcileWorker{store: store, provider: prov, fire: firer.fire()}

		if err := w.Work(context.Background(), reconcileJob(domain, 1, 12)); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		if len(prov.StatusCalls) != 0 {
			t.Fatalf("provider.Status should not be called, got %d calls", len(prov.StatusCalls))
		}
		if firer.count() != 0 {
			t.Fatalf("firer should not fire, got %d", firer.count())
		}
	})

	t.Run("provider verified sets verified and fires", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusPending)
		store.setOwner(domain, owner)
		prov := NewFakeProvider()
		prov.SetStatus(domain, Result{Status: StatusVerified})
		firer := &recordingFirer{}
		w := &ReconcileWorker{store: store, provider: prov, fire: firer.fire()}

		if err := w.Work(context.Background(), reconcileJob(domain, 1, 12)); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		got, ok := store.lastSetStatus()
		if !ok || got.Status != StatusVerified {
			t.Fatalf("expected status verified, got %+v ok=%v", got, ok)
		}
		ev, ok := firer.last()
		if !ok || ev.Status != StatusVerified || ev.UserID != owner {
			t.Fatalf("expected fired verified for owner, got %+v ok=%v", ev, ok)
		}
	})

	t.Run("provider failed sets failed and fires", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusPending)
		store.setOwner(domain, owner)
		prov := NewFakeProvider()
		prov.SetStatus(domain, Result{Status: StatusFailed, Error: "DKIM rejected"})
		firer := &recordingFirer{}
		w := &ReconcileWorker{store: store, provider: prov, fire: firer.fire()}

		if err := w.Work(context.Background(), reconcileJob(domain, 1, 12)); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		got, _ := store.lastSetStatus()
		if got.Status != StatusFailed || got.ErrMsg != "DKIM rejected" {
			t.Fatalf("expected failed with reason, got %+v", got)
		}
		ev, _ := firer.last()
		if ev.Status != StatusFailed || ev.ErrMsg != "DKIM rejected" {
			t.Fatalf("expected fired failed, got %+v", ev)
		}
	})

	t.Run("pending with attempts left returns retry signal", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusPending)
		store.setOwner(domain, owner)
		prov := NewFakeProvider()
		prov.SetStatus(domain, Result{Status: StatusPending})
		firer := &recordingFirer{}
		w := &ReconcileWorker{store: store, provider: prov, fire: firer.fire()}

		err := w.Work(context.Background(), reconcileJob(domain, 1, 12))
		if err == nil {
			t.Fatalf("expected retry error, got nil")
		}
		if !errors.Is(err, errStillPending) {
			t.Fatalf("expected errStillPending, got %v", err)
		}
		if len(store.TouchCalls) != 1 {
			t.Fatalf("expected TouchSendingChecked called once, got %d", len(store.TouchCalls))
		}
		if st, _ := store.GetSendingStatus(context.Background(), domain); st != StatusPending {
			t.Fatalf("status should stay pending, got %v", st)
		}
		if len(store.SetStatusCalls) != 0 {
			t.Fatalf("SetSendingStatus should not be called on a still-pending poll, got %d", len(store.SetStatusCalls))
		}
	})

	t.Run("pending with attempts exhausted times out (TTL path)", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusPending)
		store.setOwner(domain, owner)
		prov := NewFakeProvider()
		prov.SetStatus(domain, Result{Status: StatusPending})
		firer := &recordingFirer{}
		w := &ReconcileWorker{store: store, provider: prov, fire: firer.fire()}

		// Attempt >= MaxAttempts → no more retries; mark failed.
		if err := w.Work(context.Background(), reconcileJob(domain, 12, 12)); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		if len(store.TouchCalls) != 1 {
			t.Fatalf("expected TouchSendingChecked called once before TTL check, got %d", len(store.TouchCalls))
		}
		got, _ := store.lastSetStatus()
		if got.Status != StatusFailed || got.ErrMsg != "verification timed out" {
			t.Fatalf("expected failed/timeout, got %+v", got)
		}
		ev, _ := firer.last()
		if ev.Status != StatusFailed {
			t.Fatalf("expected fired failed, got %+v", ev)
		}
	})

	t.Run("identity not found sets failed and fires", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusPending)
		store.setOwner(domain, owner)
		prov := NewFakeProvider()
		prov.SetStatusNotFound(domain)
		firer := &recordingFirer{}
		w := &ReconcileWorker{store: store, provider: prov, fire: firer.fire()}

		if err := w.Work(context.Background(), reconcileJob(domain, 1, 12)); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		got, _ := store.lastSetStatus()
		if got.Status != StatusFailed || got.ErrMsg != "sending identity not found at provider" {
			t.Fatalf("expected failed/not-found reason, got %+v", got)
		}
		if ev, _ := firer.last(); ev.Status != StatusFailed {
			t.Fatalf("expected fired failed, got %+v", ev)
		}
	})

	t.Run("domain gone (ErrNoRows) is a no-op", func(t *testing.T) {
		store := newFakeStore() // domain absent ⇒ GetSendingStatus returns pgx.ErrNoRows
		prov := NewFakeProvider()
		firer := &recordingFirer{}
		w := &ReconcileWorker{store: store, provider: prov, fire: firer.fire()}

		if err := w.Work(context.Background(), reconcileJob(domain, 1, 12)); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		if len(store.SetStatusCalls) != 0 {
			t.Fatalf("nothing should be set when domain is gone, got %d", len(store.SetStatusCalls))
		}
		if len(prov.StatusCalls) != 0 {
			t.Fatalf("provider.Status should not be called when domain is gone, got %d", len(prov.StatusCalls))
		}
		if firer.count() != 0 {
			t.Fatalf("firer should not fire, got %d", firer.count())
		}
	})
}

func TestReconcileWorker_GetStatusRealError(t *testing.T) {
	store := newFakeStore()
	boom := errors.New("db down")
	store.getStatusErr = boom
	w := &ReconcileWorker{store: store, provider: NewFakeProvider()}
	err := w.Work(context.Background(), reconcileJob("example.com", 1, 12))
	if !errors.Is(err, boom) {
		t.Fatalf("expected real DB error to propagate, got %v", err)
	}
}

func TestDeprovisionWorker_Work(t *testing.T) {
	const domain = "gone.example"
	deprovJob := func() *river.Job[DeprovisionArgs] {
		return &river.Job[DeprovisionArgs]{
			JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3, Kind: DeprovisionArgs{}.Kind()},
			Args:   DeprovisionArgs{Domain: domain},
		}
	}

	t.Run("success calls provider Deprovision", func(t *testing.T) {
		prov := NewFakeProvider()
		prov.SeedIdentity(domain)
		w := &DeprovisionWorker{provider: prov}
		if err := w.Work(context.Background(), deprovJob()); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		if len(prov.DeprovisionCalls) != 1 || prov.DeprovisionCalls[0] != domain {
			t.Fatalf("expected Deprovision(%q), got %v", domain, prov.DeprovisionCalls)
		}
	})

	t.Run("provider error propagates for retry", func(t *testing.T) {
		prov := NewFakeProvider()
		boom := errors.New("ses unreachable")
		prov.SetDeprovisionErr(boom)
		w := &DeprovisionWorker{provider: prov}
		if err := w.Work(context.Background(), deprovJob()); !errors.Is(err, boom) {
			t.Fatalf("expected provider error to propagate, got %v", err)
		}
	})
}

func reapJob() *river.Job[ReapArgs] {
	return &river.Job[ReapArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 1, Kind: ReapArgs{}.Kind()},
		Args:   ReapArgs{},
	}
}

func TestReapWorker_Work(t *testing.T) {
	t.Run("flags orphan but returns nil", func(t *testing.T) {
		store := newFakeStore()
		store.setLive("a.example", true)
		store.setLive("b.example", false) // orphan: no live domain row
		prov := NewFakeProvider()
		prov.SeedIdentity("a.example")
		prov.SeedIdentity("b.example")
		w := &ReapWorker{store: store, provider: prov}

		if err := w.Work(context.Background(), reapJob()); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
	})

	t.Run("all live returns nil", func(t *testing.T) {
		store := newFakeStore()
		store.setLive("a.example", true)
		store.setLive("b.example", true)
		prov := NewFakeProvider()
		prov.SeedIdentity("a.example")
		prov.SeedIdentity("b.example")
		w := &ReapWorker{store: store, provider: prov}

		if err := w.Work(context.Background(), reapJob()); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
	})
}

func provisionJob(domain string) *river.Job[ProvisionArgs] {
	return &river.Job[ProvisionArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 12, Kind: ProvisionArgs{}.Kind()},
		Args:   ProvisionArgs{Domain: domain},
	}
}

func TestProvisionWorker_Work(t *testing.T) {
	const domain = "example.com"
	const owner = "user_1"

	t.Run("provisions ok then sets pending and attempts reconcile enqueue", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusNone)
		store.setOwner(domain, owner)
		store.setProvisionInputs("sel1", []byte("der-bytes"), true)
		prov := NewFakeProvider() // default Provision → StatusPending
		firer := &recordingFirer{}
		w := &ProvisionWorker{store: store, provider: prov, fire: firer.fire()}

		// A live Work() always has a River client in ctx; the pending branch
		// then enqueues the reconcile job. With a DB-less client the enqueue
		// fails (connection refused), which is what Work returns — but the
		// status MUST already be pending and the event MUST NOT have fired.
		err := w.Work(workCtxWithClient(t), provisionJob(domain))
		if err == nil {
			t.Fatalf("expected reconcile enqueue to fail without a live DB")
		}
		if len(prov.ProvisionCalls) != 1 {
			t.Fatalf("expected Provision called once, got %d", len(prov.ProvisionCalls))
		}
		got, ok := store.lastSetStatus()
		if !ok || got.Status != StatusPending {
			t.Fatalf("expected status pending set before enqueue, got %+v ok=%v", got, ok)
		}
		// pending should not fire an event.
		if firer.count() != 0 {
			t.Fatalf("pending should not fire, got %d", firer.count())
		}
	})

	// The pending branch uses river.ClientFromContextSafely (NOT
	// ClientFromContext, which panics when no client is present). On a
	// client-less context it must fall back to a clean no-enqueue pass: the
	// domain is left pending (provision succeeded) and Work returns nil
	// without panicking. A forced re-check later re-enqueues.
	t.Run("client-less ctx falls back cleanly (no panic, leaves pending)", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusNone)
		store.setProvisionInputs("sel1", []byte("der"), true)
		prov := NewFakeProvider()
		w := &ProvisionWorker{store: store, provider: prov}

		// Background() carries no River client → must NOT panic.
		if err := w.Work(context.Background(), provisionJob(domain)); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		if got, _ := store.GetSendingStatus(context.Background(), domain); got != StatusPending {
			t.Fatalf("status = %q, want pending (provisioned but enqueue skipped)", got)
		}
	})

	t.Run("no DKIM key material sets failed and fires", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusNone)
		store.setOwner(domain, owner)
		store.setProvisionInputs("", nil, false) // ok=false
		prov := NewFakeProvider()
		firer := &recordingFirer{}
		w := &ProvisionWorker{store: store, provider: prov, fire: firer.fire()}

		if err := w.Work(context.Background(), provisionJob(domain)); err != nil {
			t.Fatalf("Work returned error: %v", err)
		}
		if len(prov.ProvisionCalls) != 0 {
			t.Fatalf("Provision must not be called without key material, got %d", len(prov.ProvisionCalls))
		}
		got, _ := store.lastSetStatus()
		if got.Status != StatusFailed {
			t.Fatalf("expected failed, got %+v", got)
		}
		if got.ErrMsg == "" {
			t.Fatalf("expected a non-empty failure reason")
		}
		if ev, _ := firer.last(); ev.Status != StatusFailed {
			t.Fatalf("expected fired failed, got %+v", ev)
		}
	})

	t.Run("transient provision error returns error and leaves status", func(t *testing.T) {
		store := newFakeStore()
		store.setStatus(domain, StatusNone)
		store.setOwner(domain, owner)
		store.setProvisionInputs("sel1", []byte("der"), true)
		prov := NewFakeProvider()
		boom := errors.New("ses throttled")
		prov.SetProvisionErr(boom)
		firer := &recordingFirer{}
		w := &ProvisionWorker{store: store, provider: prov, fire: firer.fire()}

		if err := w.Work(context.Background(), provisionJob(domain)); !errors.Is(err, boom) {
			t.Fatalf("expected transient error to propagate, got %v", err)
		}
		if len(store.SetStatusCalls) != 0 {
			t.Fatalf("status must not change on transient error, got %d set calls", len(store.SetStatusCalls))
		}
		if st, _ := store.GetSendingStatus(context.Background(), domain); st != StatusNone {
			t.Fatalf("status should remain none, got %v", st)
		}
	})

	t.Run("provision inputs ErrNoRows is a no-op", func(t *testing.T) {
		store := newFakeStore()
		store.inputsErr = pgx.ErrNoRows
		prov := NewFakeProvider()
		w := &ProvisionWorker{store: store, provider: prov}
		if err := w.Work(context.Background(), provisionJob(domain)); err != nil {
			t.Fatalf("expected nil for deleted domain, got %v", err)
		}
		if len(prov.ProvisionCalls) != 0 || len(store.SetStatusCalls) != 0 {
			t.Fatalf("nothing should happen for a deleted domain")
		}
	})
}

// TestProvisionWorker_AlreadyVerifiedNoOp pins the review fix: re-running
// provisioning for an already-verified domain (POST /verify forced re-check
// after the unique-dedup window) must NOT call the provider or demote the
// domain back to pending.
func TestProvisionWorker_AlreadyVerifiedNoOp(t *testing.T) {
	store := newFakeStore()
	store.setStatus("acme.com", StatusVerified)
	store.setProvisionInputs("sel", []byte("der"), true)
	prov := NewFakeProvider()
	w := &ProvisionWorker{store: store, provider: prov}

	if err := w.Work(context.Background(), provisionJob("acme.com")); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(prov.ProvisionCalls) != 0 {
		t.Errorf("Provision must not run for an already-verified domain, got %d calls", len(prov.ProvisionCalls))
	}
	if got, _ := store.GetSendingStatus(context.Background(), "acme.com"); got != StatusVerified {
		t.Errorf("status = %q, want verified (no demotion)", got)
	}
}

// TestReconcileWorker_LastAttemptTransientErrorFails pins the review fix: a
// transient provider error on the FINAL attempt must mark the domain failed
// (absolute TTL) rather than return an error that River discards, stranding
// the domain in pending forever. A non-last attempt still retries.
func TestReconcileWorker_LastAttemptTransientErrorFails(t *testing.T) {
	store := newFakeStore()
	store.setStatus("acme.com", StatusPending)
	store.setOwner("acme.com", "u1")
	prov := NewFakeProvider()
	prov.SetStatusErr("acme.com", errors.New("ses throttled"))
	firer := &recordingFirer{}
	w := &ReconcileWorker{store: store, provider: prov, fire: firer.fire()}

	// Final attempt: must NOT return an error and must set failed.
	if err := w.Work(context.Background(), reconcileJob("acme.com", 5, 5)); err != nil {
		t.Fatalf("Work on last attempt returned err (would strand pending): %v", err)
	}
	if got, _ := store.GetSendingStatus(context.Background(), "acme.com"); got != StatusFailed {
		t.Errorf("status = %q, want failed after last-attempt transient error", got)
	}
	if firer.count() == 0 {
		t.Error("expected domain.sending_failed to fire on TTL timeout")
	}

	// Non-final attempt: must return the error to trigger a retry.
	store.setStatus("acme.com", StatusPending)
	if err := w.Work(context.Background(), reconcileJob("acme.com", 1, 5)); err == nil {
		t.Error("expected retry error on a non-final transient error")
	}
}

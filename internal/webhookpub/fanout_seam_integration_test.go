package webhookpub_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5"
)

// fakeFanOutSeamEnq implements webhookpub.FanOutEnqueuer for the outbox seam tests. It
// records the event ids it was asked to enqueue and (optionally) fails, to exercise the
// best-effort savepoint path.
type fakeFanOutSeamEnq struct {
	ids    []string
	err    error
	poison bool // if set, run a failing DB statement on the tx (aborting the savepoint subtx) before returning err
	n      int64
}

func (f *fakeFanOutSeamEnq) EnqueueFanOutTx(ctx context.Context, tx pgx.Tx, eventID string) (int64, error) {
	if f.poison {
		// Simulate a REAL DB failure inside the fan-out enqueue (e.g. a failed river_job
		// insert): this aborts the savepoint subtransaction, so the caller MUST issue
		// ROLLBACK TO SAVEPOINT to recover the parent tx. A fake that only returns an
		// error (no DB op) would not exercise that recovery.
		_, _ = tx.Exec(ctx, `SELECT * FROM a_table_that_does_not_exist_xyz`)
	}
	if f.err != nil {
		return 0, f.err
	}
	f.ids = append(f.ids, eventID)
	f.n++
	return f.n, nil
}

// TestOutbox_Integration_PublishTx_EnqueuesFanOut: with a fan-out enqueuer wired
// (river mode), PublishTx writes the event AND enqueues a fan-out job in the same tx,
// stamping fanout_job_id. A dedup re-publish (same deterministic id → ON CONFLICT
// no-op) enqueues nothing more.
func TestOutbox_Integration_PublishTx_EnqueuesFanOut(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	const userID = "u_fanout_seam_tx"
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name, google_subject, created_at)
	                              VALUES ($1, $2, 'Test', $1, now()) ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	outbox := webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true))
	enq := &fakeFanOutSeamEnq{}
	outbox.SetFanOutEnqueuer(enq)

	event := webhookpub.Event{
		ID:     webhookpub.DeterministicEventID("msg_seam_tx", webhookpub.EventEmailReceived),
		Type:   webhookpub.EventEmailReceived,
		UserID: userID,
	}

	// First publish: real insert → enqueue + stamp.
	tx, _ := pool.Begin(ctx)
	if err := outbox.PublishTx(ctx, tx, event); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("PublishTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if len(enq.ids) != 1 || enq.ids[0] != event.ID {
		t.Fatalf("enqueued ids = %v, want [%s]", enq.ids, event.ID)
	}
	var jobID *int64
	if err := pool.QueryRow(ctx, `SELECT fanout_job_id FROM webhook_events WHERE id = $1`, event.ID).Scan(&jobID); err != nil {
		t.Fatalf("read fanout_job_id: %v", err)
	}
	if jobID == nil {
		t.Errorf("fanout_job_id = nil, want stamped")
	}

	// Second publish of the SAME id: ON CONFLICT no-op → inserted=false → no enqueue.
	tx2, _ := pool.Begin(ctx)
	if err := outbox.PublishTx(ctx, tx2, event); err != nil {
		_ = tx2.Rollback(ctx)
		t.Fatalf("PublishTx (dedup): %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("commit (dedup): %v", err)
	}
	if len(enq.ids) != 1 {
		t.Errorf("after dedup re-publish, enqueue calls = %d, want 1", len(enq.ids))
	}
}

// TestOutbox_Integration_PublishBestEffort_SavepointProtectsTx pins the load-bearing
// guarantee: a fan-out enqueue FAILURE on the post-side-effect path must NOT poison the
// caller's must-commit tx (SES already sent). The event row still commits; fanout_job_id
// stays NULL for the reconciler to re-drive.
func TestOutbox_Integration_PublishBestEffort_SavepointProtectsTx(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	const userID = "u_fanout_seam_be"
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name, google_subject, created_at)
	                              VALUES ($1, $2, 'Test', $1, now()) ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	outbox := webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true))
	// poison=true makes the enqueue run a failing statement on the savepoint FIRST,
	// aborting the subtransaction — the realistic river_job-insert-failed case, which
	// the parent tx must survive via ROLLBACK TO SAVEPOINT.
	outbox.SetFanOutEnqueuer(&fakeFanOutSeamEnq{poison: true, err: errors.New("river insert failed")})

	event := webhookpub.Event{
		ID:     webhookpub.DeterministicEventID("msg_seam_be", webhookpub.EventEmailSent),
		Type:   webhookpub.EventEmailSent,
		UserID: userID,
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// The enqueue will fail inside the savepoint; PublishBestEffortTx must swallow it.
	wrote := outbox.PublishBestEffortTx(ctx, tx, event)
	if !wrote {
		t.Fatalf("PublishBestEffortTx wrote=false, want true (row written despite enqueue failure)")
	}
	// The caller's tx must still commit cleanly — the savepoint rollback left it usable.
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit after failed fan-out enqueue: %v (tx was poisoned — savepoint failed)", err)
	}

	// The event row is durably persisted with NO fan-out job — the reconciler's target.
	var jobID *int64
	var status string
	if err := pool.QueryRow(ctx, `SELECT fanout_job_id, status FROM webhook_events WHERE id = $1`, event.ID).Scan(&jobID, &status); err != nil {
		t.Fatalf("read event: %v (row missing → best-effort lost the event)", err)
	}
	if jobID != nil {
		t.Errorf("fanout_job_id = %v, want nil (enqueue failed)", *jobID)
	}
	if status != "pending" {
		t.Errorf("status = %q, want pending (reconciler re-drives)", status)
	}
}

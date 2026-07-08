package inboundprocess_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/inboundprocess"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// fakeEnq is a minimal jobs.Enqueuer that hands back an increasing job id without a
// live River client — enough to exercise ReconcilePending's stamp path.
type fakeEnq struct{ n int64 }

func (f *fakeEnq) Insert(_ context.Context, _ river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.n++
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: f.n}}, nil
}

func (f *fakeEnq) InsertTx(_ context.Context, _ pgx.Tx, _ river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.n++
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: f.n}}, nil
}

// TestReconcilePending covers the startup cutover: an accepted intake row with no job
// (a crash between insert and enqueue, or a pre-async row) gets a job stamped, and a
// re-run does not re-enqueue it (idempotent via the process_job_id IS NULL guard).
func TestReconcilePending(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	// Plant an accepted intake with NO job (stamp step skipped).
	id := identity.NewInboundIntakeID()
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, e := store.InsertInboundIntakeTx(ctx, tx, id, "bot@recon.test", "a@b.test", "1.2.3.4", "<recon@b.test>", "h", []byte("raw"))
		return e
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	j := inboundprocess.NewJobs(store)
	j.SetEnqueuer(&fakeEnq{})

	n, err := j.ReconcilePending(ctx, pool)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n < 1 {
		t.Fatalf("reconcile should enqueue at least the stranded row, got %d", n)
	}
	var jobID *int64
	if err := pool.QueryRow(ctx, `SELECT process_job_id FROM inbound_intake WHERE id=$1`, id).Scan(&jobID); err != nil {
		t.Fatalf("read job id: %v", err)
	}
	if jobID == nil {
		t.Fatal("reconcile must stamp a job id on the stranded row")
	}
	stamped := *jobID

	// Idempotent: a re-run must not re-enqueue this row (its job id is unchanged).
	if _, err := j.ReconcilePending(ctx, pool); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	var jobID2 *int64
	if err := pool.QueryRow(ctx, `SELECT process_job_id FROM inbound_intake WHERE id=$1`, id).Scan(&jobID2); err != nil {
		t.Fatalf("read job id 2: %v", err)
	}
	if jobID2 == nil || *jobID2 != stamped {
		t.Fatalf("re-run must not re-enqueue an already-stamped row: was %d, now %v", stamped, jobID2)
	}
}

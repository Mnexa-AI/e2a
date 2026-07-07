package outboundsend_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/jobs"
	"github.com/Mnexa-AI/e2a/internal/outboundsend"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestReconcilePending_EnqueuesStrandedAndStamps stands up a REAL River client and
// asserts the slice-C startup cutover: an accepted message with send_job_id IS NULL
// (stranded by an accept-tx that crashed before the job committed) gets an
// outbound_send river_job carrying its message id, its send_job_id stamped, and a
// re-run is idempotent (no second job). Store/Deliverer are nil — the reconciler
// never runs the worker, only the enqueue path.
func TestReconcilePending_EnqueuesStrandedAndStamps(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("jobs.Migrate: %v", err)
	}
	store := identity.NewStore(pool)

	// Seed a verified agent + one accepted outbound message with NO send job.
	user, err := store.CreateOrGetUser(ctx, "owner-recon@example.com", "Owner", "google-recon")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	domain := "recon.example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	ag, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	var msgID string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		m, err := store.CreateOutboundMessageTx(ctx, tx, ag.ID,
			[]string{"a@gmail.com"}, nil, nil, "S", "send", "smtp", "", "conv-recon",
			[]byte("raw"), "accepted", "agent@test.e2a.dev", "relay")
		msgID = m.ID
		return err // NB: no StampSendJobIDTx → send_job_id stays NULL (stranded)
	}); err != nil {
		t.Fatalf("accept tx: %v", err)
	}

	// Build the integration on a real shared River client so EnqueueSendTx inserts
	// an actual river_job row. Store/Deliverer unused by the reconcile path.
	j := outboundsend.NewJobs(nil, nil)
	client, err := jobs.New(pool, jobs.Config{}, j)
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	j.SetEnqueuer(client)

	n, err := j.ReconcilePending(ctx, pool)
	if err != nil {
		t.Fatalf("ReconcilePending: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReconcilePending enqueued %d, want 1", n)
	}

	var jobCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE kind='outbound_send' AND args->>'message_id' = $1`,
		msgID).Scan(&jobCount); err != nil {
		t.Fatalf("count river_job: %v", err)
	}
	if jobCount != 1 {
		t.Errorf("outbound_send river_job for %s = %d, want 1", msgID, jobCount)
	}
	var sendJobID *int64
	if err := pool.QueryRow(ctx, `SELECT send_job_id FROM messages WHERE id=$1`, msgID).Scan(&sendJobID); err != nil {
		t.Fatalf("read send_job_id: %v", err)
	}
	if sendJobID == nil {
		t.Errorf("send_job_id not stamped after ReconcilePending")
	}

	// Idempotent: a second cutover pass enqueues nothing more.
	if n2, err := j.ReconcilePending(ctx, pool); err != nil || n2 != 0 {
		t.Errorf("second ReconcilePending = (%d, %v), want (0, nil)", n2, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE kind='outbound_send' AND args->>'message_id' = $1`,
		msgID).Scan(&jobCount); err != nil {
		t.Fatalf("recount river_job: %v", err)
	}
	if jobCount != 1 {
		t.Errorf("after re-run: river_job for %s = %d, want 1 (idempotent)", msgID, jobCount)
	}
}

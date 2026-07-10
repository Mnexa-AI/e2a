package hitlnotify_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/hitlnotify"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/jobs"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestReconcilePending_EnqueuesStrandedAndStamps stands up a REAL River client and
// asserts the startup cutover: a pending_review message with notify_job_id IS NULL
// (held before the feature shipped, or stranded by an accept-tx that crashed before
// the job committed) gets a hitl_notify river_job carrying its message id, its
// notify_job_id stamped, and a re-run is idempotent. The Deliverer is nil — the
// reconciler only exercises the enqueue path, never the worker.
func TestReconcilePending_EnqueuesStrandedAndStamps(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("jobs.Migrate: %v", err)
	}
	store := identity.NewStore(pool)

	// Seed a verified agent + one pending_review message with NO notify job (the
	// plain CreatePendingOutboundMessage path leaves notify_job_id NULL).
	user, err := store.CreateOrGetUser(ctx, "owner-notify-recon@example.com", "Owner", "google-notify-recon")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	domain := "notify-recon.example.com"
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
	msg, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{"a@gmail.com"}, nil, nil, "Subject", "body", "", nil,
		"send", "conv-notify-recon", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}
	msgID := msg.ID

	// Build the integration on a real shared River client so EnqueueNotifyTx inserts
	// an actual river_job row. Deliverer unused by the reconcile path.
	j := hitlnotify.NewJobs(store)
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
		`SELECT count(*) FROM river_job WHERE kind='hitl_notify' AND args->>'message_id' = $1`,
		msgID).Scan(&jobCount); err != nil {
		t.Fatalf("count river_job: %v", err)
	}
	if jobCount != 1 {
		t.Errorf("hitl_notify river_job for %s = %d, want 1", msgID, jobCount)
	}
	var notifyJobID *int64
	if err := pool.QueryRow(ctx, `SELECT notify_job_id FROM messages WHERE id=$1`, msgID).Scan(&notifyJobID); err != nil {
		t.Fatalf("read notify_job_id: %v", err)
	}
	if notifyJobID == nil {
		t.Errorf("notify_job_id not stamped after ReconcilePending")
	}

	// Idempotent: a second cutover pass enqueues nothing more.
	if n2, err := j.ReconcilePending(ctx, pool); err != nil || n2 != 0 {
		t.Errorf("second ReconcilePending = (%d, %v), want (0, nil)", n2, err)
	}

	// Cutover safety: a pending_review hold already stamped notified_at (either by
	// the old code path at deploy, seeded in migration 057, or a prior send) must be
	// SKIPPED — never re-notified. Seed one and assert the reconciler ignores it.
	already, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{"b@gmail.com"}, nil, nil, "Already", "body", "", nil,
		"send", "conv-already", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage(already): %v", err)
	}
	if err := store.MarkMessageNotified(ctx, already.ID); err != nil {
		t.Fatalf("MarkMessageNotified: %v", err)
	}
	if n3, err := j.ReconcilePending(ctx, pool); err != nil || n3 != 0 {
		t.Errorf("ReconcilePending over an already-notified hold = (%d, %v), want (0, nil)", n3, err)
	}
	var strayJobs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE kind='hitl_notify' AND args->>'message_id' = $1`,
		already.ID).Scan(&strayJobs); err != nil {
		t.Fatalf("count river_job(already): %v", err)
	}
	if strayJobs != 0 {
		t.Errorf("an already-notified hold must not be enqueued, got %d job(s)", strayJobs)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE kind='hitl_notify' AND args->>'message_id' = $1`,
		msgID).Scan(&jobCount); err != nil {
		t.Fatalf("recount river_job: %v", err)
	}
	if jobCount != 1 {
		t.Errorf("after re-run: river_job for %s = %d, want 1 (idempotent)", msgID, jobCount)
	}
}

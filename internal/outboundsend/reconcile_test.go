package outboundsend_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/jobs"
	"github.com/Mnexa-AI/e2a/internal/outboundsend"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

func TestTerminalReconcileIndex(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	var (
		valid      bool
		keyColumns int
		allColumns int
		definition string
		predicate  string
	)
	if err := pool.QueryRow(ctx,
		`SELECT i.indisvalid, i.indnkeyatts, i.indnatts,
		        pg_get_indexdef(i.indexrelid), pg_get_expr(i.indpred, i.indrelid)
		   FROM pg_class c
		   JOIN pg_index i ON i.indexrelid = c.oid
		  WHERE c.relname = 'idx_messages_outbound_terminal_reconcile'`,
	).Scan(&valid, &keyColumns, &allColumns, &definition, &predicate); err != nil {
		t.Fatalf("read terminal reconcile index: %v", err)
	}
	if !valid {
		t.Error("terminal reconcile index is invalid")
	}
	if keyColumns != 2 || allColumns != 3 {
		t.Errorf("terminal reconcile index columns = (%d key, %d total), want (2 key, 3 total)", keyColumns, allColumns)
	}
	for _, want := range []string{"(created_at, id)", "INCLUDE (send_job_id)"} {
		if !strings.Contains(definition, want) {
			t.Errorf("terminal reconcile index definition %q missing %q", definition, want)
		}
	}
	for _, want := range []string{"direction = 'outbound'", "delivery_status", "'accepted'", "'sending'", "send_job_id IS NOT NULL"} {
		if !strings.Contains(predicate, want) {
			t.Errorf("terminal reconcile index predicate %q missing %q", predicate, want)
		}
	}
}

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
	j := outboundsend.NewJobs(nil, nil, pool)
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

type terminalFixture struct {
	pool    *pgxpool.Pool
	store   *identity.Store
	agentID string
	jobs    *outboundsend.Jobs
}

func newTerminalFixture(t *testing.T, pool *pgxpool.Pool, store *identity.Store, outboundStore outboundsend.Store) *terminalFixture {
	t.Helper()
	if pool == nil {
		pool = testutil.TestDB(t)
		store = identity.NewStore(pool)
	}
	ctx := context.Background()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("jobs.Migrate: %v", err)
	}
	user, err := store.CreateOrGetUser(ctx, "owner-terminal@example.com", "Owner", "google-terminal")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	domain := "terminal.example.com"
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

	j := outboundsend.NewJobs(outboundStore, nil, pool)
	client, err := jobs.New(pool, jobs.Config{}, j)
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	j.SetEnqueuer(client)
	return &terminalFixture{pool: pool, store: store, agentID: ag.ID, jobs: j}
}

func (f *terminalFixture) seed(t *testing.T, label, messageStatus, jobState string, missing bool) string {
	t.Helper()
	ctx := context.Background()
	var messageID string
	var jobID int64
	if err := f.store.WithTx(ctx, func(tx pgx.Tx) error {
		m, err := f.store.CreateOutboundMessageTx(ctx, tx, f.agentID,
			[]string{label + "@gmail.com"}, nil, nil, label, "send", "smtp", "", "conv-"+label,
			[]byte("raw"), "accepted", "bot@terminal.example.com", "relay")
		if err != nil {
			return err
		}
		messageID = m.ID
		jobID, err = f.jobs.EnqueueSendTx(ctx, tx, messageID)
		if err != nil {
			return err
		}
		if err := f.store.StampSendJobIDTx(ctx, tx, messageID, jobID); err != nil {
			return err
		}
		if messageStatus == "sent" {
			if _, err = tx.Exec(ctx, `UPDATE messages SET delivery_status='sending' WHERE id=$1`, messageID); err != nil {
				return err
			}
			_, err = f.store.MarkOutboundSentTx(ctx, tx, messageID, "<provider-"+label+">")
		}
		return err
	}); err != nil {
		t.Fatalf("seed %s: %v", label, err)
	}

	if missing {
		if _, err := f.pool.Exec(ctx, `DELETE FROM river_job WHERE id=$1`, jobID); err != nil {
			t.Fatalf("prune job for %s: %v", label, err)
		}
	} else if jobState != "" {
		query := `UPDATE river_job SET state=$2, finalized_at=NULL WHERE id=$1`
		if jobState == "cancelled" || jobState == "discarded" || jobState == "completed" {
			query = `UPDATE river_job SET state=$2, finalized_at=now() WHERE id=$1`
		}
		if _, err := f.pool.Exec(ctx, query, jobID, jobState); err != nil {
			t.Fatalf("set job %s state to %s: %v", label, jobState, err)
		}
	}
	return messageID
}

func (f *terminalFixture) status(t *testing.T, messageID string) (string, string) {
	t.Helper()
	var status, detail string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT delivery_status, COALESCE(delivery_detail,'') FROM messages WHERE id=$1`, messageID,
	).Scan(&status, &detail); err != nil {
		t.Fatalf("read message %s: %v", messageID, err)
	}
	return status, detail
}

func (f *terminalFixture) failedEventCount(t *testing.T, messageID string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM webhook_events WHERE id=$1`,
		webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailFailed),
	).Scan(&count); err != nil {
		t.Fatalf("count email.failed for %s: %v", messageID, err)
	}
	return count
}

func TestTerminalReconcileWorker_ReconcilesOnlyTerminalJobs(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	adapter := agent.NewOutboundSendStore(store,
		webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)), usage.NewNoopUsageTracker())

	f := newTerminalFixture(t, pool, store, adapter)
	discardedID := f.seed(t, "discarded", "accepted", "discarded", false)
	cancelledID := f.seed(t, "cancelled", "accepted", "cancelled", false)
	completedID := f.seed(t, "completed", "accepted", "completed", false)
	retryableID := f.seed(t, "retryable", "accepted", "retryable", false)
	sentID := f.seed(t, "sent", "sent", "completed", false)
	missingID := f.seed(t, "missing", "accepted", "", true)

	worker := outboundsend.NewTerminalReconcileWorker(pool, adapter)
	if err := worker.Work(context.Background(), &river.Job[outboundsend.TerminalReconcileArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	for _, tc := range []struct {
		name, id, wantStatus, wantState string
	}{
		{"discarded", discardedID, "failed", "discarded"},
		{"cancelled", cancelledID, "failed", "cancelled"},
		{"completed", completedID, "failed", "completed"},
		{"retryable", retryableID, "accepted", ""},
		{"sent", sentID, "sent", ""},
		{"missing", missingID, "failed", "missing"},
	} {
		status, detail := f.status(t, tc.id)
		if status != tc.wantStatus {
			t.Errorf("%s status = %q, want %q", tc.name, status, tc.wantStatus)
		}
		if tc.wantState != "" {
			wantDetail := "outbound send job " + tc.wantState + " before terminal delivery status was recorded"
			if detail != wantDetail {
				t.Errorf("%s detail = %q, want %q", tc.name, detail, wantDetail)
			}
		}
	}
	for _, id := range []string{discardedID, cancelledID, completedID, missingID} {
		if got := f.failedEventCount(t, id); got != 1 {
			t.Errorf("email.failed count for %s = %d, want 1", id, got)
		}
	}
	for _, id := range []string{retryableID, sentID} {
		if got := f.failedEventCount(t, id); got != 0 {
			t.Errorf("email.failed count for untouched %s = %d, want 0", id, got)
		}
	}

	if err := worker.Work(context.Background(), &river.Job[outboundsend.TerminalReconcileArgs]{}); err != nil {
		t.Fatalf("second Work: %v", err)
	}
	for _, id := range []string{discardedID, cancelledID, completedID, missingID} {
		if got := f.failedEventCount(t, id); got != 1 {
			t.Errorf("email.failed count after second pass for %s = %d, want 1", id, got)
		}
	}
}

type failingTerminalStore struct{ err error }

func (s failingTerminalStore) ClaimSend(context.Context, string, int64) (*outboundsend.SendJob, error) {
	return nil, nil
}
func (s failingTerminalStore) ReleaseSend(context.Context, string, int64) error       { return nil }
func (s failingTerminalStore) MarkSent(context.Context, string, string, string) error { return nil }
func (s failingTerminalStore) MarkFailed(context.Context, string, int, string) error {
	return s.err
}

func TestTerminalReconcileWorker_PropagatesStoreFailure(t *testing.T) {
	sentinel := errors.New("mark failed unavailable")
	f := newTerminalFixture(t, nil, nil, failingTerminalStore{err: sentinel})
	f.seed(t, "store-error", "accepted", "discarded", false)

	worker := outboundsend.NewTerminalReconcileWorker(f.pool, failingTerminalStore{err: sentinel})
	err := worker.Work(context.Background(), &river.Job[outboundsend.TerminalReconcileArgs]{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Work error = %v, want %v", err, sentinel)
	}
}

func TestRegisterJobs_RegistersTerminalReconcilePeriodic(t *testing.T) {
	j := outboundsend.NewJobs(nil, nil, nil)
	periodics := j.RegisterJobs(river.NewWorkers())
	if len(periodics) != 1 {
		t.Fatalf("RegisterJobs periodics = %d, want 1", len(periodics))
	}
}

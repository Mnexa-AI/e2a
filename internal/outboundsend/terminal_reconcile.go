package outboundsend

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tokencanopy/e2a/internal/delivery"
	"github.com/tokencanopy/e2a/internal/jobs"
)

const terminalReconcileInterval = time.Minute

// providerEvidenceGrace is how long after its send job reached a terminal
// state (river_job.finalized_at) an accepted/sending row without recorded
// provider-accept evidence is left alone before being declared failed. A
// final attempt that crashed AFTER the SMTP accept never captured a provider
// id, and its SES Send/Delivery notification (carrying the X-E2A-Message-ID
// echo) typically lands within seconds-to-minutes — the grace gives that
// evidence time to arrive so the reconciler settles the row as sent instead
// of firing a false, hard-to-correct email.failed. Rows WITH evidence are
// settled immediately; a pruned/missing job is by definition long past any
// grace (River retains terminal jobs for hours before pruning). The cost of
// the grace is a ≤ ~grace+interval delay on the email.failed for a genuinely
// abandoned job — a deliberate trade against false terminal failures
// (async-send-contract §3.1).
const providerEvidenceGrace = 15 * time.Minute

// TerminalReconcileArgs drives the periodic safety net for outbound messages
// whose stamped send job reached a terminal state before recording delivery.
type TerminalReconcileArgs struct{}

func (TerminalReconcileArgs) Kind() string { return "outbound_terminal_reconcile" }

// TerminalReconcileWorker settles accepted/sending outbound messages after
// their stamped River job is terminal or has already been pruned. SendWorker
// is still the primary owner; the compare-and-set store transitions make races
// safe. The store's guarded MarkFailed is the single terminal write: a row
// with provider-accept evidence is settled as sent (+ email.sent), a row
// without evidence — once past the providerEvidenceGrace window — is declared
// failed with provenance 'local' (correctable, §3.1) + exactly one
// email.failed.
type TerminalReconcileWorker struct {
	river.WorkerDefaults[TerminalReconcileArgs]
	pool  *pgxpool.Pool
	store Store
	ramp  RampGate
}

// NewTerminalReconcileWorker builds the periodic safety-net worker.
func NewTerminalReconcileWorker(pool *pgxpool.Pool, store Store, ramps ...RampGate) *TerminalReconcileWorker {
	w := &TerminalReconcileWorker{pool: pool, store: store}
	if len(ramps) > 0 {
		w.ramp = ramps[0]
	}
	return w
}

type terminalCandidate struct {
	messageID string
	attempt   int
	state     string
}

func (w *TerminalReconcileWorker) Work(ctx context.Context, _ *river.Job[TerminalReconcileArgs]) error {
	// Candidates: outbound rows still pre-terminal whose stamped job can never
	// run again. Processed immediately when provider-accept evidence exists
	// (settled as sent) or when the job has been terminal past the grace
	// window / was already pruned; a freshly-terminal row without evidence is
	// skipped this pass so in-flight SES notifications can still arrive.
	rows, err := w.pool.Query(ctx,
		`SELECT m.id,
		        COALESCE(r.attempt, 0),
		        CASE WHEN r.id IS NULL THEN 'missing' ELSE r.state::text END
		   FROM messages m
		   LEFT JOIN river_job r ON r.id = m.send_job_id
		  WHERE m.direction = 'outbound'
		    AND m.delivery_status IN ('accepted', 'sending')
		    AND m.send_job_id IS NOT NULL
		    AND (r.id IS NULL OR r.state IN ('cancelled', 'discarded', 'completed'))
		    AND ( m.provider_accepted_at IS NOT NULL
		       OR r.id IS NULL
		       OR COALESCE(r.finalized_at, to_timestamp(0)) <= now() - make_interval(secs => $2) )
		  ORDER BY m.created_at ASC, m.id ASC
		  LIMIT $1`,
		jobs.DefaultReconcileBatch, providerEvidenceGrace.Seconds(),
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	candidates := make([]terminalCandidate, 0)
	for rows.Next() {
		var candidate terminalCandidate
		if err := rows.Scan(&candidate.messageID, &candidate.attempt, &candidate.state); err != nil {
			return err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	processed := 0
	for _, candidate := range candidates {
		detail := fmt.Sprintf("outbound send job %s before terminal delivery status was recorded", candidate.state)
		// MarkFailed is the guarded terminal write: it settles the row as sent
		// when provider-accept evidence exists (never a false failure), else
		// fails it with provenance 'local' so later authoritative evidence can
		// still correct it. The stored detail of a deferred final attempt is
		// preferred over this generic sweep detail.
		if err := w.store.MarkFailed(ctx, candidate.messageID, candidate.attempt, detail, delivery.FailureSourceLocal); err != nil {
			if processed > 0 {
				log.Printf("[outbound-terminal-reconcile] processed %d candidates", processed)
			}
			return err
		}
		if w.ramp != nil {
			if err := w.ramp.Resolve(ctx, candidate.messageID); err != nil {
				return fmt.Errorf("resolve sending ramp for %s: %w", candidate.messageID, err)
			}
		}
		processed++
	}
	if processed > 0 {
		log.Printf("[outbound-terminal-reconcile] processed %d candidates", processed)
	}
	return nil
}

func terminalReconcilePeriodicConstructor() (river.JobArgs, *river.InsertOpts) {
	return TerminalReconcileArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance}
}

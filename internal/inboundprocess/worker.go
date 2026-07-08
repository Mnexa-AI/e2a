// Package inboundprocess is Layer 3 of the queue-first inbound pipeline
// (docs/design/inbound-message-pipeline-river.md): the River execution stage that
// takes an accepted inbound_intake row and runs the full processing chain — parse,
// SPF/DKIM, HMAC sign, ingestion gate, content screening, persist, and event publish
// — off the SMTP critical path. It mirrors internal/outboundsend: a River Worker on
// the shared `inbound` queue, with River owning claim / retry / rescue.
//
// At-least-once from the SMTP edge: the intake row is committed (and the job
// enqueued) in the same tx before 250, so an accepted message is never lost. The
// worker's terminal write flips intake.status='processed' ATOMICALLY with the
// messages insert + event publish (inside processInbound's tx), so a crash re-drive
// finds 'processed' and no-ops — the idempotency gate.
//
// One processing pass per job attempt — River owns the multi-attempt envelope via
// NextRetry, so Work() stays a single ProcessIntake call, not an internal loop.
package inboundprocess

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// inboundRetryBackoffs is the per-attempt delay for a failed processing attempt.
// River drives it via NextRetry, indexed by attempt. Inbound processing errors are
// transient (DB persist / agent resolve), so the tail is a bounded retry, not the
// outage/permanent split the outbound sender needs (there is no upstream provider
// call except the fail-open Gemini screen).
var inboundRetryBackoffs = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
	4 * time.Hour,
}

// MaxInboundAttempts caps the retry tail before the intake is marked failed.
const MaxInboundAttempts = 6

// InboundProcessArgs drives one inbound processing job. Args carry only the intake
// id; the worker re-reads the inbound_intake row (the source of truth) each attempt.
type InboundProcessArgs struct {
	IntakeID string `json:"intake_id"`
}

func (InboundProcessArgs) Kind() string { return "inbound_process" }

// Processor runs the full inbound chain for one intake row and marks the intake
// processed atomically with the messages insert. Implemented over the relay Server
// (which owns processInbound) in the binary.
type Processor interface {
	ProcessIntake(ctx context.Context, intake *identity.InboundIntake) error
}

// Store is the intake surface the worker + reconciler need. Implemented over
// internal/identity.
type Store interface {
	// LoadInboundIntake returns the row, or (nil, nil) if pruned — a no-op.
	LoadInboundIntake(ctx context.Context, intakeID string) (*identity.InboundIntake, error)
	// MarkInboundIntakeFailed records a terminal failure after the retry tail.
	MarkInboundIntakeFailed(ctx context.Context, intakeID, detail string) error
	// StampInboundIntakeJobIDTx records the job id on a reconciled row.
	StampInboundIntakeJobIDTx(ctx context.Context, tx pgx.Tx, intakeID string, jobID int64) error
	// PruneProcessedIntake deletes processed rows older than olderThan (retention).
	PruneProcessedIntake(ctx context.Context, olderThan time.Duration) (int64, error)
}

// InboundProcessWorker processes an accepted intake row. Mirrors
// outboundsend.SendWorker.
type InboundProcessWorker struct {
	river.WorkerDefaults[InboundProcessArgs]
	store     Store
	processor Processor
}

func NewInboundProcessWorker(store Store, processor Processor) *InboundProcessWorker {
	return &InboundProcessWorker{store: store, processor: processor}
}

// NextRetry overrides River's default backoff with the decided inbound envelope.
func (w *InboundProcessWorker) NextRetry(job *river.Job[InboundProcessArgs]) time.Time {
	i := job.Attempt
	if i < 0 || i >= len(inboundRetryBackoffs) {
		return time.Time{} // fall back to River's default at the tail
	}
	return time.Now().Add(inboundRetryBackoffs[i])
}

func (w *InboundProcessWorker) Work(ctx context.Context, job *river.Job[InboundProcessArgs]) error {
	it, err := w.store.LoadInboundIntake(ctx, job.Args.IntakeID)
	if err != nil {
		return err // DB error — retryable
	}
	if it == nil {
		return nil // intake pruned — nothing to do
	}
	if it.Status != identity.IntakeStatusAccepted {
		return nil // already processed/failed — idempotent re-drive no-op
	}

	if err := w.processor.ProcessIntake(ctx, it); err != nil {
		if errors.Is(err, identity.ErrIntakeAlreadyProcessed) {
			return nil // a concurrent/prior attempt already processed it — done
		}
		if errors.Is(err, identity.ErrRecipientGone) {
			// Recipient's agent was deleted between accept and processing. Mark the
			// intake terminally (not a retry) so it doesn't linger 'accepted' forever
			// with orphaned raw MIME; the message is dropped (nothing to deliver).
			if ferr := w.store.MarkInboundIntakeFailed(ctx, it.ID, "recipient agent no longer exists"); ferr != nil {
				log.Printf("[inbound-process] mark-gone for %s: %v", it.ID, ferr)
			}
			return nil
		}
		// Processing errors are transient (DB persist / agent resolve). Retry within
		// the bounded envelope; on exhaustion, mark the intake failed for ops
		// visibility — we already returned 250, so the message is dropped, not lost
		// silently. (A genuinely-undeliverable body doesn't error here: parsing is
		// best-effort and screening fails open.)
		if job.Attempt >= MaxInboundAttempts {
			if ferr := w.store.MarkInboundIntakeFailed(ctx, it.ID, err.Error()); ferr != nil {
				log.Printf("[inbound-process] CRITICAL: mark-failed for %s errored: %v", it.ID, ferr)
			}
			return fmt.Errorf("inbound processing failed (final attempt %d): %w", job.Attempt, err)
		}
		return fmt.Errorf("inbound processing attempt %d failed: %w", job.Attempt, err)
	}
	// Success — ProcessIntake flipped intake.status to 'processed' in its own tx.
	return nil
}

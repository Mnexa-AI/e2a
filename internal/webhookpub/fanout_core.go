package webhookpub

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fanout_core.go holds the fan-out logic SHARED by both engines that drain the
// webhook_events outbox (Layer 1) into webhook_subscriber_deliveries (Layer 2): the
// legacy in-process OutboxWorker (worker.go, LISTEN/NOTIFY) and the River FanOutWorker
// (fanout_worker.go). Keeping the shared core here means worker.go is entirely the
// legacy path — deletable as one unit once E2A_WEBHOOK_FANOUT_MODE=river is the only
// mode.

// DeliveryEnqueuer enqueues a webhook delivery job transactionally.
// *webhookdelivery.Jobs satisfies it. Injected so webhookpub stays decoupled
// from the River-delivery package and the choice is a wiring decision.
type DeliveryEnqueuer interface {
	EnqueueDeliveryTx(ctx context.Context, tx pgx.Tx, deliveryID string) (int64, error)
}

// identityReader is the subset of identity.Store the fan-out needs.
// Kept as an interface so tests can pass a fake.
type identityReader interface {
	ListEnabledWebhooksForRouting(ctx context.Context, userID, eventType string) ([]identity.Webhook, error)
}

// leasedEvent is the fan-out row-shape for one webhook_events row. Named for the legacy
// worker's SKIP-LOCKED lease that produces it (leasePending); the River path builds the
// same shape with a plain SELECT (see loadedEvent in fanout_worker.go).
type leasedEvent struct {
	id             string
	userID         string
	eventType      string
	envelope       []byte
	agentID        *string
	conversationID *string
	messageID      *string
	attempts       int
}

// fanOutEventCore fans out a single loaded event in ONE transaction: match the user's
// enabled subscribers, insert one webhook_subscriber_deliveries row per match (ON
// CONFLICT dedup), enqueue a River delivery job per real insert, and mark the event
// row terminal (processed/no_match) under a status='pending' guard so a concurrent
// finisher (an expired-lease replica, or a duplicate at-least-once River job) can't be
// clobbered. Returns an error instead of recording failure so both the legacy
// OutboxWorker and the River FanOutWorker share the exact same body.
//
// Failure-metric parity with the pre-extraction fanOutOne: a list-subscribers error
// records NO OutboxFailures label (it only wraps the message, which recordFailure then
// logs); a tx error emits OutboxFailures("update_status"). Callers add their own
// recovery on top (recordFailure for the legacy poll, River retry for the worker).
func fanOutEventCore(ctx context.Context, pool *pgxpool.Pool, identityStore identityReader, enqueuer DeliveryEnqueuer, metrics telemetry.Metrics, ev leasedEvent) error {
	webhooks, err := identityStore.ListEnabledWebhooksForRouting(ctx, ev.userID, ev.eventType)
	if err != nil {
		return fmt.Errorf("list subscribers: %w", err)
	}

	// Reconstruct an Event-shaped struct from the leasedEvent to feed `matches`.
	eventForMatching := Event{
		Type:           ev.eventType,
		UserID:         ev.userID,
		AgentID:        derefString(ev.agentID),
		ConversationID: derefString(ev.conversationID),
		// Labels: not currently tracked on webhook_events; deferred.
		MessageID: derefString(ev.messageID),
	}

	// matched starts as an empty slice (not nil) so pgx serializes it as the empty
	// Postgres array '{}', not NULL. matched_webhook_ids is TEXT[] NOT NULL DEFAULT
	// '{}' — a NULL would fail the NOT NULL constraint and the UPDATE would error.
	matched := make([]string, 0, len(webhooks))
	for _, wh := range webhooks {
		if matches(eventForMatching, wh.Filters) {
			matched = append(matched, wh.ID)
		}
	}

	if len(matched) == 0 {
		metrics.OutboxEventsNoMatch(ev.eventType)
	} else {
		metrics.OutboxEventsFanOut(ev.eventType, len(matched))
	}
	if err := poolBeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if len(matched) > 0 {
			inserted, err := insertPendingBatchTx(ctx, tx, ev.id, matched, ev.eventType, ev.messageID, ev.envelope)
			if err != nil {
				return err
			}
			// Enqueue a River delivery job for each row that ACTUALLY inserted (dedup
			// no-ops are absent from `inserted`), in this same tx — the Layer 2 row and
			// its job commit atomically, so there is never a record without a job, and a
			// deduped event enqueues nothing. Stamp the row's job_id for the cutover
			// discriminator + observability.
			if enqueuer != nil {
				for _, d := range inserted {
					jobID, err := enqueuer.EnqueueDeliveryTx(ctx, tx, d)
					if err != nil {
						return err
					}
					if _, err := tx.Exec(ctx,
						`UPDATE webhook_subscriber_deliveries SET job_id = $2 WHERE id = $1`, d, jobID,
					); err != nil {
						return err
					}
				}
			}
		}
		// Conditional UPDATE: if another worker already processed this event row (our
		// lease expired during a long fan-out and replica B took over and finished, or
		// — under River — a duplicate at-least-once job ran), the status='pending'
		// predicate matches zero rows and this UPDATE no-ops.
		newStatus := "processed"
		if len(matched) == 0 {
			newStatus = "no_match"
		}
		_, err := tx.Exec(ctx,
			`UPDATE webhook_events
			 SET status = $1, processed_at = now(), matched_webhook_ids = $3
			 WHERE id = $2 AND status = 'pending'`,
			newStatus, ev.id, matched,
		)
		return err
	}); err != nil {
		metrics.OutboxFailures("update_status")
		return err
	}
	return nil
}

// insertPendingBatchTx writes one webhook_subscriber_deliveries row
// per matched webhook in a single multi-row INSERT. Per-row ON
// CONFLICT (event_id, webhook_id) WHERE event_id IS NOT NULL AND
// replay_id IS NULL DO NOTHING swallows duplicate inserts that come
// from a multi-replica lease race (worker A inserted, worker B's
// lease expired, retries, both see the row) without aborting the tx.
//
// The WHERE predicate is verbatim-matched to the partial unique index
// in migration 028 — Postgres requires exact predicate matching for
// ON CONFLICT to bind to a partial index.
// It RETURNs the ids of the rows that were ACTUALLY inserted (ON CONFLICT skips
// duplicates from a multi-replica lease race, and those are absent from
// RETURNING) — so the caller enqueues exactly one River delivery job per real
// insert, never for a dedup no-op.
func insertPendingBatchTx(ctx context.Context, tx pgx.Tx, eventID string, webhookIDs []string, eventType string, messageID *string, envelope []byte) ([]string, error) {
	if len(webhookIDs) == 0 {
		return nil, nil
	}

	// Build multi-row VALUES list. pgx supports parameterized arrays,
	// but the simplest portable shape is to fan out the parameters.
	args := make([]any, 0, 5+len(webhookIDs)*2)
	values := make([]string, 0, len(webhookIDs))
	args = append(args, eventID, eventType, messageID, envelope)
	for i, whID := range webhookIDs {
		// id, webhook_id placeholders. The other 4 fields are shared
		// across all rows so reference them by their fixed indexes.
		base := 4 + i*2
		values = append(values, fmt.Sprintf("($%d, $%d, $1, $2, $4, $3, 'pending')", base+1, base+2))
		args = append(args, generateDeliveryID(), whID)
	}

	sql := `INSERT INTO webhook_subscriber_deliveries
	            (id, webhook_id, event_id, event_type, event_payload, message_id, status)
	        VALUES ` + strings.Join(values, ", ") + `
	        ON CONFLICT (event_id, webhook_id)
	            WHERE event_id IS NOT NULL AND replay_id IS NULL
	            DO NOTHING
	        RETURNING id`

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var inserted []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		inserted = append(inserted, id)
	}
	return inserted, rows.Err()
}

// generateDeliveryID mints a whd_<32-hex> delivery id — the one id format for
// every webhook_subscriber_deliveries row (outbox fan-out, /test, redelivery).
func generateDeliveryID() string {
	// 16 bytes of entropy. The format is enforced by the rest of the
	// codebase; cryptographic strength is overkill but keeps prefix
	// width consistent with evt_/whd_/etc.
	return "whd_" + randHex16()
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// poolBeginFunc emulates pgx v4's BeginFunc against a *pgxpool.Pool (pgx v5 dropped
// the helper): Begin → fn → Commit, rolling back on error. Shared by both fan-out
// workers via fanOutEventCore.
func poolBeginFunc(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

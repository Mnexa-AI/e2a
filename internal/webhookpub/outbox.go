package webhookpub

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Outbox is the Stripe-tier publisher entry point. Triggers write the
// webhook_events row inside the same transaction as their business
// state (e.g. the messages row for email.received). A separate
// publisher worker (slice 2) consumes pending rows and fans out into
// webhook_subscriber_deliveries.
//
// Why this lives alongside Publisher: the legacy Publisher.Publish
// does in-process fan-out (it reads enabled webhooks and inserts
// delivery rows directly). The new Outbox.PublishTx writes ONE row
// to webhook_events and arranges NOTIFY; fan-out is deferred. During
// the rollout window (controlled by the WEBHOOKS_OUTBOX_ENABLED env
// var, plumbed into the trigger sites), each trigger picks one path.
// After slice 11 the legacy Publisher.Publish branch is deleted.
//
// See docs/design/2026-06-01-stripe-tier-webhooks.md §4.2 and Appendix A.
type Outbox interface {
	// PublishTx writes the event to webhook_events inside the
	// caller's transaction. Returns error so the caller can roll back
	// its business state if the outbox write fails.
	//
	// Used for PRE-side-effect triggers (email.received, future
	// email.bounced from SNS, email.pending_review, email.review_rejected).
	// If the outbox write fails the caller's tx rolls back; on
	// retry, the deterministic event id makes the second outbox
	// INSERT a no-op via ON CONFLICT (id) DO NOTHING.
	PublishTx(ctx context.Context, tx pgx.Tx, e Event) error

	// PublishBestEffortTx attempts the outbox write inside the
	// caller's transaction but never returns an error. On failure,
	// logs to webhook_publish_failures (slice 4 will add the table)
	// and lets the caller's tx commit anyway.
	//
	// Returns `wrote=true` iff the row was actually written (flag
	// enabled AND writeOutboxRow succeeded). Callers use this to
	// decide whether to fire the legacy publisher.Publish goroutine
	// as a fallback for at-least-once: when wrote=false, the legacy
	// path is the safety net so the customer's webhook still gets
	// the event. Without this signal, an outbox failure under
	// WEBHOOKS_OUTBOX_ENABLED=true would silently drop the event.
	//
	// Used for POST-side-effect triggers (email.sent, email.review_approved)
	// where the irreversible action (SES.Send) has already happened
	// and rolling back the business state would orphan an SES
	// delivery.
	PublishBestEffortTx(ctx context.Context, tx pgx.Tx, e Event) (wrote bool)

	// DeleteExpiredWebhookEvents drops rows past their 30-day retention.
	// Called from the hourly cleanup loop in cmd/e2a/main.go.
	DeleteExpiredWebhookEvents(ctx context.Context) (int, error)

	// Enabled reports whether the outbox is the durable fan-out path
	// for this deployment. When true, trigger sites (relay + the
	// publishPendingApproval/publishRejected/publishSent/publishApproved
	// helpers) MUST skip the legacy `go publisher.Publish(...)`
	// goroutine — both paths inserting into webhook_subscriber_deliveries
	// would produce duplicate customer-visible webhook POSTs. When
	// false, the outbox is a dormant no-op and the legacy goroutine
	// remains the sole delivery path.
	//
	// Closes the C3 audit finding: legacy InsertPending writes rows
	// with event_id=NULL, falling OUTSIDE the partial unique index
	// `idx_wsd_event_webhook_uniq` (predicate: WHERE event_id IS NOT
	// NULL AND replay_id IS NULL). So the index, designed to dedupe
	// multi-replica outbox workers, cannot dedupe legacy-vs-outbox
	// rows — they look like distinct deliveries.
	Enabled() bool
}

// outboxExecutor is the subset of pgx.Tx and pgxpool.Pool needed by
// the outbox writer. Mirrors the agentExecutor pattern at
// internal/identity/store.go:600 — same SQL body works for both
// stand-alone and in-transaction callers.
type outboxExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// outbox is the production Outbox backed by a pgxpool.
type outbox struct {
	pool *pgxpool.Pool
	flag FeatureFlag
}

// NewOutbox constructs the Stripe-tier outbox writer. The FeatureFlag
// gates writes: when disabled, PublishTx is a no-op (returns nil with
// no DB write). Slice 4's trigger sites branch on the same flag so
// the legacy go publisher.Publish(...) path runs instead.
//
// Pass StaticFlag(false) in v1 production until slice 11 flips it.
func NewOutbox(pool *pgxpool.Pool, flag FeatureFlag) Outbox {
	if flag == nil {
		flag = StaticFlag(false)
	}
	return &outbox{pool: pool, flag: flag}
}

func (o *outbox) PublishTx(ctx context.Context, tx pgx.Tx, e Event) error {
	if !o.flag.Enabled() {
		return nil
	}
	return writeOutboxRow(ctx, tx, e)
}

// Enabled mirrors the flag state. Trigger sites check this to suppress
// the legacy publisher.Publish goroutine when the outbox is the
// durable fan-out path; see the Outbox interface docstring for the
// reasoning.
func (o *outbox) Enabled() bool {
	return o.flag.Enabled()
}

// DeleteExpiredWebhookEvents removes terminal rows whose expires_at has
// passed. Migration 026 sets a 30-day TTL on every event row; without
// this janitor the table grows monotonically and the (user_id,
// created_at) index degrades. Mirrors
// webhook.SubscriberStore.DeleteExpiredSubscriberDeliveries for the
// parallel slice-2 delivery table.
//
// Returns the number of rows deleted.
//
// The status guard is load-bearing. The outbox is at-least-once by
// design — see worker.go's recordFailure docstring: "there is NO
// terminal 'failed' state on the outbox … we let the row stay pending
// until human intervention or a successful retry." If we delete
// pending rows at 30 days the retry-forever guarantee silently breaks,
// dropping events that never reached any webhook. A row only becomes
// eligible for the sweep once the worker has marked it terminal
// (`processed` after a successful fan-out, `no_match` when no enabled
// webhook subscribed).
//
// Trade-off: a row that's broken on the retry path (e.g. a downstream
// SELECT panics every iteration) will accumulate forever rather than
// fall out at day 30. That's the "page ops after many attempts" case
// recordFailure calls out — preferable to silent loss.
func (o *outbox) DeleteExpiredWebhookEvents(ctx context.Context) (int, error) {
	tag, err := o.pool.Exec(ctx,
		`DELETE FROM webhook_events
		 WHERE expires_at <= now()
		   AND status <> 'pending'`,
	)
	if err != nil {
		return 0, fmt.Errorf("delete expired webhook_events: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (o *outbox) PublishBestEffortTx(ctx context.Context, tx pgx.Tx, e Event) (wrote bool) {
	if !o.flag.Enabled() {
		return false
	}
	if err := writeOutboxRow(ctx, tx, e); err != nil {
		// Best-effort: log and return wrote=false. The caller's tx
		// commits the business state regardless because the
		// irreversible action (SES.Send) already happened — rolling
		// back would orphan a sent email. A future slice will pipe
		// these failures to a webhook_publish_failures table; for now
		// the log is the only signal AND the legacy-fallback signal
		// for the caller.
		log.Printf("[outbox] PublishBestEffortTx err (event=%s type=%s): %v", e.ID, e.Type, err)
		return false
	}
	return true
}

// writeOutboxRow is the SQL body shared by PublishTx and (eventually)
// PublishBestEffortTx. Idempotent on (id): a retried trigger with the
// same deterministic id no-ops the second INSERT. Issues pg_notify so
// the slice-2 worker wakes immediately on commit.
func writeOutboxRow(ctx context.Context, exec outboxExecutor, e Event) error {
	if e.ID == "" {
		return fmt.Errorf("webhookpub: outbox event must have non-empty ID")
	}
	if e.UserID == "" {
		return fmt.Errorf("webhookpub: outbox event must have non-empty UserID")
	}
	if e.Type == "" {
		return fmt.Errorf("webhookpub: outbox event must have non-empty Type")
	}

	envelopeJSON, err := json.Marshal(e.AsEnvelope())
	if err != nil {
		return fmt.Errorf("webhookpub: marshal envelope: %w", err)
	}

	var messageID *string
	if e.MessageID != "" {
		mid := e.MessageID
		messageID = &mid
	}
	var agentID *string
	if e.AgentID != "" {
		aid := e.AgentID
		agentID = &aid
	}
	var conversationID *string
	if e.ConversationID != "" {
		cid := e.ConversationID
		conversationID = &cid
	}

	// created_at and expires_at use the column DEFAULTs so the
	// timestamps come from the Postgres server clock (one clock per
	// primary writer; no application-side skew).
	_, err = exec.Exec(ctx,
		`INSERT INTO webhook_events
		    (id, user_id, type, aud, envelope, schema_version,
		     agent_id, conversation_id, message_id, status)
		 VALUES ($1, $2, $3, 'webhook', $4, 1, $5, $6, $7, 'pending')
		 ON CONFLICT (id) DO NOTHING`,
		e.ID, e.UserID, e.Type, envelopeJSON,
		agentID, conversationID, messageID,
	)
	if err != nil {
		return fmt.Errorf("webhookpub: insert webhook_events: %w", err)
	}

	// pg_notify is best-effort: NOTIFY only fires on COMMIT (Postgres
	// queues it). If COMMIT fails, no notification is emitted. The
	// slice-2 worker's 1s fallback poll catches missed wakeups
	// (deploy windows, LISTEN reconnect races). Payload is empty
	// because the worker rescans the table anyway.
	//
	// A pg_notify error here (NOTIFY queue overflow is the realistic
	// case; max_notify_queue_pages defaults to 1024 × 8KB = 8MB) is a
	// SOFT failure: we log and return nil so the caller's tx still
	// commits. The webhook_events row is what matters for at-least-
	// once delivery; the NOTIFY is only a latency optimization that
	// the worker's 1s fallback poll covers.
	//
	// Returning the error here would propagate out of writeOutboxRow,
	// out of PublishTx, and out of the caller's WithTx — rolling back
	// the entire trigger transaction. In the relay path that means
	// the inbound `messages` row is also lost; SMTP returns a 4xx to
	// the MTA which retries into the same broken state. (See PR for
	// C2 in the audit.)
	if _, nerr := exec.Exec(ctx, `SELECT pg_notify('webhook_events_new', '')`); nerr != nil {
		log.Printf("[outbox] pg_notify webhook_events_new failed (event=%s, type=%s): %v — worker fallback poll will recover",
			e.ID, e.Type, nerr)
	}
	return nil
}

// Time helpers — kept here rather than relying on time.Now() in
// callers so test code can swap them. Not currently used; slice 2's
// worker will need a clock abstraction.
var nowUTC = func() time.Time { return time.Now().UTC() }

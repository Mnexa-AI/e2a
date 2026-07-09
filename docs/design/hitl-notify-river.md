# Durable HITL approval-notification on River

Status: implementing · Owner: backend · Related: `internal/hitlnotify`, `internal/agent/api.go`, migration 057

## Context / the gap

When an outbound message enters `pending_review` (HITL hold), the account
owner gets an email with a preview + one-click approve/reject magic links. It
is the reviewer's primary — sometimes only — signal that a message is waiting.

Today that email is **fire-and-forget**: `HoldForApprovalCore`
(`internal/agent/api.go`) calls `Notifier.NotifyPendingApprovalAsync`, which
spawns a detached `go func()` (background ctx, 2-min timeout) that composes and
SMTP-sends inline. Nothing persists the intent to notify. If the process
crashes or SMTP is unreachable between the `202 pending_review` response and the
send, **the notification is lost forever** and the reviewer is never told.
`cmd/e2a/main.go` flags this in-code as a known gap.

This is the last real at-least-once hole after the inbound (#391) and outbound
(#388) River migrations. This change closes it by putting the notification on
River — same three-layer pattern as `internal/outboundsend`.

## Design

Three layers, mirroring `outboundsend`:

```
HoldForApprovalCore (one tx):  INSERT messages (pending_review)
                               + InsertTx hitl_notify job
                               + stamp notify_job_id            → commit → 202
River QueueNotify worker:      re-read msg+agent+owner → compose → SendOnce
                               → set notified_at
```

- **Layer 1 (fact):** the `messages` row entering `pending_review` — it already
  exists, so no new table. We add two nullable columns to `messages`:
  `notify_job_id BIGINT` (reconciler bookkeeping) and `notified_at TIMESTAMPTZ`
  (send-dedup marker).
- **Layer 2 (state / outbox):** in the **same tx** as the pending-message
  insert, `EnqueueNotifyTx` inserts the `hitl_notify` River job and stamps its
  id onto `notify_job_id`. The row and its job commit atomically — neither can
  exist without the other. (This matches the outbound accept-tx; if the enqueue
  fails the whole hold fails with a 500, which is a same-DB failure that would
  fail the message insert anyway.)
- **Layer 3 (execution):** a `river.Worker` on a new `notify` queue re-reads the
  message + agent + owner, composes the email (existing render code, deterministic
  magic-link tokens), and submits **once** via `SMTPRelay.SendOnce` — River owns
  the retry envelope, replacing the notifier's old inline behavior.

### Worker guards (idempotency / pointlessness)

`Work` loads the row and no-ops (returns nil) when:
1. message is gone (deleted/pruned) — `LoadPendingNotify` returns nil;
2. `status != pending_review` — already approved/rejected/expired, nothing to review;
3. `approval_expires_at` is in the past — the hold is dead; the TTL worker
   handles it, a "please review" email is now useless;
4. `notified_at` is already set — a prior attempt sent it (crash-after-send
   re-drive).

On deliver:
- **success** → `MarkNotified` (sets `notified_at`), return nil;
- **permanent** (`IsPermanentSMTPError`, e.g. bad owner address) → log + `river.JobCancel`
  (no retry; unavoidable, the hold still finalizes on TTL);
- **connection/outage** (`IsConnectionError`) → `river.JobSnooze` while the hold is
  still live, else give up;
- **transient** → wrapped error → River reschedules per `NextRetry` until
  `MaxNotifyAttempts`, then cancel + log.

At-least-once holds because loss is impossible: `notified_at` is only ever set
**after** a successful send. A duplicate "please review" email (rare crash
window) is benign — the accepted at-least-once trade.

### Reconciler (cutover)

`ReconcilePending`, run once at startup like the inbound/outbound reconcilers:
`SELECT id FROM messages WHERE status='pending_review' AND notify_job_id IS NULL
AND notified_at IS NULL`, per-row `FOR UPDATE` re-check + `EnqueueNotifyTx` +
stamp. Backed by a partial index `idx_messages_pending_no_notify_job` (built
`CONCURRENTLY` in migration 058 so the deploy doesn't write-lock `messages`).

**Cutover double-notify — the `notified_at IS NULL` guard.** Unlike the outbound
cutover (where the sync path had already moved rows off `accepted`), every hold
still `pending_review` when this ships was *already* emailed by the old
goroutine. Without a guard the reconciler would email each owner a second time.
Migration 057 stamps `notified_at = now()` on exactly those pre-existing
`pending_review` rows, and the reconciler skips any row with `notified_at` set —
so the first deploy notifies no one twice. A hold created on the no-notifier
plain path keeps `notified_at` NULL and is correctly picked up if a relay is
later configured.

### Queue

New `QueueNotify = "notify"` (small pool, ~4 workers) so a burst of held sends
notifying can't starve customer outbound delivery, and a stuck notification
(bad owner address retrying) doesn't consume outbound worker slots. Adding a
queue is a `queues.go` map + `jobs.Config` entry — no SQL (River owns its own
schema).

## Files

- `migrations/057_messages_notify_job.sql` — `ADD COLUMN IF NOT EXISTS notify_job_id
  BIGINT`, `notified_at TIMESTAMPTZ` (metadata-only), + a cutover `UPDATE` stamping
  `notified_at` on pre-existing `pending_review` rows.
- `migrations/058_messages_notify_job_idx.sql` — the partial reconciler index, built
  `CREATE INDEX CONCURRENTLY` under `-- e2a:no-transaction` (split out so the build
  doesn't write-lock `messages`).
- `internal/jobs/queues.go` — add `QueueNotify` + config wiring.
- `internal/hitlnotify/` — new `HITLNotifyArgs`, `NotifyWorker`, `Store`/`Deliverer`
  interfaces, `Jobs` (Registrar + `EnqueueNotifyTx` + `ReconcilePending`); refactor
  `Notifier` to implement `Deliverer` via `SendOnce`; drop `NotifyPendingApprovalAsync`.
- `internal/identity/store.go` — extract shared `createPendingOutboundMessage(exec, …)`,
  add `CreatePendingOutboundMessageTx`, `StampNotifyJobIDTx`, `MarkMessageNotified`,
  `LoadPendingNotify` (+ `PendingNotify` type).
- `internal/agent/api.go` — `NotifyEnqueuer` seam + `SetNotifyEnqueuer`; rewrite
  `HoldForApprovalCore` to the same-tx path (falls back to the plain insert when
  no enqueuer is wired, i.e. notifier unconfigured).
- `cmd/e2a/main.go` — build + register the notify Jobs, `SetEnqueuer`,
  `ReconcilePending`, `SetNotifyEnqueuer(api)`; delete the known-gap comment.

## Verification

- `go build ./...`, unit tests for the worker (success, gone, resolved, expired,
  already-notified, permanent-cancel, outage-snooze, last-attempt) + reconciler
  integration test (mirrors `outboundsend/reconcile_test.go`).
- Live smoke against Mailpit: hold a send → `202` → notification email lands
  **off** the request path → re-drive/duplicate is suppressed by `notified_at`;
  unconfigured notifier → plain hold, no enqueue, no notification (unchanged).

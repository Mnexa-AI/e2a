# Webhook Delivery → River — Migration Design

**Status:** Approved direction (decisions locked 2026-07-05). Migrates the hand-rolled webhook delivery queue onto the shared River foundation (`internal/jobs`, PR #386). Live, delivery-critical customer traffic — cutover is the load-bearing risk.

**Decided:** three-layer model (facts / delivery-state / River execution); the event outbox is made **unconditional** (remove `WEBHOOKS_OUTBOX_ENABLED`, delete the legacy fire-and-forget path); **one-shot** cutover (stop legacy worker → enqueue pending → start River); at-least-once is the target semantic (unconditional, no flag). Implementation order: (0) make outbox unconditional + delete legacy path, (1) migration + `DeliverWorker` + retry policy behind `delivery_engine=river`, (2) one-shot cutover, (3) delete the legacy `SubscriberRetryWorker`.

---

## ⟳ SUPERSEDED BY SLICE 3b (SHIPPED — reconciled to reality)

The migration shipped over slices 1–3b. Three things diverged from the plan above; this section is authoritative where it conflicts:

1. **No `delivery_engine=river` flag.** River is the **sole, unconditional** delivery engine. The flag existed only during slices 1–4 for the flag-gated cutover; slice 3b removed it and deleted the legacy `SubscriberRetryWorker`, `retry.go`, and the legacy in-process publisher. There is nothing to flip.
2. **THREE enqueue sites, not two.** §4's "just two sites" (outbox drain + redelivery) missed a third: the **`/test` webhook endpoint** (`InsertPendingForTest`) also inserts a delivery row directly and must enqueue. All three now call River enqueue: the outbox drain (`EnqueueDeliveryTx`, in-tx with the row) and the `/test` + redelivery handlers (`EnqueueDelivery`, own tx after the committed insert).
3. **The one-shot cutover became a live reconciler.** `ReconcilePending` (née `CutoverPending`) still runs once at startup, but it is ALSO driven by a **periodic `ReconcileWorker` every 1 min** (`QueueMaintenance`). This is the fix for the review finding that a startup-only backstop leaves the separate-tx `/test`/redelivery paths (and any outbox-drain crash window) stranded until the next restart. Now any `status='pending' AND job_id IS NULL` row is re-driven within a minute. Migration `052` adds a partial index backing that scan. Consequence: the `/test` + redelivery handlers no longer 500 / report `skipped` on an enqueue failure — the row is durable and reconciler-backed, so they log and report `pending` (a 500 would just spawn duplicate rows on retry).
4. **The GA retry envelope is eight attempts spanning 29h21m.** Attempt 1 is immediate; failed attempts 1–7 wait `1m, 5m, 15m, 1h, 4h, 8h, 16h` before the next attempt. The earlier plan's extra `24h` delay was unreachable with `MaxAttempts=8`, and its “~72h” claim was inaccurate. `internal/webhookdelivery/worker.go`, the shared contract tests, and `docs/api.md` are authoritative.

**Known residual (accepted, LOW):** a row whose `job_id` is stamped but whose River job died *without* writing a terminal status (e.g. manual `river_job` deletion, or a crash on the final attempt between River's attempt-bump and `MarkSubscriberFailed`) is not re-driven — the reconciler gates on `job_id IS NULL`. River persists jobs in Postgres and resumes them on restart, so this only bites on manual job deletion; not worth a stale-job sweep at current scale.

**Also deferred:** the webhook auto-disable + signing-grace janitor moved from a `time.Ticker` to a River periodic (`webhookdelivery.MaintenanceJobs`, `QueueMaintenance`) — the last webhook-adjacent worker off a hand-rolled loop.

## 1. Problem statement

Before this migration, webhook delivery conflated two responsibilities on one table. `webhook_subscriber_deliveries` was **both** the customer-visible delivery record (backs `GET /v1/webhooks/{id}/deliveries`) **and** the execution queue (`SubscriberRetryWorker` used `SKIP LOCKED` leases, a hand-rolled retry schedule, a per-webhook inflight cap, and disabled/deleted-webhook handling). River now owns queue execution while preserving the customer-facing delivery record and history API.

## 2. Goals and non-goals

**Goals**
- Move delivery **execution** (claim/lease/retry/backoff/concurrency) from the hand-rolled worker to River, on the shared `QueueWebhook` lane. ("webhook_delivery_queue" is the conceptual name for this Layer-3 execution stage; it is River's `river_job` on the `webhook` lane, NOT a new hand-rolled table.)
- **Make the event outbox unconditional.** Remove `WEBHOOKS_OUTBOX_ENABLED` and delete the legacy fire-and-forget `publisher.Publish` path (`shouldFireLegacy`, the relay's `!outbox.Enabled()` branch). After this, `webhook_events` is the sole event path and the outbox drain is the sole enqueue path — so at-least-once is **unconditional** (no flag) and there is exactly ONE place to wire River enqueue instead of two.
- Preserve exactly: the delivery-history API + its fields, the eight-attempt custom retry policy (corrected by the authoritative note above), the (event, webhook) dedup guarantee, disabled/deleted-webhook and secret-rotation semantics.
- Delete `internal/webhook/subscriber_retry.go`'s claim/retry machinery and `internal/webhook/retry.go`'s schedule (superseded by a River retry policy).
- A **safe cutover** (one-shot, §8) — no lost or double-delivered events during deploy.
- Document the consumer contract: **at-least-once + dedup on the event id** (the industry standard — Stripe/Svix/Postmark; e2a already emits deterministic event ids).

**Non-goals**
- Changing `webhook_events` (Layer 1) — untouched.
- Changing the webhook event catalog, filters, signing, SSRF guard, or the delivery-history API shape.
- Migrating the legacy per-message `webhook_deliveries` table (separate retirement).
- HITL / outbound migrations (separate; this establishes the pattern they follow).

## 3. Relevant context and constraints — the three layers

```
Layer 1 — FACTS           webhook_events                 immutable event log; GET /v1/events, redelivery, ~30d
            │  fan-out (1 event → N matching subscribers)
            ▼
Layer 2 — DELIVERY STATE  webhook_subscriber_deliveries  per (event × webhook) record; GET /v1/webhooks/{id}/deliveries
            │                                             + the dedup point (idx_wsd_event_webhook_uniq)
            │  drives
            ▼
Layer 3 — EXECUTION       river_job (queue="webhook")     POST + retry + backoff — River owns claim/lease/retry
```

**Current mechanics (all to preserve behaviorally):**
- **Layer 2 fields** (`SubscriberDelivery`): `id, webhook_id, event_type, event_payload (POSTed verbatim), message_id, status (pending|delivered|failed), attempts, max_attempts, last_error, last_status_code, last_attempt_at, next_retry_at, created_at, expires_at`. The history API returns these.
- **Retry envelope** (`webhookdelivery/worker.go`): attempt 1 is immediate; retry delays are `1m,5m,15m,1h,4h,8h,16h` for attempts 2–8 (eight attempts spanning `29h21m`); after attempt 8, terminal `failed`.
- **Dedup** (`idx_wsd_event_webhook_uniq`, migration 028): one first-delivery row per `(event_id, webhook_id)` where `event_id IS NOT NULL AND replay_id IS NULL` — prevents double-delivery across replicas; **replays are exempt** (they carry `replay_id`).
- **Per-attempt webhook re-fetch** (`processOne`): disabled → defer 1h (`disabledDeferral`, no attempt burned); deleted → mark failed.
- **Signing-secret rotation:** honor `signing_secret_prev` only within its 24h grace window.
- **Per-webhook inflight cap of 1** (a `sync.Map[webhookID]→Mutex`): a slow customer backlogs onto its own webhook without starving others; also avoids hammering one endpoint concurrently.
- **Enqueue points that create Layer 2 rows today:** `webhookpub.OutboxWorker` (drains `webhook_events`, matches subscribers, inserts one row per match); the legacy `publisher.InsertPending`; the redelivery API (`internal/agent/replay_api.go`).
- **Janitors:** `DeleteExpiredSubscriberDeliveries` (Layer 2 TTL) + `auto_disable.go` (disable persistently-failing webhooks).

**Foundation:** `internal/jobs` (PR #386) — `jobs.Enqueuer` (`Insert`/`InsertTx`), `jobs.Registrar`, `QueueWebhook`, one shared `river.Client` / `river_job`.

## 4. Proposed design

### Layer 1 — `webhook_events`: unchanged.

### Layer 2 — `webhook_subscriber_deliveries`: same table, role flips to pure delivery-state
- **Stops being claimed.** `GetPending` (the `SKIP LOCKED` lease) and `BumpNextRetry` are deleted. Nothing leases Layer 2 anymore.
- **Becomes worker-written state.** The River worker updates the row on each attempt: `MarkDelivered(id, code)` and `RecordAttemptFailure(id, err, code)` **stay** (they write `status/attempts/last_error/last_status_code/last_attempt_at`) — the history API is unchanged.
- **`next_retry_at`** is now a denormalized reflection of the River job's schedule (River owns the true schedule). Keep the column (history API may surface it) but write it from the worker's known next-retry, or stop surfacing it — **open question 3**. `attempts`/`max_attempts` stay meaningful and are written from `job.Attempt`/`MaxAttempts`.
- **Dedup unchanged.** The `(event_id, webhook_id)` unique index stays the dedup gate — see enqueue below.
- **Schema delta:** add `job_id BIGINT NULL` (the River job id) for observability + the cutover discriminator (below). Additive, idempotent migration. No destructive change.

### Layer 3 — `river_job` on `QueueWebhook`: the new execution layer

**`WebhookDeliverArgs{ DeliveryID string }`**, `Kind() = "webhook_deliver"`. Args carry only the Layer 2 delivery id — the worker loads payload + webhook from L2/identity (keeps the job row tiny; L2 is the source of truth).

**`DeliverWorker.Work(ctx, job)`** — mirrors today's `processOne`:
1. Load the L2 row by `DeliveryID` (gone → return terminal, nothing to do).
2. Re-fetch the webhook (`GetWebhookByIDInternal`):
   - **deleted** → `MarkFailed` L2 + return a **terminal** error (`river.JobCancel`) so River discards it (no retries).
   - **disabled** → **`river.JobSnooze(disabledDeferral)`** (1h) — River reschedules without counting an attempt, exactly matching today's `BumpNextRetry` defer. Re-enable resumes within the hour.
3. Compute `prevSecret` with the 24h grace check (unchanged).
4. `deliverer.Deliver(...)` (POST, 15s timeout, SSRF guard — unchanged).
   - **2xx** → `MarkDelivered` L2 + return `nil` (River completes the job).
   - **failure** → `RecordAttemptFailure` L2; if `job.Attempt >= job.MaxAttempts` also set L2 `status='failed'` (terminal); return the error so River retries per the retry policy.

**Retry policy — exact envelope, not River's default exponential.** A custom per-kind policy returns `now + retryBackoffs[job.Attempt-1]` after failed attempts 1–7, with `MaxAttempts = 8`. This produces attempts at cumulative offsets `0, 1m, 6m, 21m, 1h21m, 5h21m, 13h21m, 29h21m`. River's built-in policy is a different curve and must not replace this contract.

**Per-webhook inflight cap of 1.** River v0.39 has no native per-arg concurrency limit. Preserve the property with a **Postgres advisory xact lock keyed on `hashtext(webhook_id)`** taken at the top of `Work`: `pg_try_advisory_xact_lock` — if not acquired, `river.JobSnooze(short)` so a second concurrent job for the same webhook backs off instead of hammering the endpoint. (Alternatively accept `QueueWebhook` global concurrency and drop the per-webhook cap — **open question 2**; the advisory-lock approach preserves current behavior.)

### Outbox is now the sole event path (flag + legacy path retired)

`WEBHOOKS_OUTBOX_ENABLED` is removed — the outbox is always on. The legacy fire-and-forget `go publisher.Publish(...)` path and its `shouldFireLegacy`/`!outbox.Enabled()` branches (relay + agent) are **deleted**. Consequence: `webhook_events` is written unconditionally (in the message tx, as today when the flag was on), and the outbox drain is the **only** thing that creates Layer 2 rows. This means there is exactly one enqueue site to wire into River (below), and the end-to-end at-least-once guarantee no longer depends on a flag.

### Enqueue — where Layer 2 rows are born, now also enqueue Layer 3 (atomically)

The single enqueue site (the outbox drain, plus the redelivery API) inserts a Layer 2 row **in one transaction with the River enqueue** (the outbox pattern between L2 and L3, via `jobs.Enqueuer.InsertTx`), gated on the dedup:

```
BEGIN
  INSERT INTO webhook_subscriber_deliveries (...) ON CONFLICT (dedup) DO NOTHING RETURNING id
  -- if a row was actually inserted (not a dedup no-op):
  client.InsertTx(tx, WebhookDeliverArgs{DeliveryID: id}, &InsertOpts{Queue: QueueWebhook, MaxAttempts: 8})
  UPDATE ... SET job_id = <returned> (or capture from InsertTx result)
COMMIT
```

- **Dedup preserved:** if the L2 insert is a conflict no-op, **no job is enqueued** — exactly one delivery per (event, webhook), even across replicas.
- **Atomic:** L2 row and its job commit together — no orphan row without a job, no job without a row.
- **Sites to update:** just two now that the legacy path is gone — `webhookpub.OutboxWorker` (the fan-out drain) and the redelivery API (`replay_api.go` — replays carry `replay_id`, exempt from dedup, so they always insert + enqueue).

### Auto-disable → River periodic job

`auto_disable.go`'s ticker becomes a **periodic River job on `QueueMaintenance`** that reads persistent-failure counts from Layer 2 (still worker-written) and disables the webhook. Consolidates another hand-rolled ticker; same logic, River-scheduled. (Could stay a ticker for v1 — **open question 4**.)

### Wiring

A `webhookdelivery` package (or extend `internal/webhook`) exposes a `jobs.Registrar` that registers `DeliverWorker` (+ the auto-disable periodic job) and holds the injected `jobs.Enqueuer` for the enqueue sites. `main.go` adds it to the `jobs.New(...)` registrar list next to `senderMgr`. This also **lifts the shared client out of the SES-only block** (webhook delivery isn't SES-gated) — the client becomes unconditional, which the PR #386 review already anticipated.

## 4a. At-least-once guarantee

The design is **unconditionally at-least-once, end to end** (no flag) — the industry-standard webhook guarantee (Stripe/Svix/Postmark are all at-least-once). Hop by hop:

- **event → `webhook_events` (L1):** at-least-once — the event commits in the message transaction, always (the outbox is now unconditional; the flag and the fire-and-forget path are removed).
- **`webhook_events` → L2 row + River job (fan-out drain):** the `OutboxWorker` inserts the L2 row(s) **and `InsertTx`s the job(s) in one transaction** — both commit or neither. Re-drain after a crash is idempotent: the L2 insert is `ON CONFLICT DO NOTHING` and the job is enqueued **only if a row was actually inserted**, so no duplicate row/job. No window where a record exists without a job or vice versa.
- **job → delivered (River worker):** River is at-least-once — a crashed/timed-out worker's job is re-driven (River's rescuer), failures retry to `MaxAttempts` on the frozen 29h21m schedule, terminal `failed` is recorded (queryable + redeliverable), never silently dropped.

**Irreducible residual (⇒ at-least-once, not exactly-once — which is impossible over HTTP webhooks):** POST succeeds → worker crashes before writing L2 `delivered` → River re-drives → endpoint receives a duplicate. Same residual as today's lease re-drive.

**Consumer contract (document in `docs/events.md`):** *e2a delivers webhook events at-least-once; your endpoint may receive an event more than once — dedup on the event id and return 2xx.* e2a already emits deterministic event ids (message-id + type) in the signed payload, which is exactly the stable id consumers dedup on. This is the same contract Stripe (`event.id`), Svix (`webhook-id`), and Postmark document.

## 5. Edge cases and failure handling

- **Crash between L2 insert and job enqueue:** same transaction → can't happen (both commit or neither).
- **Crash after job completes, before L2 marked delivered:** River retries the job; the worker re-POSTs (at-least-once — same as today's lease-expiry re-drive). Receivers dedup on the event id in the signed payload (unchanged property). Acceptable, pre-existing.
- **Duplicate delivery across replicas:** prevented at enqueue by the dedup index (no second L2 row → no second job).
- **Disabled webhook backlog:** snoozed 1h per attempt, no attempts burned — does not exhaust the 8-attempt budget while disabled (matches today).
- **Deleted webhook mid-flight:** `JobCancel` (terminal) + L2 `failed`; the L2 row cascades away with the webhook at the next janitor pass.
- **Secret rotation grace:** unchanged (worker computes `prevSecret` per attempt).
- **River job vs L2 divergence:** L2 is authoritative for the customer API; the River job is transient. If a job is manually cancelled/discarded in River, the worker's terminal path already wrote L2 `failed`; a River discard without a worker write (e.g. `MaxAttempts` reached and River discards before the worker sets failed) is handled by writing L2 `failed` on the last-attempt branch, and belt-and-suspenders by a `Config.ErrorHandler` that marks L2 failed on discard — **decide in impl**.
- **Fail-closed defaults:** webhook lookup error → do not POST (today's behavior); advisory-lock contention → snooze, never double-POST.

## 6. Scalability and extensibility

- **Queue isolation:** webhook delivery on `QueueWebhook` with its own `MaxWorkers` (default 16) can't starve outbound sends on `QueueOutbound` — the lane isolation the hand-rolled worker lacked.
- **Per-webhook fairness** preserved via the advisory lock; scales to many webhooks without the `sync.Map` growing unbounded in the app (the lock lives in Postgres).
- **This is the template** for the HITL and outbound migrations: L1 fact log / L2 state record / L3 River execution, atomic L2-insert-plus-InsertTx, custom retry policy per lane. HITL's approve-send reuses the outbound `SendArgs` job the same way.
- **Deliberately narrow for v1:** args carry only `delivery_id` (not the payload) — simplest, L2 is source of truth. If payload-in-job is ever wanted (avoid the L2 read), it's an additive args change.

## 7. Verification strategy

- **Unit/DB tests (CI-run, non-tagged so `make cover` covers them):** DeliverWorker success → L2 delivered + job complete; failure → L2 attempt recorded + River retry scheduled at the exact backoff; last-attempt failure → L2 failed; disabled → snooze (no attempt burned); deleted → cancel + L2 failed; dedup → second enqueue for same (event, webhook) inserts no row and no job; replay (replay_id) → always enqueues.
- **Retry-policy test:** assert `NextRetry` returns the exact seven-delay sequence after attempts 1–7 and returns no further retry after attempt 8.
- **Cutover test:** with pre-existing `pending` L2 rows (no `job_id`), the chosen cutover path drains them without double-delivery (see open questions).
- **History-API regression:** `GET /v1/webhooks/{id}/deliveries` returns identical fields/shape before and after.
- **Load/soak:** queue-depth + oldest-job age on `QueueWebhook`; confirm per-webhook serialization under a slow endpoint doesn't starve others.
- **Most likely regressions:** retry timing drift (custom policy wrong), a dedup hole (enqueue not gated on the insert result), or a double-delivery during cutover.

## 8. Open questions

1. ~~Cutover strategy~~ — **DECIDED: one-shot migration.** At deploy: enqueue a River job for every `pending` L2 row, then run only River. **Correctness-critical ordering (the whole risk of one-shot):**
   1. **Stop the legacy `SubscriberRetryWorker` first** (remove its wiring from `main.go` in the same release) — it must NOT run concurrently with the River worker over the same rows, or both deliver the backlog → duplicate delivery of every in-flight event.
   2. **One-shot enqueue** a `WebhookDeliverArgs` job for every `status='pending' AND job_id IS NULL` L2 row, setting `job_id` in the same statement/tx. Idempotent: the `job_id IS NULL` guard means a re-run (or a crashed-and-restarted migration) never double-enqueues. Run it at startup after `jobs.Migrate`, before the worker starts claiming — or as an admin/one-time job.
   3. **Start the River worker.** From here all delivery is River.
   New deliveries arriving during/after cutover already go through the new atomic-enqueue path (§4), so they're never in the "pending without job_id" set. Rollback is a redeploy of the prior binary (the enqueued River jobs are harmless if the legacy worker is also absent — they just sit until a River-capable binary runs; do NOT roll back to a binary that runs the legacy worker while River jobs exist, or double-delivery). Because rollback room is narrower than the strangler, gate the whole path behind `webhook.delivery_engine=river` config so the cutover is a deliberate flip, not implicit in the deploy.
2. **Per-webhook inflight cap:** preserve via Postgres advisory lock (keeps current behavior), or drop it and rely on `QueueWebhook` global concurrency (simpler, but a slow endpoint could take multiple concurrent slots)? Recommend preserve.
3. **`next_retry_at` in the history API:** keep writing it (denormalized from the job) for API continuity, or drop it from the response now that River owns scheduling? Recommend keep-writing for zero API change.
4. **Auto-disable:** River periodic job now (consolidation), or leave the ticker for v1 and fold in later? Recommend fold in (it's small and on-theme).
5. **Terminal-failed write:** worker last-attempt branch vs `Config.ErrorHandler` on discard — which is the single source of truth for L2 `failed`? Decide in impl (likely worker branch, ErrorHandler as backstop).

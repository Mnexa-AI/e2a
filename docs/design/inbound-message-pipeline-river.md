# Inbound message pipeline → persist-first on River

Status: **DRAFT for review** (2026-07-07). Author: design pass off the as-built trace.
Companion to `async-message-pipeline.md` (outbound) and
`webhook-delivery-river-migration.md`. This is the **inbound** counterpart: push the
durability boundary to the SMTP edge, the way outbound pushed it to the API edge.

---

## 1. Problem statement

Today the inbound path does **all** processing inline in the SMTP session — MIME
parse, SPF/DKIM, sender gate, content screening (incl. an up-to-10s Gemini call),
HMAC signing, conversation lookup, and the `messages` insert — and only then returns
`250 OK`. Two consequences:

1. **Silent loss.** `deliverToAgent` swallows a persist error (logs + returns), and
   `deliverMessages` counts it delivered anyway, so a failed insert still returns
   **`250 OK`** (`internal/relay/server.go:522`). The sending MTA treats `250` as
   done and never retries → the message is **lost**. `docs/events.md` advertises
   `email.received` as "at-least-once end-to-end," which is **false** at this edge.
2. **LLM latency on the SMTP critical path.** A multi-second Gemini screen inside the
   session risks the sender's SMTP timeout and couples receipt to an external API.

There is also **no inbound dedup**: the message id is random per DATA call, so an MTA
retry after a slow/failed `250` produces a **second `messages` row + second
`email.received`**, delivered twice.

Outbound already solved the symmetric problem (persist-first accept-tx → River
`QueueOutbound`, #388). Inbound should mirror it.

## 2. Goals and non-goals

**Goals**
- **At-least-once from the SMTP edge inward.** A `250 OK` means the message is
  durably accepted; anything we can't accept returns a `4xx` so the sender retries.
- **Move parse / SPF-DKIM / screening / persist / deliver OFF the SMTP session** into
  a River worker on a new `QueueInbound`.
- **Best-effort dedup** of MTA retries (collapse the lost-ack duplicate).
- **Preserve** all existing behavior: RCPT-time hard rejects (550/552), SPF/DKIM
  semantics (against the connecting IP + envelope), inbound screening/HITL
  (flag/review/block), conversation threading, `email.received`/flagged/blocked/
  pending_review events, WS live-tail, per-recipient fan-out.
- **Flag-gated cutover** (`E2A_INBOUND_MODE`, default `sync`) with the sync path
  byte-for-byte unchanged; a startup + live reconciler for stranded intake.

**Non-goals**
- Changing the SPF/DKIM/screening *logic* (only *where* it runs).
- A raw-object/S3 store — raw MIME lives in the intake row (bytea), as today it lives
  in `messages.raw_message`.
- Exactly-once (impossible over SMTP); dedup is a quality property, not a correctness
  invariant.
- The outbound/webhook lanes (already migrated).

## 3. Relevant context and constraints

- e2a's relay is the **direct MX** (`docs/deployment.md:42`), `emersion/go-smtp`. The
  retrying MTA is the *sender's*, not SES. `4xx` → sender queues + retries ~4–5 days;
  `250` → done, no retry; `5xx` → bounce to author.
- **RCPT-time gating stays synchronous and pre-DATA** — unknown/unverified recipient
  → `550`, owner over cap → `552` (`server.go:194-220`). We must keep rejecting bad
  recipients *before* accepting a body; only post-DATA processing moves async.
- **SPF needs the connecting IP**, available only in-session. The intake row MUST
  capture `remote_ip` + `envelope_from` so the worker can run `emailauth.Check`
  later. (`auth_verdict` is already stored for the same reason.)
- Delivery fan-out is **already** decoupled (outbox → `OutboxWorker` → River
  `webhookdelivery`); we reuse it verbatim — the worker's `messages`-tx publishes
  `email.received` to the same outbox.
- Shared River foundation exists (`internal/jobs` Registrar/Enqueuer, named queues).
  Add `QueueInbound`.
- Per-recipient model: today one `messages` row per resolved RCPT TO.

## 4. Proposed design

### 4.1 Three-layer model (consistent with webhook/outbound)

- **L1 — `inbound_intake`** (NEW): the immutable durable fact — "these raw bytes
  arrived for this recipient from this IP." Written in the SMTP session; the only
  thing that gates `250`.
- **L2 — `messages`** (unchanged shape): the processed result, written by the worker.
- **L3 — `river_job` on `QueueInbound`**: the execution (parse/screen/persist/deliver).

### 4.2 SMTP session (pre-`250`) — shrunk to a durable accept

RCPT gating unchanged. In `session.Data`, when `E2A_INBOUND_MODE=async`:

```
read body (already bounded 10MB)
content_hash = sha256(raw)
ONE TX:
  for each resolved recipient:
     INSERT INTO inbound_intake (id, recipient, envelope_from, remote_ip,
        raw_message, message_id, content_hash, status='accepted', created_at)
     ON CONFLICT (recipient, message_id, content_hash) DO NOTHING
     RETURNING id
     if a row was inserted:            -- not a duplicate
        job_id = EnqueueInboundProcessTx(tx, intake_id)   -- River InsertTx on QueueInbound
        UPDATE inbound_intake SET process_job_id=job_id WHERE id=intake_id
COMMIT
if commit ok  -> 250 OK            (durably accepted, incl. idempotent duplicate)
if commit err -> 451 4.3.0         (transient — sender retries)
```

- **Dedup:** `UNIQUE (recipient, message_id, content_hash)`. A duplicate (lost-ack
  retry) hits `ON CONFLICT DO NOTHING`, enqueues nothing, and still returns `250` —
  idempotent accept. If `message_id` is empty (sender omitted it), the hash + envelope
  still key it; two genuinely-distinct bodies won't collapse.
- **Atomicity:** intake insert + River enqueue + job-id stamp in one tx (River
  `InsertTx` on the same `pgx.Tx`), exactly like the outbound accept-tx. A committed
  `accepted` intake row always has a job.
- **`451` correctness rule:** return `4xx` **only** when the tx did not commit. A
  failure *after* commit but before `250` must still `250` (else double-accept — the
  dedup key would collapse it anyway, but the rule keeps intent clean).

### 4.3 River worker (post-`250`) — `internal/inboundprocess`

`InboundProcessWorker.Work(intake_id)` — one attempt per job attempt, River owns retry:

```
load intake row; nil -> no-op (pruned)
if intake.status='processed' -> no-op         (idempotent re-drive)
parse MIME (net/mail)                          — off the SMTP path now
emailauth.Check(intake.remote_ip, envelope_from, raw)
HMAC sign (headers.Signer)
sender gate (inboundpolicy.EvaluateIngestion)
content screen (piguard, incl. Gemini)         — off the SMTP path now
conversation resolve
ONE TX:
  CreateInboundMessageInTx(...)   -> messages row (status per screen: sent/pending_review/review_rejected)
  outbox.PublishTx(email.received | flagged | blocked | pending_review)  (suppressed when held)
  UPDATE inbound_intake SET status='processed', message_fk=<msg id> WHERE id=intake_id
COMMIT
best-effort: writeProtectionEvents; WS hub.Send (when !Hold)
```

- **Idempotency:** the worker's terminal tx flips `intake.status='processed'` **in the
  same tx** as the `messages` insert. A re-drive sees `processed` → no-op. So a crash
  after commit never double-creates.
- **Screening/HITL:** identical logic (`screenInbound`), just relocated. Held →
  `messages` row with hold status + `approval_expires_at`, `email.received`
  suppressed, only `email.pending_review`/`blocked` emitted, WS skipped. Unchanged
  externally.
- **Delivery:** the `email.received` outbox row drives the existing
  `OutboxWorker`→River webhook path with no change. WS stays best-effort.
- **Error classification** (mirror outbound §8): a transient parse/DB error → River
  retry (bounded envelope). A permanently-unparseable body → terminal (mark intake
  `failed`, emit nothing / an `email.rejected`? — see open questions). No provider
  outage concept here (no upstream call except Gemini, which fails *open* to allow).

### 4.4 Reconciler + retention

- **Startup cutover reconciler:** on boot in async mode, enqueue a job for every
  `inbound_intake` with `status='accepted' AND process_job_id IS NULL` (mirror
  `outboundsend.ReconcilePending`). Handles rows accepted by a build that crashed
  pre-enqueue and the mode-flip moment.
- **Live periodic reconciler** (`QueueMaintenance`) — **DEFERRED (follow-up, matching
  outboundsend)**: would re-enqueue `accepted` intake whose job is terminal/absent,
  closing the rare "job discarded without processing" strand. Not shipped: the
  accept-tx is atomic so the NULL-job set is ~empty in steady state, and the startup
  pass re-drives on the next deploy. Only the startup cutover ships.
- **Retention:** prune `status='processed'` intake older than N days (raw is also in
  `messages.raw_message`). A periodic sweep; keep a short window (e.g. 3 days) for
  debugging + re-drive. `failed` intake retained longer for inspection.

### 4.5 Cutover

`E2A_INBOUND_MODE` (`config.InboundConfig`, env override), default **`sync`**. The
relay's `Data` branches on it: `async` → §4.2; `sync` → the existing inline path,
untouched. Enable per-deploy in e2a-ops after canary. No wire/API change — inbound
processing is server-internal, so no spec/SDK regen.

## 5. Edge cases and failure handling

- **Transient accept failure** → `451`, sender retries (§4.2). No loss.
- **Lost `250` ack** (committed, ack dropped) → sender retry → dedup `ON CONFLICT` →
  idempotent `250`, no second job. No duplicate.
- **Worker crash mid-processing** → River re-drives; `processed` guard makes it
  idempotent; before the terminal tx nothing is externally visible, so re-run is safe.
- **Unparseable / malformed MIME** → terminal fail (don't infinite-retry a body that
  will never parse); mark intake `failed`. *Open q: notify anyone?*
- **Gemini timeout/outage** → screen fails **open** (allow + deliver), as today — an
  LLM outage must not block mail. Now it also doesn't block the SMTP session.
- **Multi-recipient** → N intake rows + N jobs in one tx; partial isn't possible (one
  tx). Each recipient dedups independently.
- **Held message** → worker writes hold-status row, suppresses `email.received`; a
  re-drive is idempotent via `processed`.
- **Agent deleted between accept and processing** → worker load resolves no agent →
  terminal no-op (message dropped; the recipient is gone). Matches today's cascade.
- **Intake grows unbounded** → retention sweep (§4.4).
- **Clock/`remote_ip` capture** → store `remote_ip` as text at accept; SPF uses it
  verbatim. IPv6 + proxied (PROXY protocol / XCLIENT) — capture the same source the
  sync path uses today (verify in impl).

## 6. Scalability and extensibility notes

- Decoupling the Gemini call from the session removes the dominant SMTP-latency /
  timeout risk and lets screening scale independently (River concurrency cap), not
  bounded by SMTP connection slots.
- `QueueInbound` is independently pausable/tunable (`river.QueuePause`,
  `MaxWorkers`) — an inbound spike can't starve outbound/webhook and vice-versa.
- The intake table is the natural home for future needs: raw re-processing (re-run
  screening after a model upgrade), inbound replay/redelivery API (symmetric to
  webhook redelivery), and a spam/greylisting layer at accept time.
- Extensible dedup: the key can gain a scope column without a rewrite.

## 7. Verification strategy

- **Unit:** dedup key (same `message_id`+recipient+hash → one row, one job); the
  `451`-on-tx-failure vs `250`-on-commit rule; worker idempotency (re-drive of a
  `processed` intake = no-op); screen relocation preserves hold/deliver outcomes;
  accept-tx atomicity (intake + job together or not at all).
- **Coverage floor** for `internal/inboundprocess` (mirror `outboundsend` @75).
- **Local e2e (over real SMTP, the required gate):** send via SMTP → `250` → worker →
  `messages` row + `email.received` at a webhook capture; a held-scan case →
  `pending_review` + suppressed `received`; a **transient-failure injection** (point
  at a broken DB / force the intake insert to fail) → **`451`, no message**, then
  recovery on retry; a **duplicate send** (same `Message-ID` twice) → one message;
  sync-mode (flag off) unchanged; reconciler cutover (plant an `accepted` intake, no
  job → boot → processed).
- **Regression:** the swallowed-`250`-on-persist-failure path must now be a `451`.
- **Independent + adversarial review** on the pushed branch, as with #388.

## 8. Open questions

1. **Intake table vs minimal-`messages`-row-first.** — **DECIDED (2026-07-07):
   separate `inbound_intake` L1 table.** Rationale: inbound cannot produce a complete
   `messages` row at accept (subject/sender/auth/conversation all come out of the
   worker's parse+screen), so a minimal `messages` row would pollute the product's
   central read model with genuinely-incomplete `receiving` rows and push an
   exclude-incomplete obligation onto every reader (API list/detail, web inbox,
   threading, review queue, exports) — a silent-leak failure mode. Unlike outbound,
   whose `accepted` row is *complete*-but-undelivered (so persist-first into
   `messages` was fine there), inbound must accept bytes before it knows what they
   are. The separate table's costs (one migration, a bounded raw-duplication window
   erased by pruning) are contained and mechanical.
2. **Terminal-failure UX.** When a body is permanently unparseable or the worker
   exhausts retries, do we (a) silently drop + mark intake `failed`, (b) emit a new
   `email.rejected` webhook event, or (c) attempt an SMTP-time reject? Since we've
   already `250`'d, (c) is out; leaning (a) for v1 with a `failed` intake for ops
   visibility, (b) as a follow-up. Agree?
3. **Dedup window.** Unbounded (unique index forever) vs scoped to a retention window
   (a very late resend after prune would re-deliver). Leaning **unique index on live
   intake + prune after 3d** — a >3d resend is effectively a new message. OK?
4. **`remote_ip` under proxy.** Confirm the sync path's IP source (direct vs PROXY
   protocol / XCLIENT) so the intake captures the same value SPF uses today.
5. **Scope of slice 1.** Recommend slice 1 = the engine-agnostic honesty fix
   (`deliverToAgent` propagates the persist error → `Data` returns `451`), shippable
   immediately and independent of the queue — then slices 2+ build the intake table,
   worker, cutover. Agree?
```

# Async Message Pipeline — Architecture Design (Outbound + Inbound)

**Status:** Approved, hardened after adversarial launch review (2026-07-04). Companion to `async-send-contract.md` (the outbound contract spec; the inbound changes have no API-contract surface). **Decided 2026-07-03: at-least-once is a pre-GA blocker in BOTH directions.** Outbound slices 1–3 and the inbound minimal fix (slice I1) land before the /v1 GA freeze and GA ships with async as the default outbound path; outbound slices 4–6 and the inbound queue (slice I2) follow post-GA.

> **Launch-review hardening (2026-07-04).** A 5-dimension adversarial review found the queue's ownership model was asserted but never specified — the single load-bearing gap, flagged independently by 4 of 5 reviewers. The fixes are folded into §§4–9 below; the deltas from the pre-review draft are: an explicit **lease fencing token** on every post-claim write (§4); lease sized **above** the real ~6.5-min SMTP envelope with **heartbeat extension** (§4/§5, was an undersized 5 min against a wrong "~21s"); a **terminal-failure guard** so the sweeper never declares `failed` for an in-flight or provider-accepted send, plus an **SES-outage circuit breaker** (§4/§8); an **honest crash matrix** (§7, the old "exactly-once"/"irreducible window" claims were false for slow-worker takeover and final-attempt crash); the **inbound dedupe redesigned** to content-hash + retry-horizon bound (§9, the Message-ID-only key silently dropped real mail and was an attacker suppression primitive); the **durable event tail made mandatory** (not a flag) whenever `outbound.mode=async` (§10). See the contract doc for the `wait=sent`, `blocked`-terminal-signal, idempotency-scope, and event-subscription fixes.

**Guarantee (normative — the GA blocker):**
- **Outbound:** once e2a returns 200 `accepted` on a send, it has durably persisted the message and MUST attempt SMTP delivery until the provider accepts or a terminal failure is declared (retries exhausted / permanent rejection → `delivery_status='failed'`). The outcome — `email.sent` or `email.failed` — is written to the durable event log (`webhook_events`, same transaction as the status write) and delivered to subscribers with at-least-once semantics.
- **Inbound:** e2a MUST NOT reply SMTP 250 until the message is durably persisted; on persist failure it replies 451 so the upstream MTA retries (SMTP's native durable-retry, the mirror of the API caller's idempotent retry). MTA retries dedupe on the RFC `Message-ID`. Once 250 is issued, the message reaches the agent's durable push path (event log → webhook) at-least-once.

No accepted message — in either direction — is ever silently dropped; every stage is either transactional or lease-protected with re-drive.

## 0. System overview — two lanes, shared tail

```
Inbound:   MX/SMTP receiver → inbound_message_queue → parse worker ─┐
                                                                    ├→ webhook_events → webhook_subscriber_deliveries → customer webhook
Outbound:  API → outbound_message_queue → send worker → SES ────────┘        (shared, direction-agnostic event log + delivery queue)
```

All queues are Postgres tables — no broker — sharing one idiom: `FOR UPDATE SKIP LOCKED` claim, lease with sweeper takeover, bounded backoff, durable terminal state. The event log and webhook delivery queue are deliberately shared, not per-direction (events carry `direction`; two parallel event pipelines would double the operational surface for no isolation benefit). §§1–8 design the outbound lane; §9 the inbound lane; §§10–12 rollout, testing, and open questions for both.

## 1. Outbound lane: current vs target

**Current (direct `/v1` send, `internal/agent/api.go` DeliverOutbound):**
```
request → screen (incl. LLM scan) → SES submit → meter → persist row → 200 sent
```
Synchronous SES inside the request (up to a ~6.5-min worst case — 4 attempts × up to a 2-min session deadline + 30s dial + 1s/5s/15s backoff sleeps, `internal/outbound/smtp_relay.go`; the codebase's `SendAttemptStaleWindow = 10m` is sized against this envelope, not the "~21s" sleep-sum an earlier draft cited), message row and `msg_` id created only AFTER SES accepts, insert failure swallowed (returns 200 anyway). Crash after SES-accept = sent + billed email with no DB trace, unrecoverable. The HITL **approve** path already has the correct shape (durable pending row + `send_attempts` WAL + `hitlworker` re-drive) — this design generalizes that shape to all outbound.

**Target:**
```
request:  auth → validate → recipient-policy gate → template render → quota + N tokens
          → ONE TX: mint msg_id, insert messages row (delivery_status='accepted') + outbound_message_queue row
          → 200 {message_id, status: accepted}                     [low tens of ms]

worker:   claim job (SKIP LOCKED lease → lease_token) → content scan → [hold → pending_review, stop]
          → ramp Reserve → RE-CHECK lease still held → compose MIME (X-E2A-Message-ID header)
          → SMTP submit to SES → mark sent + provider_message_id + meter + email.sent
            ALL in ONE tx guarded by WHERE lease_token=$mine → webhook → delete job (same guard)
          (heartbeat extends the lease every ~60s during the job so a slow-but-alive
           worker is never reclaimed mid-send)

sweeper:  re-drives leases past lease_expires_at; at max-attempts, declares failed ONLY after
          the terminal-failure guard (no in-flight lease, no provider-accept evidence)
          → delivery_status='failed' + email.failed → event log (outbox, same tx) → webhook

feedback: SES → SNS → /webhooks/ses → per-recipient rollup (already built, unchanged)
```

## 2. Sync/async split — the line is "no network I/O before the 200"

**Synchronous (all local):** auth; schema validation; recipient-policy gate (cheap DB read — disallowed recipient stays an immediate 403); template render (in-process mustache; render-before-hold ordering preserved — the reviewer still approves rendered content); suppression check; monthly-quota N-check; token-bucket debit; the persist transaction.

**Asynchronous (worker):** LLM/PI content scan and the hold it may produce; sending-ramp reservation; MIME compose; SMTP submit; provider-id capture; usage metering; `email.sent` emission.

**Product consequence (decided):** holds return **200** from GA (`body.status: pending_review`, not 202 — 200 iff persisted, contract §1.5). Through GA the scan runs synchronously before the accept-tx, so the hold is known at request time and the 200 carries `pending_review`. When slice 4 (post-GA) moves scanning into the worker, the request-time response for a to-be-held send becomes 200 `accepted` with `email.pending_review` following as an event — the HTTP code stays 200 either way (no breaking flip). The recipient-policy 403 stays sync. A scan **block** now needs a durable representation (today block is rowless 403): add `blocked` to `messages.status` (or map onto the review-rejected family — decide in slice 2), emit a terminal `email.failed{reason:blocked}` (contract §1.3, so an accepted send that is never sent still reaches a terminal signal).

## 3. Message state machine

`messages.delivery_status` (contract doc §3): 
```
accepted → sending → sent → delivered
                   ↘ failed            ↘ deferred → delivered/bounced
        ↘ (scan hold: messages.status=pending_review; job removed;
           approval re-enqueues → accepted)          ↘ bounced / complained
```
Transitions: API writes `accepted`; worker lease writes `sending`; worker success writes `sent` (existing `MarkMessageSent`); sweeper max-attempts writes `failed`; the SNS consumer owns everything after `sent` (existing monotonic `delivery.Merge`, `internal/delivery/status.go` — extend precedence to include `accepted`/`sending` below `sent`). While held for review, `delivery_status` stays `accepted`; the hold lives on `messages.status='pending_review'` (contract §3.1) — the two lifecycles keep their own columns.

## 4. Queue: Postgres, no broker

Single binary + Postgres is a hard constraint (open-core self-host story). The queue is a dedicated table — do NOT put lease churn on the hot `messages` table:

```sql
CREATE TABLE IF NOT EXISTS outbound_message_queue (
  message_id       TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
  state            TEXT NOT NULL CHECK (state IN ('queued','leased')),
  attempts         INT  NOT NULL DEFAULT 0,
  max_attempts     INT  NOT NULL DEFAULT 6,
  lease_token      UUID,               -- fencing token, rotated on every claim/takeover
  next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_expires_at TIMESTAMPTZ,
  last_error       TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_outbound_message_queue_ready ON outbound_message_queue (next_attempt_at) WHERE state = 'queued';
```

- **Outbox pattern:** the job row is inserted in the SAME transaction as the message row — persist and enqueue cannot diverge.
- **Claim (mints a fresh fencing token):**
  ```sql
  UPDATE outbound_message_queue
     SET state='leased', lease_token=gen_random_uuid(),
         lease_expires_at=now()+$lease, attempts=attempts+1
   WHERE message_id IN (
     SELECT message_id FROM outbound_message_queue
      WHERE state='queued' AND next_attempt_at<=now() AND attempts < max_attempts
      ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT $k)
  RETURNING message_id, lease_token;
  ```
  The `attempts < max_attempts` predicate is what stops a requeued-but-exhausted row from being re-leased forever (finding: the claim query previously had no cap, so a retryable failure that requeues could loop past `max_attempts` with only the sweeper — which sees `leased` rows only — ever declaring `failed`).
- **Fencing token — the ownership fence (this is the fix the pre-review draft lacked).** A lease-conditional UPDATE fences *DB writes* but cannot recall an *SMTP command already in flight*, so a slow-but-alive worker whose lease expired must be prevented from corrupting the state machine after a second worker takes over. Every post-claim write — heartbeat extension, requeue-with-backoff, ramp-deferral rewrite, the mark-sent/meter/`email.sent` transaction, and the terminal DELETE — carries `WHERE message_id=$id AND lease_token=$mine` and treats **0 rows affected as "ownership lost → abort immediately, emit nothing, delete nothing."** This is exactly the fence proven Postgres queues use (graphile-worker `locked_by`, River `attempted_by`, Que/Oban advisory locks); the codebase's own `send_attempts.MarkSendSucceeded` lacks it (guards only `WHERE status='attempting'`, which the takeover worker re-sets — so the stale worker's write still lands) and slice 5 must not inherit that hole. The residual that fencing cannot remove is the in-flight SMTP submit itself (documented in §7); fencing removes all state/event corruption around it.
- **Lease sizing + heartbeat.** Lease default **12 min** (above the ~6.5-min SMTP envelope + headroom, matching `SendAttemptStaleWindow`'s rationale — the earlier 5-min value was half the codebase's own proven window and would make sweeper takeover of *live* workers routine during SES brownouts). The worker also **heartbeats** — a fenced `UPDATE ... SET lease_expires_at=now()+$lease WHERE lease_token=$mine` every ~60s while working — so a legitimately slow job (or slice 4's LLM scan, which adds unbounded provider latency inside the lease) extends its own lease rather than being reclaimed. Heartbeat is what makes the fixed-lease-vs-slow-job tension go away; fixed-long-lease alone is the fallback.
- **Terminal (fenced):** DELETE the job row (queue stays small; `messages.delivery_status` is the durable record). Retryable failure: fenced requeue to `queued` with backoff (mirror `internal/webhook/retry.go`'s shape — e.g. 30s/2m/10m/1h, `max_attempts` default 6; `sending_ramp_limited` gets `next_attempt_at` = ramp-day rollover and does NOT increment attempts — a ramp deferral must not burn the retry budget or a domain mid-ramp gets falsely `failed`). **Provider-connection errors** (dial timeout, connection refused, SMTP 4xx) are classified separately and use an outage-tolerant tail that does not exhaust `max_attempts` on a provider outage (see §8's circuit breaker) — the industry retry horizon is 8–72h, not the few hours a 6-attempt exponential gives; matching the webhook deliverer's deliberate 72h choice.
- **Sweeper (fenced, with the terminal-failure guard):** reclaims `leased` rows past `lease_expires_at` by minting a new `lease_token` (crash takeover). At `attempts >= max_attempts` it declares `failed` **only after the terminal-failure guard**: it must not fire while an SMTP submit may still be in flight (the fence + heartbeat make an expired lease mean "worker is really gone," not "worker is slow") and must first check for provider-accept evidence (an SES `X-E2A-Message-ID`-tagged Send/Delivery notification) — declaring a delivered message `failed` is the uncorrectable error in §7. Sweep interval < heartbeat interval so a live worker is never mistaken for dead.
- The `raw_message`/composed inputs needed by the worker are already on the messages row (`raw_message`, recipients, template-rendered body) — the job row carries no payload.
- **`ON DELETE CASCADE` caveat:** two paths delete `messages` (`DeleteAgent` cascade, `DeleteExpiredMessages` TTL). Cascade silently removing a `leased` job under a live worker is benign given the fence (the worker's next fenced write finds 0 rows → aborts), but the worker MUST treat "job vanished" as abort-not-error, and the data-deletion paths must also clear any in-flight `accepted`/`sending` message (don't delete the agent out from under a sending message without cancelling it).

## 5. Worker

- Goroutine pool inside the existing binary (no new deployable), size configurable (`outbound.workers`, default ~8). Poll + claim batch; optional `LISTEN/NOTIFY` on insert to cut poll latency (also powers `wait=sent`).
- Per job: content scan → hold/block/allow (reuses `screenOutbound` minus the recipient gate already done sync) → `rampGate.Reserve` → **re-check the lease is still held** (cheap fenced SELECT; shrinks the exposure to just lease-check→submit) → send via existing `sender.Send` → then **one fenced transaction** (`WHERE lease_token=$mine`): `MarkMessageSent` + provider id + meter (`recordOutboundUsage` moves here, post-success, fixing the bill-before-persist bug) + `email.sent` via outbox `PublishTx` → delete job (fenced). Bundling mark-sent + meter + event + (logically) the delete into one guarded tx is what makes crash-matrix row 5 a clean no-op instead of a re-scan/re-reserve/re-send. Residual: a crash between SMTP-accept and that tx yields a sent-but-unbilled, un-marked message — rare, customer-favoring for billing, reconcilable for status via the wire header.
- **Durable outcome events:** `email.sent` (and the sweeper's `email.failed`) are emitted via the webhook outbox `PublishTx` in the same transaction as the mark-sent/mark-failed write — not the fire-and-forget legacy `go publisher.Publish(...)` goroutine. The durable event tail is **mandatory** whenever `outbound.mode=async` (§10 makes the outbox non-optional in that mode, not a separate flag to forget), because these events are the only push signal that an async send died — an async server whose failure events ride a fire-and-forget goroutine does not meet the guarantee.
- **SES-accept vs mark-sent window (the one true residual):** stamp `X-E2A-Message-ID: <msg_id>` on the outgoing MIME (`internal/outbound/compose.go` currently omits any e2a id). If a crash or lease-loss lands between SMTP accept and the fenced mark-sent tx, the re-drive MAY duplicate — this is at-least-once's irreducible residual (an accepted SMTP command cannot be recalled). **What the earlier draft got wrong:** it claimed "everything else becomes exactly-once" and that a live-but-slow worker "must not double-claim thanks to the lease UPDATE guard." False — before the fence + heartbeat, a slow (uncrashed) worker whose lease expired mid-`Send` and a takeover worker would BOTH submit, with no crash, and the stale worker's unguarded tail would then corrupt the taken-over job. The fence + heartbeat close the state/event corruption; the double-submit during a genuine over-lease stall collapses into this same residual, and the SNS feedback (now header-tagged) makes it detectable. See §7 for the honest matrix.
- Graceful shutdown: stop claiming, then **wait for in-flight sends up to the shutdown budget**; anything still in flight when the budget expires dies into the SES-accept↔mark-sent residual and is re-driven on restart (deploys are the common trigger for this row — size the shutdown drain against the SMTP envelope, and prefer heartbeat-extended leases so a mid-send deploy re-drives rather than double-sends where possible).
- **Ordering:** no FIFO guarantee, including within a conversation — document it. (If per-conversation ordering ever matters, add `ORDER BY created_at` + a per-conversation advisory lock later; not v1.)

## 6. Unification wins

- **Approve path collapses onto the queue.** Today `ApproveAndSend` has its own WAL (`send_attempts`, migration 015) and re-drive (`hitlworker`). Post-migration: approve = transition message back to `accepted` + insert job. `send_attempts` machinery is retired after the transition (keep the table; stop writing).
- **`wait=sent`** = subscribe to job completion (NOTIFY or short-poll the row) with ~10s timeout, then return current state. CLI `send` uses it by default (frozen exit-code contract).
- **Batch send** becomes: sync-validate all items → persist N message+job rows in ONE tx → 200 with N `accepted` items + `batch_id`. One batch idempotency key over one atomic insert suffices (the resumable per-item `{key}#{index}` design from the earlier batch plan is superseded). Crashed batch = jobs the sweeper finishes; no intent journal needed.
- **Idempotency simplifies:** with persist-first, "same key + same body → same `message_id`, never a second send" becomes strictly true — the key's `Complete` commits **inside the same transaction as whichever durable acceptance point fires**, before any side effect. That is the accept-tx for allowed sends AND `HoldForApprovalCore`'s insert for review-held sends (`internal/agent/api.go:1230-1243`): a held send never reaches the accept-tx, so if `Complete` only committed there, a crash after the hold row commits but before `Complete` would let a same-key retry re-screen and hold *again* — two `pending_review` rows, two owner-notification emails, and two real sends if the reviewer approves both. Slice 2 must commit `Complete` in the hold tx too (caching the 200 `pending_review` response), not only the accept tx. ("adjacent" is never enough — crash-matrix row 2 depends on same-tx.) The dead `markSideEffectCommitted` code gets deleted. (Contract §5.1 carries the caller-facing scope/TTL caveats.)

## 7. Crash matrix (target)

Exactly-once holds for every row **except** the SMTP-accept↔mark-sent window, which is at-least-once by nature (an accepted SMTP command cannot be recalled). The fence + heartbeat are what keep the other rows exactly-once; without them, slow-worker takeover (row 4) also duplicates.

| Crash / fault point | Outcome |
|---|---|
| Before accept-tx commit | Nothing durable; caller got no 200; retry safe (idempotency key replays nothing) |
| After commit, before 200 reaches caller | Row + job exist; worker sends anyway; caller's same-key retry replays `accepted` + same msg_id — exactly-once |
| Worker crash before SMTP submit | Lease expires → sweeper re-drives; fence stops the dead worker corrupting the row — exactly-once |
| **Worker slow (not crashed) past lease, mid-`Send`** | Sweeper takeover worker submits; original may also submit → **duplicate (at-least-once residual)**. Heartbeat makes this rare (a live worker extends its lease); the fence prevents the stale worker's tail from corrupting state/events. This row was wrongly labelled "must not double-claim" pre-review. |
| Crash/lease-loss after SMTP accept, before mark-sent tx | Re-drive may duplicate (the irreducible residual); wire header makes it reconcilable. Common trigger: a deploy whose shutdown budget expires mid-send. |
| **Crash after SES-accept on the FINAL attempt** | Without the guard: sweeper sees expired lease at `max_attempts` → declares `failed` + `email.failed{retryable:false}` for a message SES *accepted and will deliver* — and `delivery.Merge` ranks `failed` above `delivered`, so SNS feedback can never correct it (caller told it failed → re-sends → guaranteed duplicate). **With the terminal-failure guard: the sweeper checks for header-tagged provider-accept evidence before declaring `failed`, and `delivery.Merge` gets an explicit exception letting header-matched `sent`/`delivered` feedback override a local `failed`.** |
| Crash after mark-sent tx | Job re-drive finds 0 rows on its fenced claim / sees terminal state → no-op; `email.sent` already committed in the mark-sent tx (deterministic id, `ON CONFLICT DO NOTHING` on re-emit) |

## 8. Backpressure & limits

Quota N-check and token-bucket debit happen at accept time, so the queue is bounded per agent by the same budgets as today (accepting ≠ bypassing limits). Ramp throttling no longer fails requests — it reschedules jobs to the ramp-day rollover. A terminally-`failed` message has already consumed quota + a token at accept: decided, no automatic refund in v1 (document it; revisit if permanent-failure rates ever matter).

**SES-outage circuit breaker (required before slice 3).** A multi-hour regional SES/SNS incident must not exhaust every queued message's `max_attempts` and mass-fire false `email.failed{retryable:false}` webhooks — the guarantee would be honored to the letter while the practical outcome is an irreversible mass-failure event. Three mitigations: (1) **error classification** — provider-connection errors (dial timeout, connection refused, SMTP 4xx) use the outage-tolerant tail from §4 and do not count toward `max_attempts`, so an outage defers rather than fails; (2) an operator **pause switch** (`outbound.paused` via config/SIGHUP) that stops claiming without stopping the binary — under the single-binary constraint this is the only brake an operator has; (3) an **admin re-queue path** for terminally-`failed` outbound messages (re-insert a job row), mirroring the inbound parked-row re-drive — a runbook entry at minimum. The backoff schedule and `max_attempts` are **decided values, not `e.g.`**, before slice 3 ships.

**Per-tenant fairness.** Accept-time budgets bound queue *growth* per agent but not *contention*: one tenant blasting batch sends (slice 6 inserts up to 100 jobs/tx) fills the head of the global `next_attempt_at` ordering and starves every other tenant behind a fixed ~8-worker drain. The claim query should select round-robin/fairly across `agent_id` (e.g. a windowed claim, or a per-agent in-flight cap in the claim predicate) rather than pure global `next_attempt_at` — otherwise a single tenant monopolizes the pool. Decide the exact mechanism in slice 3; do not ship a pure-FIFO global claim.

**Observability (the metrics list must distinguish the failure modes it exists to catch).** Queue-depth + oldest-job-age alone cannot tell a dead worker pool from a slow provider from a poison-message loop — all three look like rising age. Emit at minimum: claim/complete **throughput**; **lease-takeover (sweeper re-drive) rate** (a rising rate is the double-send precursor — workers crashing or overrunning leases); **`email.failed` rate** (SES-outage early-warning); **heartbeat-extension rate**; outbox **drain lag** (`webhook_events` unclaimed age); parked-row count + age (inbound). `telemetry.NewLog()` is the default backend, but the alert thresholds (not just the metrics) are specified in the e2a-ops runbook. Optional global max-depth guard returning 503 `queue_full` (config, default off).

## 9. Inbound lane

**Current (`internal/relay/server.go`):**
```
MX/SMTP → RCPT gates (550 unknown recipient / 552 quota) → DATA:
  SPF/DKIM/DMARC → screen (incl. LLM scan — network I/O inside the open SMTP session)
  → persist messages row [+ events same-tx when outbox on] → fan-out → ALWAYS reply 250
```
Three gaps, mirroring outbound's send-then-persist disease:

1. **250-before-durable.** `Data()` replies 250 regardless of persist failure — a `CreateInboundMessage` error is logged and dropped (the code documents this itself at `server.go:576-583`: "the upstream MTA gets SMTP 250 OK and will NOT retry… tracked separately"). A DB hiccup = acknowledged-and-lost mail. SMTP is the one protocol where durable retry is free (senders retry a 451 for days); the lever exists and is unused.
2. **No dedupe under MTA retry.** The `msg_` id is minted randomly per delivery attempt (`server.go:312`) and there is no unique index on the stored RFC `Message-ID` — so the moment gap 1 is fixed and MTAs start retrying, each retry would create a duplicate message row + duplicate events (the "deterministic" event ids are keyed to the per-attempt random id, so they do not dedupe retries either, contra the comment at `server.go:480-483`). Gaps 1 and 2 must ship together.
3. **Fire-and-forget event enqueue** in legacy mode — closed by the same `WEBHOOKS_OUTBOX_ENABLED` flip as outbound (the relay's outbox branch, message + all events in one tx, is already the best-built in the codebase).

**Pre-GA minimal fix (slice I1 — the guarantee, not the architecture):**
- Propagate persist failure up through `deliverMessages` to `Data()` → reply **451** (transient; sender owns the retry). RCPT-time gates are unchanged — they already reject before accepting responsibility.
- **Content-aware, retry-horizon-bounded dedupe — NOT Message-ID alone.** The naive "`(agent_id, email_message_id)` unique index, reuse the row on conflict" is unsafe: Message-ID equality does not imply message identity. Real senders reuse Message-IDs across distinct messages (buggy ticketing/notification systems, some listservs, MTA forwards that preserve the original id), so the second, *different* message would hit the conflict, get "reused" onto the old row, and e2a would reply 250 having never persisted the new content — a silent drop with a success ACK, the exact thing the inbound guarantee forbids. Message-ID is also **attacker-controlled**: pre-sending junk with a guessed Message-ID becomes a mail-suppression primitive against an agent. Instead: dedupe key = `(agent_id, email_message_id, body_hash)` where `body_hash` is a digest of the raw message; on conflict reuse the row (a true MTA retry carries byte-identical content, so it matches), otherwise **insert a new row** — a genuinely different message with a colliding Message-ID is kept, not dropped. Scope the key to the **MTA retry horizon** (RFC 5321 §4.5.4: senders give up after ~4–5 days), e.g. bucket by received-week or ignore/purge index entries older than ~7 days, so the dedupe window matches what it exists to catch rather than deduping forever. **No-Message-ID messages** (RFC-legal, common in spam) fall outside a Message-ID-only index — dedupe them on `(agent_id, body_hash)` (or accept duplicates and say so); the design must state this rather than leave it implicit.
- The outbox flag flip (shared with outbound slice 3).

**Target (slice I2, post-GA): `inbound_message_queue` + parse worker.** Same shape as `outbound_message_queue` with the payload inline (raw blob + envelope + connection metadata — client IP and HELO must be captured at DATA time, SPF cannot be recomputed later):

```sql
CREATE TABLE IF NOT EXISTS inbound_message_queue (
  id               TEXT PRIMARY KEY,            -- minted once ⇒ dedupe is internal
  raw_message      BYTEA NOT NULL,
  body_hash        BYTEA NOT NULL,              -- digest for retry-horizon dedupe (§ dedupe above)
  mail_from        TEXT NOT NULL,
  rcpt_to          TEXT[] NOT NULL,
  client_ip        INET,
  helo             TEXT,
  state            TEXT NOT NULL CHECK (state IN ('queued','leased','parked','dropped')),
  lease_token      UUID,                        -- same fencing discipline as outbound
  attempts         INT  NOT NULL DEFAULT 0,
  max_attempts     INT  NOT NULL DEFAULT 6,
  next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_expires_at TIMESTAMPTZ,
  last_error       TEXT,
  received_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```
The parse worker uses the same fencing-token discipline as the outbound worker (§4) — every post-claim write carries `WHERE lease_token=$mine`. `dropped`/`parked` are the terminal states below.

- **DATA handler shrinks to:** read body → one-tx insert of the queue row → 250. Fast ACK; the LLM scan leaves the SMTP session entirely.
- **Parse worker:** claim (SKIP LOCKED + lease, same idiom) → SPF/DKIM/DMARC verdict from stored connection metadata → parse headers/threading → screening incl. LLM scan → per-recipient-agent `messages` rows + events (`email.received` / `pending_review` / `blocked` / `flagged`) in one outbox tx → best-effort WS live-tail → delete queue row. One queue row per SMTP message; per-recipient fan-out happens in the worker's tx (matches today's `deliverMessages` loop).
- **Terminal dispositions — split `parked` (retriable) from `dropped` (terminal).** After 250 we own the message, so nothing bounces; but not every terminal is the same:
  - `parked` = **retriable-after-fix**: a poison message (MIME that breaks the parser, pathological encoding) or a persistent infra/constraint bug. Retry never helps until code changes, so it waits with an alert and is re-driven after the fix. This is what parking is *for*.
  - `dropped` = **truly terminal, do NOT retry**: the recipient agent/domain was deleted or offboarded between the 250 and the parse (RCPT-validated at SMTP time, gone by parse time). There is nothing to fix and re-driving is categorically wrong — record a terminal disposition + audit row (and, if the account still exists, an `email.blocked`-style signal), do not park. Conflating this with `parked` sends an operator to investigate a message that will never be deliverable.
- **Data-deletion sweep.** A `parked`/queued row holds the full raw message — potentially a deleted user's mail. The user-data-rights deletion path (`internal/identity/user_data_rights.go`) MUST sweep `inbound_message_queue` too, or offboarded-user messages sit in the queue holding data we are obligated to delete.
- **Parked-row alerting is first-class, not just event-time.** A parked row that is a week old must re-alert (a count+age metric with a threshold), not fire once into a channel and go quiet — parked-forever is the new failure surface I2 introduces.

## 10. Rollout slices (each a reviewable PR; outbound 1–3 + inbound I1 pre-GA, rest post-GA)

1. **Contract + fixes (pre-GA)** — everything in `async-send-contract.md` (enum/param/events/spec regen; unswallow insert error; meter-after-persist).
2. **Persist-first, still inline (pre-GA)** — migration for `outbound_message_queue` (incl. `lease_token`, `max_attempts`) + reorder DeliverOutbound: accept-tx first, with idempotency `Complete` committing in the same tx as the durable acceptance point (**both** the accept-tx and `HoldForApprovalCore`'s hold insert — §6), then execute the job *inline in the request* via the worker code path. **Includes the minimal sweeper AND the fencing discipline** — the lease token + fenced writes are defined here, not deferred to slice 3, because the inline-executing request IS a concurrent worker the moment the sweeper exists (a slow inline request past its lease + a sweeper re-drive is the same double-send this slice must prevent). **Responses are NOT "identical to today":** once the accept-tx commits, at-least-once obligates delivery, so a transient SES failure or ramp-throttle can no longer return today's 500/throttle — the message *will* send via re-drive, and returning 500 would make the caller retry into a duplicate. Inline transient failures return **200 `accepted`** (the send is now the queue's job); only pre-accept-tx failures stay synchronous errors. This is the one behavior change slice 2 introduces, and it is the correct one.
3. **Worker pool + async default (pre-GA — GA freezes on this slice as the default path)** — pool, heartbeat + lease sizing, error-classified backoff, SES circuit breaker + pause switch, per-tenant-fair claim, terminal-failure guard, `email.failed`, `wait=sent`, `LISTEN/NOTIFY`, config flag `outbound.mode: sync|async` (sync = slice-2 behavior, kept one release as escape hatch). **The durable event tail is not a separate flag in async mode:** `outbound.mode=async` *implies* the outbox is the event path (validate at startup and refuse `async` + legacy-publisher config), so a self-hoster cannot run async with `WEBHOOKS_OUTBOX_ENABLED` off and silently lose failure events. The self-host default is `outbound.mode=sync` until they opt in, and opting into async turns the outbox on with it.
4. **Async scan + hold/block states** — move screening into the worker; `blocked` status decision (reserve it in the vocabularies now, contract §3.1); the block/reject/expiry **terminal signal** (contract §1.3 — these must emit `email.failed`-with-reason so an `accepted` send that is never sent still reaches a terminal push, not rest at `accepted` forever); digest/event wiring.
5. **Approve-path unification** — approve = re-enqueue; retire `send_attempts` writes; delete `markSideEffectCommitted`. **Until this lands, the GA approve path still publishes outcomes via the legacy fire-and-forget publisher (`hitlworker`) — contract §4.5's durability claim does not yet hold for approved-hold sends.** Either pull the approve-path outcome events onto the outbox in slice 3, or scope §4.5 explicitly to direct sends at GA and note approved-hold events as best-effort until slice 5.
6. **Batch endpoint** — per the batch plan, now trivial on top (atomic insert-N). Note the per-tenant-fairness interaction (§8): a 100-job batch tx must not starve the pool.

**Migration safety (all slices, per CLAUDE.md).** The I1 dedupe index and any index on the prod-sized `messages` table use `CREATE INDEX CONCURRENTLY` (a plain `CREATE UNIQUE INDEX` takes a write-blocking lock for the build, and migrations auto-apply at binary startup — a long build stalls the deploy and blocks inbound persistence). Preclear duplicates before adding a unique index, and give `email_message_id`/`body_hash` explicit non-null defaults so the index is well-defined on existing rows. Every package writing `outbound_message_queue`/`inbound_message_queue`/new `messages` columns gets DB-backed tests.

Inbound slices (independent track, small):

- **I1. Inbound guarantee (pre-GA)** — 451 on persist failure + `(agent_id, email_message_id)` dedupe index + conflict-reuse (§9). ~a day of work; closes the silent-loss bug without building the queue.
- **I2. `inbound_message_queue` + parse worker (post-GA)** — the DATA-handler shrink and async parse/scan per §9. Internal refactor, no contract surface.

## 11. Testing strategy

- Failure-injection suite: kill/panic at every row of the crash matrix (fake sender with accept/latency/error controls; assert exactly-one SMTP submit per msg_id except the documented residual window).
- **Fencing / slow-worker:** pause a worker past its lease mid-`Send`, let the sweeper take over, resume the original — assert the stale worker's mark-sent/delete/requeue all no-op (0 rows on the fenced write), exactly one `email.sent`, and no job-state corruption. Assert heartbeat extends the lease so a slow-but-alive worker is NOT reclaimed. This is the guard §4 now actually defines (the old test asserted a guard no section specified).
- **Terminal-failure guard:** crash after SES-accept on the final attempt → assert the sweeper does NOT mark `failed` when header-tagged provider evidence exists, and that header-matched `delivered` feedback overrides a local `failed` (Merge exception).
- **SES outage:** provider-connection errors for hours → assert messages defer (do not exhaust `max_attempts`), the pause switch stops claiming, and no false `email.failed` storm.
- Lease contention: two workers + SKIP LOCKED claim races; per-tenant fairness (one tenant's 100-job batch does not starve others).
- Idempotent replay: same-key retry at each stage returns same msg_id, zero extra submits; **held-send replay** (crash between hold-commit and `Complete`) does not double-hold.
- DB-backed tests for every package writing `outbound_message_queue` / `inbound_message_queue` / new `messages` columns (schema-change convention in CLAUDE.md).
- Contract tests: `accepted` shape, `wait=sent` per-outcome response table (sent/failed/held/timeout), replay-during-wait 409.
- Load: p99 accept latency target < 50ms; queue drain rate vs 60/min token budget.
- Inbound (I1): injected persist failure at DATA → assert 451 on the wire; same RFC `Message-ID` delivered twice → exactly one `messages` row and one `email.received` event; DB-backed tests for the new dedupe index (schema-change convention in CLAUDE.md).
- Inbound (I2): kill/panic matrix across the DATA tx and parse worker; lease takeover; parked-state entry + re-drive.

## 12. Open questions

Resolved by the launch-review hardening (were open, now decided in §§4–10): lease fencing mechanism (fencing token); lease size (12m + heartbeat); terminal-failure guard; SES-outage handling (classify + pause + re-drive); inbound dedupe (content-hash + horizon bound); parked-vs-dropped split; outbox mandatory in async mode; slice-2 response semantics.

Still open:

1. **`email.deferred`** — add as an event vs poll-only. Contract-surface, so **must decide before freeze** (not "during slice 4"); both the contract doc and peer practice (Resend, SendGrid push a deferred/delayed signal) lean add.
2. ~~Held-send response shape at GA~~ — **resolved (owner, 2026-07-04): 200 iff persisted, so holds return 200 `pending_review` from GA (202 retired as a success code). Contract §1.5.** The HTTP code stays 200 through slice 4's async-scan move, so there is no post-freeze breaking flip.
3. `blocked` representation — new `messages.status` value vs review-rejected family (keep the migration additive). Reserve the name in the vocabularies now (contract §3.1) even though it activates in slice 4.
4. Scan COGS control: batch/coalesce LLM scans per agent once scanning is async. Cost-only, no deadline.
5. Residual-window reconciler (header-tagged SNS feedback vs a `sending` row): alert-only v1, auto-heal later. (The terminal-failure guard already handles the more dangerous `failed`-over-`delivered` case inline.)
6. Per-tenant-fair claim mechanism (windowed claim vs per-agent in-flight cap) — decide in slice 3; the requirement (no pure-FIFO global claim) is fixed.
7. Inbound (I2): raw-blob retention for `parked` rows — cap size / age out to cold storage.

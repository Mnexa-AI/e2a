# Sending Ramp Hardening Design

**Status:** Proposed follow-up to draft PR #545 review  
**Date:** 2026-07-18

## Problem statement

PR #545 reserves daily recipient capacity before provider I/O, but it also
advances the ramp on the first reservation of each UTC day. A domain can
therefore reach the target after one recipient per day, and failed submissions
can advance the schedule. Reservations are keyed by `(message_id, day)`, so an
unsent message retried after midnight consumes another day's capacity. Ramp
deferrals have no terminal horizon, daily counters have no retention policy,
and several rollout-critical paths lack direct tests.

The hardened design must continue to enforce the cap before SMTP submission,
while making progression evidence-based, retries logically idempotent, queued
messages bounded, and operational state maintainable.

## Goals and non-goals

Goals:

- Enforce each UTC day's cap before provider submission.
- Advance the schedule only from meaningful provider-accepted recipient volume.
- Treat one message as one logical reservation across UTC-day retries.
- Give every accepted message a bounded terminal outcome.
- Bound counter and reservation storage.
- Preserve tenant isolation and organizational-domain sharing.
- Keep policy operator-managed and read-only to end users.
- Add direct coverage for adapters, migration backfill, concurrency, retries,
  retention, and rollout operations.

Non-goals:

- Depending on SES delivery notifications for progression. Those notifications
  are optional in current deployments.
- Building reputation scoring from bounce or complaint rates in this slice.
- Exposing reset or schedule mutation through the public API.
- Guaranteeing strict FIFO scheduling in River. The bounded horizon prevents an
  indefinite strand; fair admission can be designed separately if needed.

## Relevant context and constraints

- The River worker must reserve before SMTP I/O to prevent cap overshoot.
- `sent` means the upstream provider accepted the submission, not recipient
  delivery. This is the first durable outcome available in every deployment.
- Accepted messages must eventually produce `email.sent` or `email.failed`.
- Provider outcomes can be ambiguous. Capacity may be released only after an
  unambiguous terminal failure; ambiguous attempts retain their reservation
  until reconciliation.
- The schedule is snapshotted when an organizational-domain scope first arms.
- Existing verified domains are intentionally grandfathered during migration.

## Proposed design

### 1. Reservation and confirmation lifecycle

Replace the per-day reservation identity with one row per message:

- `message_id` is the primary key.
- `day` is the UTC day whose cap currently owns the reservation.
- `units` is the unique-recipient count.
- `state` is `reserved`, `confirmed`, or `released`.
- timestamps record creation and the latest transition.

Daily counters track both `reserved_count` and `confirmed_count`. The reserved
count is the cap-enforcement value. Confirmed count is provider-accepted volume
and is the progression value.

`Reserve` behaves as follows:

1. Validate the request, verified domain ownership, and persisted schedule.
2. Lock the exact domain and shared organizational-domain scope.
3. A same-message/same-day `reserved` or `confirmed` row is idempotent.
4. A pending reservation from an earlier day is moved atomically: subtract its
   units from the old day's reserved count, remove the stale pending row, then
   attempt to reserve today's cap. If today's cap is full, no new reservation is
   created and the job is deferred. It never counts against two days
   simultaneously.
5. Confirmed and released reservations are terminal and idempotent.

`Confirm(message_id)` runs after the worker has durably recorded `sent`. It
changes `reserved` to `confirmed` and increments the owning day's confirmed
count exactly once. If confirmation fails after `MarkSent`, the River re-drive's
already-done path retries confirmation before completing the job. This repairs
the `MarkSent`/confirmation crash window without resubmitting SMTP.

`Release(message_id)` changes a pending reservation to `released` and subtracts
its units from the reserved count. It is called only for definitive local or
provider failures. Ambiguous failures retain the reservation until existing
provider-evidence reconciliation resolves the outcome.

### 2. Evidence-based progression

A UTC day qualifies when its `confirmed_count` reaches at least 50% of the
counter's snapshotted `daily_limit`, rounded up. Qualifying a day increments the
scope's `active_days` at most once and records `last_qualified_day`.

The counter's daily limit never changes after its first reservation, so reaching
the threshold does not increase the current day's allowance. The higher limit
applies when the next UTC day's counter is created. When the final ramp day
qualifies, the scope becomes complete only on a later UTC day; this prevents the
last confirmation from making the remainder of the same day unlimited.

Provider acceptance is used rather than final delivery because it is available
in every deployment. A future reputation policy may additionally require
delivery, bounce, and complaint quality without changing the reservation
lifecycle.

### 3. Error classification and retry horizon

Ramp validation errors are typed as permanent:

- empty identifiers or zero recipients;
- missing domain;
- owner mismatch;
- invalid persisted schedule;
- impossible reservation state.

The worker terminally fails these messages rather than bypassing the ramp or
snoozing forever. Database and other infrastructure errors remain transient and
snooze for one minute.

Both ramp-capacity deferrals and transient ramp-store failures use the existing
72-hour `AcceptedAt` horizon. Once exceeded, the worker records a local terminal
failure with reason/detail `ramp_capacity_timeout`, releases any pending
reservation, and stops the River job. Ramp deferrals and their oldest age are
logged so operators can alert from queue telemetry.

Production schedules require `start_daily >= 50`, `target_daily >= start_daily`,
and `ramp_days >= 1` at config, constructor/store, and database boundaries. Bad
persisted data is rejected rather than silently clamped.

### 4. Retention and maintenance

Add a `sendramp` River maintenance job on the existing maintenance queue:

- Run daily.
- Delete daily counters older than 35 days.
- Delete terminal reservations older than 7 days.
- Retain nonterminal reservations until they are resolved, even if old, so a
  maintenance mistake cannot create duplicate capacity.

Add indexes beginning with `day` for counter pruning and with
`(state, updated_at)` for terminal reservation pruning. Remove the unused
reservation scope/day index.

The 35-day counter window is for operational diagnosis; progression is stored
on the scope and does not depend on historical counter rows.

### 5. Exemption rollout and reset

Keep migration-time exemption for pre-existing verified domains and runtime
exemption for domains that send while enforcement is disabled. Add an operator
runbook explaining that exemptions persist across later enablement.

The operator-only reset path is a documented transactional SQL procedure. It
requires explicit `user_id` and organizational-domain scope, clears that
scope's counters and reservations, deletes the scope, and moves the tenant's
matching exempt domain rows to `inactive`. It is deliberately not exposed by
the public API. The runbook includes preview queries and rollback guidance.

## Edge cases and failure handling

- A message reserved before midnight and retried after midnight moves capacity;
  it does not create a second logical reservation or confirmed send.
- A permanent provider rejection releases same-day pending capacity.
- An ambiguous connection failure retains capacity because SES may have accepted
  the message. Confirmation follows if provider evidence repairs it to `sent`.
- A crash after `MarkSent` but before confirmation is repaired by the
  already-done worker path.
- A day below 50% confirmed utilization does not advance, regardless of how many
  reservations or failures occurred.
- Concurrent sibling-domain reservations serialize through the shared scope
  lock and cannot exceed the shared cap.
- A final qualifying day does not become unlimited until a later UTC day.
- Maintenance never deletes active reservations.
- Invalid state fails closed to a terminal message outcome, not an unbounded
  retry and not an enforcement bypass.

## Scalability and extensibility

The daily write path remains one counter row per tenant/scope/day and one
reservation row per accepted message during its short retention window. Scope
locking intentionally serializes one tenant's organizational-domain sends; it
does not serialize unrelated tenants or domains. Retention indexes make cleanup
bounded and avoid full-table scans.

Separating reserved from confirmed volume permits future quality gates or
operator-controlled progression thresholds without weakening pre-send cap
enforcement.

## Verification strategy

Use test-first slices with explicit red/green evidence:

1. Store lifecycle tests: same-day idempotency, cross-day move, confirm/release,
   failed submissions not progressing, 50% qualification, delayed completion,
   sibling-domain sharing, tenant isolation, and concurrent cap enforcement.
2. Worker tests: confirmation after success, repair on already-done re-drive,
   release on definitive failure, retention on ambiguity, permanent ramp errors,
   and 72-hour ramp timeout.
3. Adapter tests: disabled-to-exempt branch and injected UTC clock.
4. Migration test: execute the actual migration against a transaction containing
   pre-existing verified, unverified, and newly created domain states.
5. Maintenance tests: retention boundaries and preservation of active rows.
6. Operator reset test or transaction fixture proving tenant/scope isolation.
7. CI: add `internal/sendramp` to the integration target, establish a measured
   package coverage floor, and run targeted `-race` tests for sendramp and the
   outbound worker.
8. Run affected Go suites, the full Go suite, build, OpenAPI/spec freshness, SDK
   generation freshness, and existing SDK tests before updating PR #545.

## Rollout and compatibility

PR #545 is still a draft and migration 067 is not released, so its schema can be
edited in place without a follow-up production migration. The API remains
read-only and additive. Existing verified senders remain exempt. New scopes use
the hardened lifecycle from their first eligible send.

## Open questions

None for this slice. The 50% threshold, 72-hour horizon, and retention windows
are operator policy constants for the initial implementation; making them
configurable is intentionally deferred until operational evidence justifies the
additional surface.

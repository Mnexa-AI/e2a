# Canonical Message Trust Ledger Design

Date: 2026-07-21
Status: Approved for implementation

## Context

e2a currently exposes useful but fragmented observations about a message. The
`messages` and `message_recipients` tables hold current-state rollups; the event
outbox records selected notifications for 30 days; review and suppression state
live in separate tables; and queue jobs carry additional processing history.
Those sources can explain portions of a message's journey, but no canonical
ordered record joins them together.

This design introduces an append-only Message Trust Ledger. Every newly
persisted inbound or outbound message receives an ordered lifecycle containing
only transitions e2a actually observed. The ledger becomes the source for a
new REST read surface and for lifecycle data attached to existing message
events. It does not claim inbox placement.

The `/v1` API is GA. The design therefore preserves every existing event name,
field, and message status. It adds one endpoint, new response schemas, and an
optional additive lifecycle field on event payloads. No existing contract is
reinterpreted or removed.

## Industry comparison

Current email providers validate the event-ledger model but leave useful gaps:

- [SendGrid Email Logs](https://www.twilio.com/docs/sendgrid/api-reference/email-logs)
  expose event-level per-message history aligned with Event Webhook data, but
  retain it for 30 days and do not paginate that read surface.
- [Postmark message details](https://postmarkapp.com/developer/api/messages-api)
  embed ordered `MessageEvents`, and its
  [delivery webhook](https://postmarkapp.com/developer/webhooks/delivery-webhook)
  explicitly defines delivery as recipient-server acceptance rather than
  inbox placement.
- [Mailgun Events](https://documentation.mailgun.com/docs/mailgun/user-manual/events/events)
  drive its API, webhooks, and UI from one event stream with cursor navigation.
- [SparkPost Events](https://developers.sparkpost.com/api/events/) retain both
  normalized and raw provider diagnostics with stable correlation fields, but
  searchable events are short-lived.
- [Resend event types](https://resend.com/docs/webhooks/event-types) clearly
  separate sent, delayed, delivered, bounced, failed, suppressed, and
  complained outcomes, while email retrieval exposes only a `last_event`
  rollup rather than a complete history.
- [Amazon SES notifications](https://docs.aws.amazon.com/ses/latest/dg/event-publishing-retrieving-sns-contents.html)
  provide detailed provider events but leave customers to assemble their own
  per-message ledger.

e2a will adopt the strongest patterns: one canonical REST/push representation,
precise recipient-server semantics, normalized reason codes plus bounded raw
evidence, per-recipient feedback, stable correlation identifiers, and
cursor-safe ordering. Unlike outbound-focused provider logs, e2a's lifecycle
also covers inbound acceptance and authentication and outbound review and
queue observations.

## Goals

1. Give every newly persisted message an append-only, deterministic lifecycle
   containing the transitions e2a observed.
2. Define one closed initial stage, outcome, and reason-code vocabulary.
3. Preserve the distinction among e2a acceptance, durable queueing, upstream
   submission, recipient-server acceptance, and inbox placement.
4. Persist lifecycle transitions atomically with the associated local message,
   queue, review, suppression, delivery-feedback, and event changes.
5. Make duplicate jobs and duplicate provider notifications idempotent without
   erasing distinct retry attempts or later observations.
6. Reconstruct useful facts for historical messages without inventing missing
   transitions or performing a production-sized table rewrite.
7. Expose deterministic, cursor-safe lifecycle reads across the Go API,
   OpenAPI, generated and handwritten SDK layers, CLI, MCP, and dashboard data
   client.
8. Ensure REST event polling, webhooks, and WebSocket frames cannot disagree
   with the canonical lifecycle rows they carry.

## Non-goals

- No screening, prompt-injection detection, phishing detection, or mappings
  from `email.flagged` or `email.blocked`.
- No aggregate analytics, charts, dashboards, funnels, or deliverability
  scoring.
- No open/click engagement tracking.
- No webhook-delivery or WebSocket-connection attempt history in the message
  lifecycle.
- No provider inbox-placement claim. `delivery / delivered` means the
  recipient mail server accepted the message.
- No rewrite of historical webhook event bodies.
- No exactly-once guarantee for external SMTP/provider calls. The ledger
  records e2a's observations around the existing at-least-once workers.

## Canonical contract

### Direction

`direction` is closed to:

- `inbound`
- `outbound`

It always matches the owning message row.

### Stages and outcomes

Stages describe the boundary at which e2a made an observation. Outcomes
describe what e2a observed at that boundary.

| Stage | Allowed outcomes | Meaning |
|---|---|---|
| `accepted` | `accepted` | e2a persisted the inbound SMTP or outbound API message |
| `authentication` | `passed`, `failed`, `indeterminate` | inbound SPF/DKIM/DMARC evaluation; only DMARC pass authenticates the RFC 5322 author domain |
| `review` | `pending`, `approved`, `rejected` | an observed HITL hold or resolution, without asserting why review was requested |
| `suppression` | `blocked`, `applied` | a persisted message was blocked by an existing suppression, or its feedback caused a suppression |
| `queued` | `enqueued` | durable inbound processing or outbound submission work was enqueued |
| `submission` | `accepted`, `deferred`, `failed` | an internal relay or upstream provider accepted or rejected submission, or a retryable submission attempt failed |
| `delivery` | `delivered`, `deferred`, `bounced` | per-recipient provider feedback after submission |
| `complaint` | `reported` | per-recipient feedback-loop complaint |

Stage order is descriptive, not a state-machine constraint. Provider feedback
can arrive out of order, and a later authoritative observation may correct a
locally inferred failure. The ledger preserves both observations in timestamp
order while existing message rollups retain their monotonic merge rules.

### Initial reason-code taxonomy

Every transition uses one of the following stable codes. A reason code fixes
its stage, outcome, and retryability; callers cannot supply contradictory
values. `retryable` means repeating the same logical operation could succeed
without changing its message content; it does not promise that e2a has
scheduled another attempt.

| Reason code | Stage / outcome | Retryable | Observation |
|---|---|---:|---|
| `acceptance.inbound_smtp` | `accepted / accepted` | false | inbound SMTP transaction persisted a message |
| `acceptance.outbound_api` | `accepted / accepted` | false | outbound API transaction persisted a message |
| `acceptance.local_loopback` | `accepted / accepted` | false | providerless local delivery persisted the receiving copy |
| `authentication.dmarc_pass` | `authentication / passed` | false | aligned DMARC passed |
| `authentication.dmarc_fail` | `authentication / failed` | false | DMARC alignment failed |
| `authentication.dmarc_none` | `authentication / indeterminate` | false | no applicable DMARC result was available |
| `authentication.dmarc_temporary_error` | `authentication / indeterminate` | true | DMARC evaluation had a transient error |
| `authentication.dmarc_permanent_error` | `authentication / indeterminate` | false | DMARC evaluation could not be completed without changing input/configuration |
| `review.hold_created` | `review / pending` | false | a review hold was persisted |
| `review.approved` | `review / approved` | false | a reviewer approved the hold |
| `review.rejected` | `review / rejected` | false | a reviewer rejected the hold |
| `review.expired_approved` | `review / approved` | false | expiry policy approved the hold |
| `review.expired_rejected` | `review / rejected` | false | expiry policy rejected the hold |
| `suppression.recipient_blocked` | `suppression / blocked` | false | a persisted message could not proceed because a recipient was suppressed |
| `suppression.hard_bounce_applied` | `suppression / applied` | false | hard-bounce feedback created a recipient suppression |
| `suppression.complaint_applied` | `suppression / applied` | false | complaint feedback created a recipient suppression |
| `queue.inbound_processing` | `queued / enqueued` | false | durable inbound processing work was enqueued |
| `queue.outbound_submission` | `queued / enqueued` | false | durable outbound submission work was enqueued |
| `submission.upstream_accepted` | `submission / accepted` | false | an upstream SMTP/provider accepted submission |
| `submission.local_loopback_accepted` | `submission / accepted` | false | atomic local relay delivery completed |
| `submission.temporary_failure` | `submission / deferred` | true | a submission attempt failed transiently and may retry |
| `submission.provider_rejected` | `submission / failed` | false | the provider explicitly rejected submission |
| `submission.local_retries_exhausted` | `submission / failed` | true | e2a exhausted retries without authoritative provider rejection |
| `submission.cancelled` | `submission / failed` | false | local message state deliberately cancelled pending submission |
| `delivery.recipient_server_accepted` | `delivery / delivered` | false | the recipient server accepted the message; inbox placement is unknown |
| `delivery.temporary_delay` | `delivery / deferred` | true | provider reported a temporary delivery delay |
| `delivery.permanent_bounce` | `delivery / bounced` | false | provider classified a permanent bounce |
| `delivery.transient_bounce` | `delivery / bounced` | true | provider classified a transient bounce |
| `delivery.undetermined_bounce` | `delivery / bounced` | false | provider reported a bounce without a stronger classification |
| `complaint.recipient_reported` | `complaint / reported` | false | provider reported a feedback-loop complaint |

There is no generic reason code whose stage, outcome, or retryability changes
at runtime. Unknown provider text is evidence attached to the closest proven
stable observation; if e2a cannot classify the observation itself, it does not
append a transition until the catalog is deliberately extended. New meanings
require adding a reason-code catalog entry rather than placing arbitrary text
in `reason_code`.

### Transition representation

The canonical wire and in-memory transition is:

```json
{
  "id": "mlt_...",
  "message_id": "msg_...",
  "direction": "outbound",
  "recipient": "person@example.com",
  "stage": "delivery",
  "outcome": "delivered",
  "reason_code": "delivery.recipient_server_accepted",
  "retryable": false,
  "evidence": {
    "smtp_detail": "250 2.0.0 accepted"
  },
  "correlation_ids": {
    "event_id": "evt_...",
    "job_id": "1234",
    "provider_message_id": "provider-id",
    "provider_event_id": "provider-event-id"
  },
  "occurred_at": "2026-07-21T12:00:00Z",
  "reconstructed": false
}
```

`recipient` is present only for recipient-specific suppression, delivery, and
complaint observations. Correlation values are strings so identifiers from
different providers and River can share one stable schema. Empty values are
omitted.

`evidence` is an open JSON object at the wire boundary but is built only by
canonical server constructors. Initially allowed evidence is:

- the existing structured inbound `authentication` object;
- SMTP status/detail strings;
- normalized bounce type and bounded provider bounce subtype;
- existing failure reason and machine-readable failure code;
- review resolution provenance; and
- suppression scope/source.

It never contains message bodies, raw MIME, arbitrary headers, credentials,
webhook secrets, or provider request/response bodies. Individual diagnostic
strings are capped at 2 KiB and serialized evidence at 16 KiB. Unknown keys
remain tolerated by clients for additive compatibility.

## Persistence model

Migration `073` creates two new tables on an empty path:

1. `message_lifecycle_reason_codes` is the database catalog of allowed reason
   code, stage, outcome, and retryability tuples. Adding a future reason is an
   idempotent insert rather than a constraint rewrite on the ledger. Catalog
   entries are never updated after use; a semantic correction receives a new
   code.
2. `message_lifecycle_transitions` stores the append-only observations.

The transition table contains:

- `id TEXT PRIMARY KEY`;
- `message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE`;
- `dedupe_key TEXT NOT NULL`;
- `direction`, optional `recipient`, `stage`, `outcome`, `reason_code`, and
  `retryable`;
- `evidence JSONB NOT NULL DEFAULT '{}'`;
- `correlation_ids JSONB NOT NULL DEFAULT '{}'`;
- `occurred_at TIMESTAMPTZ NOT NULL`;
- `reconstructed BOOLEAN NOT NULL DEFAULT false`; and
- `recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()`.

A composite foreign key from `(reason_code, stage, outcome, retryable)` to the
catalog prevents contradictory persisted combinations. Closed checks cover
direction and the initial stage/outcome vocabularies on the small catalog
table. The transition table has:

- `UNIQUE (message_id, dedupe_key)` for logical idempotency; and
- an index on `(message_id, occurred_at, id)` for lifecycle reads.

Both tables and indexes are created idempotently. Because the transition table
starts empty, index creation and foreign-key validation do not scan or rewrite
the production-sized `messages` table. The migration is forward-only.

Deleting or retention-purging a message cascades to its lifecycle. Before
message deletion, application code never updates or deletes a transition.

## Idempotency and atomicity

The canonical store exposes transaction-bound append operations. A producer
must provide a deterministic dedupe key derived from its durable observation:

- acceptance and initial queueing use fixed message-local keys;
- River work uses job ID plus the logical attempt/outcome;
- event-backed observations use the stable event ID plus stage/recipient;
- provider feedback uses provider event identity plus recipient and outcome;
- review decisions use the durable review resolution identity; and
- suppression application uses the suppression row identity plus message and
  recipient.

`INSERT ... ON CONFLICT (message_id, dedupe_key) DO NOTHING`, followed by a
read of the winning row, makes duplicate delivery return the exact original
transition. Reusing a dedupe key with different semantic content is an error,
not a silent success.

Whenever e2a controls a local transaction, the transition is inserted in the
same transaction as the corresponding message state, recipient rollup, River
job insertion, review/suppression change, and event outbox row. A lifecycle
write failure rolls back that local state change.

An external SMTP/provider call cannot participate in the database transaction.
The existing worker performs the call, then atomically stores the observed
result, message state, lifecycle transition, and event. Existing provider
correlation and failure-provenance correction rules remain responsible for the
crash window around provider acceptance.

## Event mapping and cross-channel consistency

Newly emitted message events carry an additive `lifecycle_transitions` array in
their `data`. Every array item is the exact canonical transition returned by
the ledger store, not an independently rebuilt projection.

| Event | Lifecycle transition(s) |
|---|---|
| `email.received` | `accepted`; plus `authentication` when inbound SMTP authentication was evaluated |
| `email.sent` | `submission / accepted` |
| `email.failed` | `submission / failed` |
| `email.delivered` | per-recipient `delivery / delivered` |
| `email.bounced` | per-recipient `delivery / bounced` |
| `email.complained` | per-recipient `complaint / reported` |
| `email.review_requested` | `review / pending` |
| `email.review_approved` | `review / approved` |
| `email.review_rejected` | `review / rejected` |
| `domain.suppression_added` | message-specific `suppression / applied` only when the event carries a causal message ID |

`agent.suppression_added` and global/manual suppression configuration changes
do not create message transitions without an observed message. A persisted
message blocked by a suppression creates its own message-specific transition.
Screening events are explicitly excluded.

The event outbox stores the complete envelope once. REST `/v1/events`, webhook
delivery, event redelivery, and WebSocket push continue to serialize that same
stored envelope. WebSocket currently pushes `email.received`; its lifecycle
array is therefore byte-identical to the webhook/event-log representation.
Tests compare embedded transition objects to the lifecycle endpoint response.

Historical event bodies are not rewritten and need not contain the additive
field.

## Historical reconstruction

Migration `073` does not bulk-insert transitions. The lifecycle read service
merges persisted rows with deterministic reconstructed entries for historical
messages that predate ledger writes.

Reconstruction uses only durable facts with a defensible timestamp:

- `messages.created_at` proves message acceptance;
- non-null inbound `messages.authentication` proves authentication evaluation;
- `messages.status`, `reviewed_at`, and approval expiry/resolution fields prove
  review states when their timestamps exist;
- `messages.send_job_id` and durable River metadata can prove queueing;
- `messages.provider_accepted_at`, provider message ID, and delivery status can
  prove provider acceptance;
- `message_recipients.status` and `updated_at` prove current per-recipient
  feedback; and
- retained event rows can prove mapped observations with their original event
  timestamp.

A current-state rollup proves only that the outcome was observed; it does not
prove unrecorded intermediate states. For example, a historical `delivered`
recipient may yield reconstructed acceptance and delivery entries when their
timestamps are available, but never an invented queue or retry history.

Reconstructed entries use the same semantic reason code as a newly observed
fact, deterministic `mlt_recon_...` IDs and dedupe keys derived from the
message, source field, recipient, stage, outcome, and source timestamp. They
set `reconstructed: true` and identify the durable source field in evidence.
Ambiguous historical failures are omitted rather than assigned a guessed
reason. Persisted rows with the same semantic dedupe key win, so rolling
deployment cannot duplicate a reconstructed fact.

The merge is bounded to one message and performed before cursor slicing. A
message lifecycle is expected to contain tens, not thousands, of transitions;
no account-wide reconstruction scan occurs.

## REST API

Add:

```text
GET /v1/agents/{email}/messages/{message_id}/lifecycle
```

Authorization and resource-hiding behavior match the existing message-detail
endpoint. A message not owned by the addressed agent returns the same not-found
contract as a missing message.

Query parameters:

- `limit`: default 50, maximum 100;
- `cursor`: opaque cursor bound to agent, message, and sort direction.

The response contains `items` and nullable `next_cursor`. Items are ordered
ascending by `(occurred_at, id)`. The next page uses strict tuple comparison,
so equal timestamps cannot skip or repeat a transition. The cursor includes a
version and filter binding and rejects malformed or cross-message reuse with
the repository's existing invalid-cursor error contract.

The endpoint is read-only. It does not persist reconstructed entries.

## Client surfaces

The Huma handler is the OpenAPI source of truth. After it lands, repository
commands regenerate `api/openapi.yaml`, the TypeScript generated SDK, and the
Python generated SDK; generated files are never edited by hand.

Handwritten surfaces add:

- TypeScript `E2AClient.messages.getLifecycle(...)`;
- Python async and sync `messages.get_lifecycle(...)`;
- CLI `e2a messages lifecycle <message-id> --agent <email>` with JSON output
  following existing global output conventions;
- MCP `get_message_lifecycle` with agent email, message ID, limit, and cursor;
  and
- dashboard API types and fetch helper only, with no page or visualization.

All clients tolerate additive evidence and correlation keys. Stage, outcome,
and reason code are generated closed enums for the initial contract; adding a
future value follows the repository's additive-enum compatibility policy and
requires coordinated SDK regeneration.

## Failure handling

- Unknown direction, stage, outcome, or reason combinations fail canonical
  construction before SQL execution.
- Oversized or forbidden evidence fails closed and rolls back the associated
  local transaction.
- A duplicate dedupe key with identical content returns the original row; a
  semantic mismatch returns an internal consistency error and does not mutate
  state.
- Provider diagnostic text never becomes a reason code. A known event uses
  its stable semantic code and keeps bounded provider text as evidence; an
  unknown event is omitted until its meaning is deliberately cataloged.
- Out-of-order feedback appends the observation but leaves message rollups
  governed by the existing monotonic precedence and correction logic.
- A malformed or mismatched cursor returns the existing stable invalid-cursor
  error.
- Historical reconstruction omits facts whose source or timestamp is
  ambiguous.

## Compatibility and rollout

The change is additive:

- one new endpoint and schemas;
- optional additive `lifecycle_transitions` fields on mapped event payloads;
- two new tables and indexes; and
- new client methods/commands/tools.

Existing message status fields, event names, event envelope version, webhook
signatures, redelivery behavior, and WebSocket close semantics do not change.
Event consumers must already tolerate additive fields under the GA event
contract. No feature flag or destructive migration is required.

Deploy the migration before code begins writing transitions, as happens in the
normal embedded auto-migration startup. Mixed-version processes remain safe:
old processes ignore the new tables and fields; new processes reconstruct
facts for messages written by old processes during rollout.

## Verification strategy

### Unit tests

- Exhaustively validate every catalog reason's stage, outcome, and
  retryability mapping.
- Reject every invalid direction and reason/stage/outcome combination.
- Validate safe-evidence allowlists and size bounds.
- Cover authentication status mapping, review outcomes, submission failure
  provenance, bounce classification, complaint, suppression, and fallback
  handling.
- Prove reconstruction never invents intermediate transitions and generates
  stable IDs and ordering.
- Cover cursor encoding, filter binding, equal timestamps, and invalid input.

### Integration tests

- Prove message acceptance, queue insertion, and lifecycle transitions commit
  or roll back together for inbound and outbound flows.
- Prove review, delivery feedback, suppression application, message rollup,
  lifecycle transition, and event outbox changes are atomic.
- Deliver the same River job and provider feedback twice and assert one logical
  transition.
- Deliver distinct retry attempts and assert distinct observations.
- Assert the lifecycle transition embedded in REST events, webhook envelopes,
  and WebSocket frames equals the transition returned by the lifecycle
  endpoint.
- Verify historical reconstruction and pagination against a private test
  database with `-tags integration -p 1`.

### Contract and repository checks

- Focused Go unit and integration packages while iterating.
- `make test-unit`.
- Affected integration packages with `-tags integration -p 1` and the private
  worktree database.
- TypeScript SDK, Python sync/async, CLI, MCP, and dashboard contract/unit
  tests.
- `make spec-check`.
- `make generate-sdk-check`.
- `make openapi-compat-check`.
- Broader verification proportionate to the packages changed.

## Remaining risks

- External provider acceptance cannot be made atomic with PostgreSQL. Existing
  provider correlation, idempotent worker behavior, and failure-provenance
  correction reduce but do not eliminate the SMTP-accept/database-crash
  residual.
- Historical rows have incomplete timestamps. Reconstruction will therefore
  be useful but intentionally sparse.
- Adding lifecycle arrays increases event payload size. The bounded evidence
  contract and small number of transitions per event keep the increase
  controlled.
- Closed generated enums require coordinated client regeneration whenever the
  taxonomy expands. This is intentional: reason-code changes are contract
  changes, not provider pass-through.

## Open questions

None. Lifecycle-only scope, exclusion of screening and prompt-injection
detection, vocabulary, persistence, reconstruction, endpoint ordering,
cross-channel representation, and client-surface scope were approved during
design review.

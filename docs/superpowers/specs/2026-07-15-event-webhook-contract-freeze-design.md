# Event and Webhook Contract Freeze

Date: 2026-07-15
Status: Approved for pre-GA implementation

## Context

e2a publishes the same logical events through three surfaces: signed webhook
deliveries, the durable `/v1/events` reconciliation log, and real-time
WebSocket push. The current implementation already has a deliberately open
event envelope, named Go/OpenAPI payload types for nine stable events, and
full/minimal golden fixtures shared by the server and both SDKs. The remaining
pre-GA work is to turn those pieces into one explicit, mechanically enforced
contract and freeze delivery behavior beside the payload contract.

The contract must preserve two properties that pull in opposite directions:

1. known stable event payloads need strong, published schemas; and
2. clients must continue accepting new event types and additive fields without
   waiting for an SDK release.

A closed OpenAPI `oneOf` or discriminator union would satisfy the first
property at the expense of the second. This design therefore keeps the wire
envelope open and publishes the stable event-to-payload association as
metadata rather than as a validation boundary.

## Industry comparison

The chosen shape follows the strongest parts of current email-provider
practice while making the compatibility boundary more explicit:

- Resend uses the closest envelope, `{type, created_at, data}`, documents each
  event payload separately, delivers at least once, signs raw request bytes
  with Svix headers, and retries for roughly 27.5 hours.
- Amazon SES uses `eventType` plus a type-named sibling object such as
  `bounce`, `delivery`, or `complaint`. This is explicit but adds a new
  top-level property for every event family.
- SendGrid sends batches of flat, varying records distinguished by `event`,
  supplies a unique event id for deduplication, treats any 2xx as success, and
  retries for up to 24 hours.
- Postmark publishes separate top-level payloads distinguished by
  `RecordType`; it requires 200, treats 403 as terminal, and varies retry
  schedules by event class.
- Mailgun publishes event-specific nested data and signs a timestamp plus
  random token; 200 succeeds, 406 is terminal, and other responses retry for
  roughly eight hours.
- SparkPost batches nested event records, exposes event-documentation and
  sample endpoints, and retries failed batches for eight hours.

None of these providers relies on a public closed OpenAPI event union as the
primary compatibility boundary. e2a will use the Resend-style generic
envelope, add a stable event id and explicit schema version, and publish a
machine-readable stable payload map plus schema-validated golden fixtures.

## Goals

1. Freeze one canonical webhook/WebSocket envelope without closing it to
   future event types or fields.
2. Publish an explicit event-type to payload-schema mapping for every stable
   event.
3. Validate golden examples against both the generic envelope and their
   mapped stable payload schema.
4. Freeze schema version, signing bytes, webhook headers, secret rotation,
   retries, at-least-once semantics, and WebSocket close behavior together.
5. Ensure Go, OpenAPI, TypeScript, Python, docs, and fixtures cannot drift
   independently.

## Non-goals

- Graduating the beta screening and review events to stable.
- Defining payload schemas for beta or unknown future events.
- Generating a closed discriminated event union in either SDK.
- Changing the REST event-log resource's enrichment or retention semantics.
- Introducing AsyncAPI as a second source of truth. A future AsyncAPI document
  may be generated from the canonical catalog.
- Adding email engagement events such as opens or clicks.
- Guaranteeing webhook ordering or exactly-once delivery.

## Design

### 1. Canonical wire envelope

Publish a named `EventEnvelope` OpenAPI component for the body delivered by
webhooks and WebSocket frames:

```yaml
EventEnvelope:
  type: object
  additionalProperties: true
  required: [type, id, schema_version, created_at, data]
  properties:
    type:
      type: string
      description: Open event-type string; consumers must tolerate unknown values.
    id:
      type: string
      description: Stable across retries and channels; consumer deduplication key.
    schema_version:
      type: string
      description: Open version string; the current server emits "1".
    created_at:
      type: string
      format: date-time
    data:
      type: object
      additionalProperties: true
```

`type` is intentionally not an enum. `data` is intentionally not a `oneOf`,
`anyOf`, or discriminator. The envelope and all stable response-direction
payload schemas remain open to additive fields.

The current server output pins `schema_version` to the string `"1"`. The
consumer-facing schema deliberately leaves it as an open string so a future
version still reaches application code instead of failing generic parsing.
Consumers branch on the version before applying version-specific typed
narrowing. The server changes the emitted value only for an incompatible
change to the five-field envelope contract. Adding a new event type, adding an
optional payload field, or adding a new optional envelope field does not bump
it.

`EventEnvelope` and the REST `EventJSON` are related but distinct:

- `EventEnvelope` is the canonical push wire object.
- `EventJSON` is the enriched `/v1/events` resource representation and also
  contains event-log state such as `status`, routing fields, and
  `delivery_status`.

The shared fields in `EventJSON` must retain the same meaning and types, but a
webhook body must not be validated against `EventJSON`, because webhook bodies
do not carry the REST-only required fields.

### 2. Stable event catalog and schema mapping

Create one internal stable-event catalog whose entries contain:

- event type;
- OpenAPI component name;
- canonical Go payload type;
- full fixture name;
- optional minimal fixture name.

The current stable catalog is:

| Event type | Payload component |
|---|---|
| `email.received` | `EmailReceivedData` |
| `email.sent` | `EmailSentData` |
| `email.failed` | `EmailFailedData` |
| `email.delivered` | `EmailDeliveredData` |
| `email.bounced` | `EmailBouncedData` |
| `email.complained` | `EmailComplainedData` |
| `domain.sending_verified` | `DomainSendingVerifiedData` |
| `domain.sending_failed` | `DomainSendingFailedData` |
| `domain.suppression_added` | `DomainSuppressionAddedData` |

The OpenAPI `EventEnvelope.data` schema publishes that association through a
non-constraining extension:

```yaml
x-e2a-event-data-schemas:
  email.received: "#/components/schemas/EmailReceivedData"
  email.sent: "#/components/schemas/EmailSentData"
  email.failed: "#/components/schemas/EmailFailedData"
  # ...one entry per stable event
```

The extension's values are JSON Pointer strings, not embedded `$ref` objects.
This keeps the extension easy for documentation and conformance tools to
consume while preventing generic OpenAPI generators from interpreting it as a
closed model union.

The beta events remain in the event vocabulary and remain parseable, but are
excluded from the stable mapping:

- `email.flagged`
- `email.blocked`
- `email.review_requested`
- `email.review_approved`
- `email.review_rejected`

They continue to carry `x-experimental-values` markers wherever users select
event types. Graduating one requires adding its canonical payload type,
mapping, fixtures, SDK typing/narrowing helpers, documentation, and tests in
the same change.

Contract tests require the stable and experimental catalogs to form an exact,
non-overlapping partition of the known event vocabulary. Every mapping target
must resolve to an emitted OpenAPI component, and no stable component may be
closed to additive fields.

### 3. Golden fixture contract

The existing full and required-fields-only fixtures remain the canonical wire
examples. Every stable event has one full fixture; every stable payload with
optional fields also has a `.min.json` fixture.

Each fixture is checked in four independent ways:

1. byte-for-byte serialization from its canonical Go event;
2. complete-document validation against emitted `EventEnvelope`;
3. `data` validation against the component selected by
   `x-e2a-event-data-schemas[fixture.type]`; and
4. parsing and stable-event narrowing in both handwritten SDKs.

An additional future-event fixture uses an unknown `type`, unknown envelope
field, and unknown `data` fields. It must validate against `EventEnvelope` and
parse through both SDKs without narrowing to a known stable event. This is the
forward-compatibility regression lock.

Schema validation must consume the live Huma-emitted document rather than a
parallel handwritten schema. `make spec-check` then guarantees that the
committed `api/openapi.yaml` is the same document.

### 4. SDK representation

The handwritten TypeScript and Python webhook event bases remain open:

- the envelope requires `type`, `id`, `schema_version`, `created_at`, and
  `data`;
- `type` remains `string`, not a literal union;
- `schema_version` remains an open `string`, while the current server and
  fixtures are separately pinned to `"1"`;
- `data` remains `unknown`/`Any` before narrowing;
- extra envelope and payload fields are retained or tolerated;
- explicit guards narrow only the nine stable event types and envelope version
  `"1"`.

Generated REST SDK models may describe `EventJSON`, but they do not replace
the handwritten push-envelope type. Unknown event values and unknown future
error codes must not throw during parsing.

### 5. Webhook signature contract

Freeze webhook signing as follows:

- key: the endpoint's `whsec_...` signing secret;
- algorithm: HMAC-SHA256;
- timestamp: base-10 Unix seconds;
- signed bytes: ASCII `timestamp`, one literal `.`, then the exact raw HTTP
  request body bytes;
- digest encoding: lowercase hexadecimal;
- signature header:
  `X-E2A-Signature: t=<unix>,v1=<current>[,v1=<previous>]`;
- signature comparison: constant time;
- default SDK replay tolerance: 300 seconds in either clock direction;
- JSON parsing occurs only after signature verification.

Reserializing parsed JSON is never valid verification input. Whitespace,
escaping, and key ordering are part of the signed body bytes.

Secret rotation immediately makes the new secret current. For exactly 24
hours, deliveries contain a second `v1=` signature made with the previous
secret. Receivers accept a match against any supplied active secret and any
`v1` value. After the grace deadline, the previous signature is omitted.

One deterministic golden signing vector freezes the timestamp, raw body,
current secret, previous secret, single-signature header, and dual-signature
header. Go, TypeScript, and Python must all produce or verify the same vector.

### 6. Webhook request headers

Every delivery freezes these headers:

| Header | Contract |
|---|---|
| `Content-Type` | `application/json` |
| `X-E2A-Signature` | Signing format above; required |
| `X-E2A-Event-Type` | Equals the signed body's `type`; required |
| `X-E2A-Schema-Version` | Equals the signed body's `schema_version`; required and currently `1` |
| `User-Agent` | `e2a-webhooks/1` |

HTTP header names remain case-insensitive. The event type and schema version
headers allow routing before JSON parsing, but the signed body remains the
source of truth. A mismatch is a server contract violation and must fail the
delivery conformance tests.

The event id is already inside the signed body and is stable across retries,
so this freeze does not add a second delivery-id or event-id header. A future
header may be added compatibly, but consumers must deduplicate on body `id`.

### 7. Webhook delivery and retry contract

Delivery remains at least once. The Layer 2 delivery row and River job are
committed together on the normal outbox path, and a reconciler re-enqueues
stranded pending rows on separate-transaction paths. A receiver may see a
duplicate if its HTTP response succeeds and the worker crashes before recording
`delivered`; consumers deduplicate on the event `id`.

One HTTP attempt has these semantics:

- no redirects are followed;
- the attempt timeout is 15 seconds;
- any 2xx response is success;
- network errors, timeouts, 3xx, 4xx, and 5xx responses fail the attempt;
- receiver `Retry-After` is not honored in v1;
- a disabled endpoint snoozes for one hour without consuming an attempt;
- a deleted endpoint terminates its delivery row rather than retrying forever.

There are eight total HTTP attempts. After failures 1 through 7, retries are
scheduled at these exact relative delays:

| Failed attempt | Delay before next attempt |
|---:|---:|
| 1 | 1 minute |
| 2 | 5 minutes |
| 3 | 15 minutes |
| 4 | 1 hour |
| 5 | 4 hours |
| 6 | 8 hours |
| 7 | 16 hours |

The eighth and final attempt therefore begins approximately 29 hours 21
minutes after the initial attempt. The delivery becomes terminally `failed`
after that attempt fails. This fixes the current indexing bug that skips the
one-minute entry and removes the inaccurate “eight attempts over ~72h” claim.

Tests use an injected clock or inspect relative schedules; they must not sleep.
They pin every attempt-to-delay pair, the total attempt count, disabled snooze,
success classification, retry classification, terminal transition, and
at-least-once crash/re-drive behavior.

### 8. WebSocket envelope and close contract

Every server-pushed WebSocket data frame validates against `EventEnvelope`.
The current channel emits `email.received`, but clients must accept unknown
future types. A webhook and live WebSocket publication of the same event use
the same event id and typed data. JSON byte ordering is not contractual.

The reconnect/termination matrix is frozen:

| Code | Reason | Classification | Client behavior |
|---:|---|---|---|
| 1000 | empty | normal | stop |
| 1001 | `shutting_down` | transient | reconnect with backoff |
| 1001 | `ping_timeout` | transient | reconnect with backoff |
| 1006 | synthesized locally | transient | reconnect with backoff |
| 1008 | human-readable policy rejection | terminal | stop |
| 1011 | human-readable server failure | transient | reconnect with backoff |
| 4000 | `replaced` | terminal, typed | stop and surface replacement |
| 4001–4999 | reserved/unknown | terminal | stop |

Handshake failures remain canonical HTTP error envelopes before upgrade:
missing or invalid credentials return 401, a credential forbidden for the
requested agent returns 403, and an absent or cross-tenant agent returns 404.
They are fatal and are never represented as close code 1008.

One table-driven contract fixture should drive the Go server tests,
TypeScript/Python reconnect classifiers, and CLI listener behavior so no
surface develops its own close-code interpretation.

### 9. Documentation and source-of-truth rules

Public documentation must state:

- the open-envelope compatibility rule;
- the complete stable event mapping;
- the beta event inventory;
- `schema_version` bump policy;
- exact signing input, signature/header syntax, replay tolerance, and rotation
  grace;
- every required delivery header;
- exact retry status classification and schedule;
- at-least-once and deduplication requirements;
- the complete WebSocket close/reconnect table.

Tests compare the documented stable mapping and retry/close inventories to
their canonical code catalogs where practical. Prose may explain the contract,
but it must not be the only source for a machine-relevant list.

## Compatibility consequences

The following become GA-frozen and require a major API version to remove,
rename, narrow, or reinterpret:

- the five required envelope fields and their meanings;
- emitting `schema_version: "1"` for this envelope shape (consumers still
  parse unknown version strings generically);
- every required field in a stable event payload;
- stable event type names;
- signing algorithm, signed-byte formula, and required headers;
- 24-hour dual-sign rotation grace;
- retry classification, count, and schedule;
- at-least-once semantics and stable event-id deduplication;
- WebSocket close codes/reconnect classifications.

Compatible evolution includes:

- adding an optional envelope or stable payload field;
- adding a new event type;
- graduating a beta event through the full catalog/fixture process;
- adding an optional webhook header;
- adding a new terminal application close code in the reserved 4xxx range;
- publishing generated AsyncAPI or other documentation from the same catalog.

Changing a beta event payload remains allowed until graduation. Unknown event
types and additive fields must remain accepted by all supported SDK versions.

## Verification

The implementation must add or extend tests for:

1. emitted `EventEnvelope` required fields, openness, and schema version;
2. exact stable mapping, resolvable component references, and stable/beta
   partition coverage;
3. every full/minimal fixture against both the generic envelope and mapped
   payload schema;
4. an unknown future event through OpenAPI validation and both SDK parsers;
5. stable event narrowing and required envelope fields in TypeScript/Python;
6. deterministic single/dual HMAC golden vectors across Go/TS/Python;
7. raw-body sensitivity, five-minute replay tolerance, and 24-hour rotation;
8. exact webhook headers and header/body type/version agreement;
9. all success/failure HTTP classifications and the exact retry table;
10. terminal attempt reconciliation and duplicate-safe at-least-once re-drive;
11. WebSocket envelope validation and the shared close-code matrix;
12. OpenAPI golden and generated SDK freshness.

Required gates:

- focused `internal/eventpayload`, `internal/httpapi`, `internal/webhook`,
  `internal/webhookdelivery`, and `internal/ws` tests;
- TypeScript SDK build, webhook/WS tests, and type tests;
- Python webhook/WS tests and mypy;
- MCP and CLI tests affected by event/listener types;
- `make spec-check`;
- `make generate-sdk-check`;
- full repository CI before merge.

## Rollout and rollback

This is a pre-GA contract freeze with no data migration. The OpenAPI component,
mapping metadata, fixture validation, SDK typing corrections, documentation,
and retry-index fix ship together. Existing queued River deliveries adopt the
corrected schedule on their next failed attempt; no stored payload bytes are
rewritten.

Before GA, rollback is a normal revert. After GA, rollback must not remove the
published envelope, stable payload mapping, signing/header guarantees, or
close-code meanings. Operational retry tuning after GA must preserve the
frozen attempt count and minimum timing guarantees or ship under a new contract
version.

## Acceptance criteria

- A consumer can parse an unknown future event without an SDK update.
- Every stable event maps to exactly one named, open payload component.
- Every stable fixture validates against both `EventEnvelope` and that mapped
  component.
- Webhook and WebSocket event bodies share the same required envelope.
- Go, TypeScript, and Python agree on deterministic HMAC vectors.
- Required webhook headers agree with the signed body.
- Failed delivery retries first after one minute and follows the exact
  eight-attempt schedule.
- The WebSocket reconnect/terminal matrix is identical across server, SDKs,
  MCP/CLI consumers, and documentation.

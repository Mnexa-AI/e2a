# Canonical API Contract Cleanup

Date: 2026-07-15
Status: Proposed for pre-GA implementation

## Context

The July 2026 full-stack conformance sweep found that the core `/v1` runtime,
OpenAPI document, generated SDKs, MCP server, and CLI are largely aligned after
PRs #478–#485. The remaining canonical REST-contract findings are precision
gaps rather than a request to redesign the API: one validation property is not
required, emitted response headers are prose-only, the human error-code table
is missing codes already present in the server and OpenAPI catalog, trash read
semantics are implicit, and the public experimental-operation inventory is not
machine-checked.

This cleanup happens before GA because tightening these contracts afterward
could force downstream users through avoidable breaking changes.

## Goals

1. Make validation-error fields structurally predictable in every generated
   client.
2. Publish the response headers integrations may rely on and verify their live
   behavior.
3. Keep the server, OpenAPI error vocabulary, and human documentation in exact
   sync.
4. Freeze the intended direct-read behavior for trashed messages.
5. Publish and drift-check the exact set of operations excluded from the GA
   compatibility promise.

## Non-goals

- Redesigning event or webhook payload polymorphism. That is the next pre-GA
  sequence item.
- Changing delivery engines, retry algorithms, idempotency semantics, or
  message state transitions.
- Renaming paths, operation IDs, fields, or existing error codes.
- Promoting templates, protection configuration, or trash operations from
  experimental to stable.
- Adding a general OpenAPI breaking-change detector. That belongs to the GA
  compatibility-gate sequence item.

## Design

### 1. Required validation locations

`FieldError` will always serialize both `location` and `message`.

- Remove `omitempty` from `FieldError.Location`.
- Keep the Go type as a non-pointer `string`.
- Preserve Huma's field path for field-specific failures, such as `body.to`,
  `query.limit`, or `path.id`.
- Use the empty string for a request-wide validation detail that has no single
  field location.
- Keep `ValidationErrorDetails.fields` required, non-null, and possibly empty.

This changes the generated TypeScript and Python `FieldError` models so
`location` is a required, non-null string. It does not change the envelope or
the `invalid_request` code.

### 2. Response-header contract

The generated OpenAPI document will define reusable header components and
reference them from response objects instead of repeating ad hoc definitions.

#### X-Request-Id

Every HTTP response under `/v1`, successful or unsuccessful, guarantees an
`X-Request-Id` string response header. OpenAPI response-header objects cannot
express required presence, so the component description states the guarantee
and live tests enforce it. The existing request-ID middleware remains the
runtime source of the value. Error responses continue to echo the same value in
`error.request_id`.

#### Retry-After

Every explicitly documented `429` and `503` response guarantees `Retry-After`
as integer seconds with a minimum of one. As with `X-Request-Id`, OpenAPI lists
and describes the header while live tests enforce presence.

The existing rate-limit paths already emit it for `429`. The sole current 503
contract, `limits_unavailable`, will return a five-second `Retry-After` header
and matching `error.details.retry_after_seconds: 5`. This makes the retry signal
available both to header-aware clients and clients that only deserialize the
error body.

The five-second value is a retry hint, not a promise that service is restored
within five seconds. Future 503-producing paths must supply their own positive
delay through the same error-envelope mechanism.

#### RateLimit headers

Operations governed by the request limiter will document the optional standard
headers already emitted by the server:

- `RateLimit-Limit`
- `RateLimit-Remaining`
- `RateLimit-Reset`

The legacy `X-RateLimit-*` family remains absent. A test will reject adding
those headers accidentally.

#### Central decoration

A post-registration OpenAPI normalizer, adjacent to the existing evolution-
stance normalizer, will decorate every OpenAPI operation response object
centrally:

- all `/v1` responses receive `X-Request-Id`;
- responses keyed `429` or `503` receive `Retry-After`;
- operations in the existing limiter operation sets receive the optional
  `RateLimit-*` headers.

The normalizer must not invent a status response that an operation does not
already declare. It only enriches existing response objects. Tests will fail
on missing component names, missing response maps, or dangling references.

### 3. Canonical error-code catalog

The existing emitted-code AST scan remains the enforcement mechanism, but its
catalog becomes structured metadata rather than a string-only list. Each entry
records:

- code;
- allowed HTTP status or statuses;
- family;
- retryability.

The catalog is the comparison source for tests, not a runtime lookup on the
request path. Existing handler-chosen status and code values remain explicit at
their call sites.

Tests will enforce four-way agreement:

1. every literal code emitted under `internal/` appears in the catalog;
2. every catalog code is emitted or explicitly marked fallback-only;
3. every catalog code appears in the OpenAPI `ErrorBody.code` description;
4. every catalog code appears in the `docs/api.md` table with the catalog's
   allowed status or statuses.

The documentation table will add the currently missing entries:

- `confirmation_required` — 400;
- `address_in_trash` — 409;
- `message_held` — 409;
- `not_in_trash` — 409;
- `send_in_progress` — 409.

The catalog remains an open set for clients: adding a new code is allowed, but
the same change must update every published surface. Renaming or removing a
code remains breaking.

### 4. Trashed-message direct reads

The existing runtime behavior is intentional and will be frozen:

- ordinary message lists exclude trashed messages;
- `deleted=true` lists only trashed messages;
- threads, reply targets, and forward targets exclude trashed messages;
- direct `GET /v1/agents/{email}/messages/{id}` may retrieve a trashed message
  and returns `deleted_at`;
- restore is available until permanent deletion or retention purge.

The `getMessage` operation description will state this explicitly. A focused
handler test will soft-delete a message and assert that ordinary list/reply
paths hide it while direct GET returns 200 with a non-null `deleted_at`.

This choice keeps recovery and audit workflows possible without adding a
second trash-detail endpoint.

### 5. Stability inventory

`x-stability: experimental` remains the machine-readable source on operations.
`docs/api.md` will publish an operation-ID table containing the exact current
experimental set and explain that those operations are excluded from the `/v1`
GA compatibility freeze.

The intended operation-ID inventory is:

- trash lifecycle: `deleteMessage`, `restoreMessage`, `restoreAgent`;
- protection configuration: `getAgentProtection`, `putAgentProtection`;
- templates: `createTemplate`, `deleteTemplate`, `getTemplate`,
  `listTemplates`, `updateTemplate`, `validateTemplate`;
- starter templates: `getStarterTemplate`, `listStarterTemplates`.

A drift test will parse the generated OpenAPI operation inventory and the
documentation table and require exact set equality. It will also continue to
assert operation-ID uniqueness. Adding, removing, or moving an experimental
marker therefore requires an explicit documentation change and contract
review.

Schemas reachable only from experimental operations continue inheriting the
experimental marker through the existing evolution-stance pass. Shared stable
schemas remain stable.

## Error handling and compatibility

The only wire-level behavior additions are:

- `location: ""` is now present where a validation detail previously omitted
  the property;
- `limits_unavailable` includes `Retry-After: 5` and structured retry details.

Both are intentional pre-GA tightenings. No existing status, code, or field is
removed. Response objects remain open to additive fields, request objects remain
closed, and response enums remain forward-compatible strings.

The header normalizer describes runtime behavior; it does not make generated
SDK model methods depend on headers. Raw HTTP users can rely on the headers,
while SDK error classes continue using the mirrored body details.

## Verification

The implementation must add or extend tests for:

1. Field-specific and request-wide validation errors, including JSON presence
   and non-nullability of both `location` and `message`.
2. `X-Request-Id` on live success and error responses, with body/header equality
   for errors, including the raw attachment-download route that is intentionally
   outside the OpenAPI operation inventory.
3. Live 429 responses with `Retry-After` and matching structured details.
4. Live 503 `limits_unavailable` with `Retry-After: 5` and matching details.
5. Standard `RateLimit-*` headers where applicable and absence of every
   `X-RateLimit-*` header.
6. Exact emitted-code/catalog/OpenAPI/docs vocabulary and status agreement.
7. Direct GET of a trashed message and exclusion from ordinary visibility and
   reply/forward paths.
8. Exact OpenAPI/docs experimental-operation inventory agreement.
9. OpenAPI golden freshness and generated TypeScript/Python SDK freshness.
10. TypeScript and Python tests/type checks for required `FieldError.location`.

Required gates:

- focused `internal/httpapi` tests;
- `make spec-check`;
- `make generate-sdk-check`;
- TypeScript SDK build, unit tests, and type tests;
- Python SDK tests and mypy;
- full repository CI before merge.

## Rollout and rollback

This is a pre-GA contract-only rollout with no migration or feature flag. If a
regression is found before GA, revert the PR and regenerate the spec/SDKs. After
GA, the required validation property, documented headers, error vocabulary,
trash read semantics, and stable/experimental inventory become compatibility
commitments and must not be weakened without a major API version.

## Acceptance criteria

- Every live `/v1` response carries the intended request ID; every response
  represented by OpenAPI references the documented header component.
- Every 429/503 response has a positive `Retry-After` value matching structured
  retry details where those details exist.
- `FieldError.location` and `message` are always present and non-null.
- Server emitters, the structured catalog, OpenAPI prose, and `docs/api.md`
  contain the same error codes and allowed statuses.
- Trashed-message direct-read behavior is explicit and regression-tested.
- The documented experimental operation IDs exactly equal OpenAPI markers.
- Spec and generated SDK checks are clean, with no unrelated API changes.

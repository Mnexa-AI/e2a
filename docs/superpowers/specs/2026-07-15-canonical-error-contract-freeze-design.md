# Canonical Error Contract Freeze

**Date:** 2026-07-15

**Status:** Approved

**Scope:** `/v1` REST errors and their TypeScript SDK, Python SDK, MCP, and CLI projections

## Goal

Freeze one dependable GA error contract: every `/v1` failure uses the same open envelope, every known code has machine-readable status/retry/detail metadata, typed detail payloads have one stable shape per code, and all official clients preserve unknown future codes without losing the known semantics.

## Audit Findings Addressed

1. Router-level `/v1` 404/405 responses bypass the canonical JSON envelope.
2. `invalid_request` sometimes omits details and sometimes carries a non-`ValidationErrorDetails` object.
3. `ErrorBody.details` uses an OpenAPI `anyOf` that TypeScript code generation collapses into a misleading `fields`-only model.
4. Runtime error responses always carry `request_id`, but OpenAPI and generated clients mark it optional.
5. CLI maps `blocked_by_policy` to the authentication exit code.
6. Python loses a body-only `request_id` and exposes raw non-envelope response text through its public error message.
7. Retry comments still describe `idempotency_key_reuse` as a 409 instead of the frozen 422.
8. `webhook_cooldown` documentation calls the code retryable even though automatic retry metadata and behavior say otherwise.

## Considered Approaches

### A. Closed discriminated unions for every error code

This gives strong generated types but makes unknown future codes fail parsing or produces brittle SDK unions. It conflicts with the additive `/v1` evolution policy. Rejected.

### B. Open generic envelope plus explicit stable-code metadata

Keep `error.code` a string and `error.details` an open object. Publish named detail schemas and machine-readable mappings as OpenAPI vendor extensions. Ergonomic clients branch on known codes and fall back to HTTP status for unknown codes. Chosen.

### C. Prose-only catalog with handwritten client maps

This preserves current behavior but allows server, docs, and client retry/classification tables to drift independently. Rejected.

## Contract Design

### Envelope

Every `/v1` non-2xx response, including router-level 404 and 405 responses, is:

```json
{
  "error": {
    "code": "machine_code",
    "message": "human-readable message",
    "request_id": "req_...",
    "details": {}
  }
}
```

`code`, `message`, and `request_id` are required and non-nullable. `details` remains optional because many codes need no structured context. The envelope, body, and details object remain open to additive fields.

The chi router owns canonical `/v1` `not_found` and `method_not_allowed` responses. Only non-`/v1` misses fall through to the legacy router.

### Machine-readable catalog

Move the current catalog out of the test file into production contract metadata. Each known code records:

- allowed HTTP statuses;
- family;
- whether an unchanged request is retryable;
- optional named details-schema reference.

OpenAPI publishes this as `x-e2a-error-contracts` on `ErrorBody.code`. Tests continue scanning server emitters, but now also compare OpenAPI, human docs, TypeScript classification, and Python classification with the same catalog.

The catalog remains an open set for consumers. The extension describes known stable codes; it is not a closed OpenAPI enum.

### Details schemas

`ErrorBody.details` becomes a plain open object, avoiding `oneOf`/`anyOf` code-generation traps. It carries `x-e2a-error-details-schemas`, mapping known codes to named schemas, analogous to `EventEnvelope.data.x-e2a-event-data-schemas`.

Stable mappings:

| Code | Details schema |
| --- | --- |
| `invalid_request` | `ValidationErrorDetails` |
| `too_many_recipients` | `TooManyRecipientsDetails` |
| `payload_too_large` | `PayloadTooLargeDetails` |
| `limit_exceeded` | `LimitExceededDetails` |
| `rate_limited` | `RateLimitedDetails` |
| `limits_unavailable` | `RetryAfterDetails` |

Template-specific detail schemas may be mapped but remain experimental with the template operations.

All manually constructed `invalid_request` responses use `ValidationErrorDetails`. Request-wide failures use one field entry with an empty or resource-level location; field-specific failures identify the request location.

`PayloadTooLargeDetails` normalizes the current composed/per-attachment/aggregate variants:

```json
{
  "scope": "composed_message | attachment | attachments_total | request_body",
  "actual_bytes": 10485761,
  "max_bytes": 10485760,
  "filename": "optional.bin"
}
```

### Retry semantics

- `429 rate_limited`, `409 idempotency_in_flight`, `503 limits_unavailable`, connection failures, and retry-safe 5xx requests retain existing retry behavior.
- `422 idempotency_key_reuse` is never retried.
- `webhook_cooldown` remains non-automatic-retry. Documentation will say to retry only after the cooldown has elapsed; it will not claim the SDK retries it.
- Transport retries remain gated by HTTP/idempotency safety, independently of an error object's informational `retryable` property.

### Client projections

- Generated SDKs expose generic `details` as an open value, not a false `fields`-only type.
- Ergonomic SDKs preserve unknown codes and unknown detail fields.
- Python mirrors TypeScript by reading `request_id` from the header first and the envelope second.
- Python uses a bounded synthesized message for malformed/non-envelope upstream responses; the raw exception remains available as `__cause__`.
- MCP continues emitting arbitrary codes and details in `structuredContent` without a whitelist.
- CLI exit 4 is reserved for `unauthorized` and scope `forbidden`; `blocked_by_policy` exits 5.

## Testing

1. Red/green wire tests for `/v1` 404 and 405 envelopes, content type, and request-id echo.
2. Catalog tests for emitter membership, status pairing, OpenAPI extension parity, docs parity, and retry metadata.
3. Golden fixtures for every stable typed details schema, validated against both generic `ErrorEnvelope` and the mapped details schema.
4. Regression tests proving every `invalid_request` detail is `ValidationErrorDetails`.
5. Spec assertions that all error body variants require `request_id`.
6. TypeScript generation/type tests proving generic future details remain accepted.
7. Python body-request-id and non-envelope-message tests.
8. CLI `blocked_by_policy` exit-code test.
9. Existing retry, MCP structured-error, SDK contract, spec freshness, and generated-code freshness suites.

## Implementation Slices

1. **Server and OpenAPI contract:** router fallbacks, required request IDs, shared catalog metadata, normalized detail schemas, fixtures, and docs.
2. **Official clients:** regenerate SDKs, align TS/Python error handling and tests, fix CLI policy exit classification, verify MCP forward compatibility, and correct stale retry prose.

Both slices land on one dedicated branch and one PR, with a coherent commit per slice.

## Non-goals

- Closing `error.code` into an enum.
- Automatically retrying every code marked informationally retryable.
- Changing OAuth-standard error bodies outside `/v1`.
- Adding new public error classes unless required to preserve current class behavior.
- Redesigning successful response schemas.

## Acceptance Criteria

- No `/v1` non-2xx path returns plain text or an empty body.
- The server, OpenAPI extension, `docs/api.md`, TS, and Python agree on every known code's status and retry semantics.
- Every stable code-specific details payload validates against its named schema.
- Unknown future codes/details parse without failure in both SDKs and MCP.
- CLI policy rejection exits 5; authentication/scope failures exit 4.
- All focused and repository freshness gates pass.

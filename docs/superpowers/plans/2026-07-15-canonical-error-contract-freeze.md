# Canonical Error Contract Freeze Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Freeze one forward-compatible `/v1` error contract across REST, OpenAPI, generated SDKs, MCP, and CLI before GA.

**Architecture:** Keep one open JSON envelope and one production-owned catalog of known codes. Decorate the Huma schema from that catalog, normalize the small set of typed detail payloads at their emitters, and let official clients preserve unknown codes/details while adding stable behavior for known codes.

**Tech Stack:** Go 1.25+, Chi, Huma v2, OpenAPI 3.1, TypeScript/Vitest, Python/Pydantic/Pytest, MCP, CLI.

---

### Task 1: Canonicalize router-level `/v1` failures

**Files:** `internal/httpapi/httpapi.go`, `internal/httpapi/router_errors_test.go`

- [ ] Add wire tests proving unknown `/v1` routes return `404 not_found` and wrong methods return `405 method_not_allowed`, with JSON content type and matching header/body request IDs, while non-`/v1` misses still delegate to `deps.Legacy`.
- [ ] Run `go test ./internal/httpapi -run 'TestV1RouterError|TestLegacyFallback' -count=1` and observe the current empty/plain-text failure.
- [ ] Add narrow Chi `NotFound` and `MethodNotAllowed` dispatchers that call `WriteError` for `/v1` and delegate only non-`/v1` requests to the legacy handler.
- [ ] Re-run the focused tests and commit the router slice.

### Task 2: Make the generic envelope and catalog canonical

**Files:** `internal/httpapi/errors.go`, `internal/httpapi/error_catalog.go`, `internal/httpapi/errorcode_vocab_test.go`, `internal/httpapi/errors_test.go`

- [ ] Add failing schema/JSON tests requiring `code`, `message`, and `request_id`, keeping `details` optional and open.
- [ ] Move `errorCodeContract` and `errorCodeCatalog` from the test into production code, add optional `DetailsSchema`, and keep test helpers consuming the production catalog.
- [ ] Remove `omitempty` from every public error-body `request_id` field.
- [ ] Replace `ErrorBody.details`' generated `anyOf` with a plain open object and attach `x-e2a-error-contracts` to `ErrorBody.code` plus `x-e2a-error-details-schemas` to `ErrorBody.details`.
- [ ] Add parity tests for catalog status/family/retryability/detail-schema metadata and run the focused vocabulary/schema suite.

### Task 3: Normalize stable error details and fixtures

**Files:** `internal/httpapi/errors.go`, `internal/httpapi/outbound.go`, `internal/httpapi/reviews.go`, related tests, `api/fixtures/errors/*.json`

- [ ] Add failing tests for `ValidationErrorDetails`, `TooManyRecipientsDetails`, `PayloadTooLargeDetails`, `LimitExceededDetails`, `RateLimitedDetails`, and `RetryAfterDetails`, including generic-envelope and mapped-schema validation of golden fixtures.
- [ ] Make `NewError(..., "invalid_request", ...)` supply one request-wide validation field when callers omit details; replace incompatible hand-built `invalid_request` objects.
- [ ] Introduce `TooManyRecipientsDetails` and normalized `PayloadTooLargeDetails{scope,actual_bytes,max_bytes,filename?}` and update every stable emitter.
- [ ] Re-run focused handler, fixture, and catalog tests.

### Task 4: Refresh OpenAPI and generated clients

**Files:** `api/openapi.yaml`, `sdks/typescript/src/v1/generated/`, `sdks/python/src/e2a/v1/generated/`, SDK type tests

- [ ] Regenerate the spec and both SDK bases using repository generators.
- [ ] Add TypeScript and Python type/model tests proving `request_id` is required and arbitrary future detail keys remain accepted.
- [ ] Run `make spec-check`, `make generate-sdk-check`, TypeScript type checks, and Python model tests.

### Task 5: Harden ergonomic TypeScript and Python errors

**Files:** `sdks/typescript/src/v1/errors.ts`, `sdks/typescript/src/v1/retry.ts`, tests; `sdks/python/src/e2a/v1/errors.py`, `sdks/python/src/e2a/v1/_retry.py`, tests

- [ ] Add regression tests for unknown codes/details, body-only Python `request_id`, bounded malformed-response messages, and non-retry of `422 idempotency_key_reuse`.
- [ ] Make Python prefer the request-id header then envelope body and synthesize a bounded public message for malformed responses while preserving the cause.
- [ ] Correct stale retry prose in both SDKs without changing the frozen retry algorithm.
- [ ] Run both SDK error/retry suites and static type checks.

### Task 6: Align CLI, MCP, and human documentation

**Files:** `cli/src/exit.ts`, `cli/src/bin/e2a.ts`, CLI tests; MCP structured-error tests; `docs/api.md`, `internal/httpapi/webhooks.go`

- [ ] Add a failing pure CLI exit-classification test showing `blocked_by_policy` must exit 5 while only `unauthorized` and scope `forbidden` exit 4.
- [ ] Centralize/fix exit classification and verify malformed/held/transient behavior remains unchanged.
- [ ] Add or extend MCP tests proving unknown codes and arbitrary details pass through `structuredContent` unchanged.
- [ ] Correct webhook cooldown wording and publish the catalog/detail mappings in `docs/api.md`.
- [ ] Run CLI and MCP focused suites.

### Task 7: Verify, review, and publish

- [ ] Run Go `internal/httpapi` tests, `make spec-check`, `make generate-sdk-check`, TS SDK build/tests/type tests, Python tests/mypy, MCP tests, and CLI tests.
- [ ] Review the complete diff against the approved design, fixing any design drift or missing negative-path tests.
- [ ] Commit coherent server/spec and client/docs slices on `codex/error-contract-freeze`, leave the branch ready for a PR, and report any untested gates explicitly.

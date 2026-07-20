# Inbound Email Ergonomics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task by task.

**Goal:** Add a typed, client-bound inbound email facade to the hand-written
TypeScript and Python SDK layers, guarded by shared cross-language conformance
vectors, without changing server behavior or wire shapes, using the canonical
inbound authentication contract already present on the base branch.

**Architecture:** A new `inbound` resource structurally validates a schema-v1
`email.received` envelope, hydrates its `MessageView`, and returns a safe,
value-oriented facade. The facade derives authentication and policy semantics
from MessageView, delegates operations to existing resources, and exposes
attachments lazily. Python has explicit async and sync facade boundaries.

## File map

- Create `sdks/testdata/inbound-email/*.json` — shared full, minimal,
  adversarial, and invalid conformance vectors.
- Create `sdks/typescript/src/v1/inbound.ts` and
  `sdks/typescript/test/v1/inbound.test.ts`.
- Modify TypeScript `client.ts`, `index.ts`, `package.json`, client runtime/type
  tests, README, and changelog.
- Create `sdks/python/src/e2a/v1/inbound.py` and
  `sdks/python/tests/test_v1_inbound.py`.
- Modify Python async/sync clients, public `__init__` exports, export/type tests,
  `pyproject.toml`, README, and changelog.
- Modify `README.md`, `docs/events.md`, canonical `plugins/e2a/docs/sdk.md`,
  and its generated mirror `web/public/sdk.md`.
- Do **not** stage ignored `sdks/typescript/dist/`.

## Task 1: Shared InboundEmail conformance vectors

**Files:**
- Create: `sdks/testdata/inbound-email/full.json`
- Create: `sdks/testdata/inbound-email/minimal.json`
- Create: `sdks/testdata/inbound-email/adversarial.json`
- Create: `sdks/testdata/inbound-email/invalid.json`

- [ ] Derive the full event envelope from
  `internal/eventpayload/testdata/email.received.json`, retaining non-empty
  authentication evidence and at least one attachment. Pair it with a complete wire-format
  MessageView response and a language-neutral expected projection.
- [ ] Add a minimal vector with required MessageView arrays, no `parsed` body,
  and no attachments. Expect empty text, absent HTML, and no truncation.
- [ ] Add an adversarial vector with SPF/DKIM failures, `flagged: true`, a
  truncated parsed body, divergent From/Reply-To/auth identity, and untrusted
  attachment filename/content type. Expect `verified: false` and explicit
  reply targets.
- [ ] Add invalid cases for wrong type/schema, non-object data, and missing,
  null, empty, or non-string `message_id`, `delivered_to`, or
  fetch-key values consumed by the facade.
- [ ] Validate the fixture JSON and commit it separately.

## Task 2: TypeScript facade, validation, and conformance gate

**Files:**
- Create: `sdks/typescript/src/v1/inbound.ts`
- Create: `sdks/typescript/test/v1/inbound.test.ts`
- Modify: `sdks/typescript/package.json`

- [ ] Write failing tests that load every shared vector. Stub the existing
  message operations and assert the facade's safe projection exactly equals
  the vector expectation.
- [ ] Assert all invalid vectors raise `E2AValidationError` with
  `invalid_email_received_event` before the stub transport is called.
- [ ] Define `EmailReceivedEvent` as the public schema-v1 envelope plus typed
  `EmailReceivedData`, but still perform runtime object and non-empty-string
  checks for both fetch keys. Do not rely only on `isEmailReceived`.
- [ ] Implement `InboundResource.fromEvent` and `InboundEmail`. Derive content,
  timestamp, authentication, recipients, flags, and attachments from the
  hydrated MessageView. Compute `verified` as aligned DMARC pass.
- [ ] Expose nullable `envelopeFrom`, nullable `authentication`, `replyTargets`, `textTruncated`, `flagged`,
  and `flagReason`; do not expose the misleading `authenticatedFrom` alias.
- [ ] Implement a safe `toJSON()` projection that omits `.message`, raw MIME,
  attachment URLs/data, and client internals.
- [ ] Run the focused Vitest file and confirm it passes.

## Task 3: TypeScript bound operations and public client shape

**Files:**
- Modify: `sdks/typescript/src/v1/inbound.ts`
- Modify: `sdks/typescript/src/v1/client.ts`
- Modify: `sdks/typescript/src/v1/index.ts`
- Modify: `sdks/typescript/test/v1/client.test.ts`
- Modify: `sdks/typescript/test/v1/client.types.ts`

- [ ] Add failing tests for reply, forward, and attachment `get`. Assert inbox,
  message ID, stable attachment index, inline option, and idempotency options
  pass through exactly. Default missing options to `{}` consistently in the
  implementation and tests.
- [ ] Assert reply/forward return the existing `SendResultView` unchanged,
  including `messageId` and `status: "pending_review"`.
- [ ] Implement `InboundAttachment` with `index`, `filename`, `contentType`,
  `size`, and `contentId`, sourced only from MessageView attachments. Its
  `get()` returns `AttachmentView`; it does not promise bytes or cache URLs.
- [ ] Add `readonly inbound` to `E2AClient`, initialized from the existing
  MessagesResource. Export the resource, facade, attachment, event, and safe
  projection types from `v1/index.ts` and the existing top-level entry point.
- [ ] Update the stale retirement comment in `v1/index.ts` so the new module is
  not confused with the removed v2 inbound helper.
- [ ] Add runtime and compile-time tests for every public property and method.
  Add the focused test file to the explicit package test list.
- [ ] Run focused tests and `npm run typecheck --workspace @e2a/sdk`.

## Task 4: Python async facade and conformance gate

**Files:**
- Create: `sdks/python/src/e2a/v1/inbound.py`
- Create: `sdks/python/tests/test_v1_inbound.py`
- Modify: `sdks/python/src/e2a/v1/client.py`

- [ ] Write failing async tests that load the same shared vectors and compare
  the same safe projection.
- [ ] Define a complete structural inbound-event protocol/union covering
  webhook and WebSocket envelopes (`id`, `type`, `schema_version`,
  `created_at`, `data`). Do not reuse the incomplete `EventLike` protocol or
  treat the existing TypeGuard as fetch-key validation.
- [ ] Validate mapping data and non-empty string fetch keys before transport;
  raise `E2AValidationError(code="invalid_email_received_event", status=0,
  retryable=False)` for every invalid vector.
- [ ] Implement `AsyncInboundResource`, `AsyncInboundEmail`, and
  `AsyncInboundAttachment`. Use generated MessageView `created_at`; do not
  parse an event `Z` timestamp with `datetime.fromisoformat`.
- [ ] Mirror TypeScript authentication, policy, reply-target, truncation,
  attachment, and safe-projection semantics exactly.
- [ ] Wire `AsyncE2AClient.inbound` immediately after the existing messages
  resource and run focused pytest coverage.

## Task 5: Python async operations and result semantics

**Files:**
- Modify: `sdks/python/src/e2a/v1/inbound.py`
- Modify: `sdks/python/tests/test_v1_inbound.py`

- [ ] Test async reply, forward, and attachment `get`, including bound fetch
  keys, idempotency key, stable index, and inline option.
- [ ] Assert operations return generated models unchanged. Use
  `result.message_id`, not nonexistent `result.id`, and preserve
  `status="pending_review"`.
- [ ] Implement direct delegates without duplicating validation, retry, or
  transport logic. Repeated attachment `get` calls remain independent.
- [ ] Test typed API errors and client-closed behavior propagate unchanged.

## Task 6: Python sync boundary and exports

**Files:**
- Modify: `sdks/python/src/e2a/v1/inbound.py`
- Modify: `sdks/python/src/e2a/v1/sync_client.py`
- Modify: `sdks/python/src/e2a/v1/__init__.py`
- Modify: `sdks/python/src/e2a/__init__.py`
- Modify: `sdks/python/tests/test_v1_sync_client.py`
- Modify: `sdks/python/tests/test_exports.py`
- Create: `sdks/python/tests/typecheck_inbound.py`
- Modify: `sdks/python/pyproject.toml`

- [ ] Write failing sync tests proving `client.inbound.from_event` returns
  `InboundEmail`, not `AsyncInboundEmail`, and no public operation returns a
  coroutine.
- [ ] Add explicit sync `InboundEmail` and `InboundAttachment` adapters using
  the existing `_EventLoopBridge`. Adapt the coroutine result at the inbound
  resource method boundary; `_wrap_value` does not see coroutine return values.
  Do not broaden generic wrapping behavior.
- [ ] Run the shared full/minimal/adversarial vectors through the sync facade
  and assert parity with async Python and TypeScript.
- [ ] Export `AsyncInboundResource`, `AsyncInboundEmail`,
  `AsyncInboundAttachment`, `InboundEmail`, and `InboundAttachment` from
  `e2a.v1` and the package top level. If sync classes live in `sync_client.py`,
  include them explicitly in that module's `__all__`; otherwise keep all
  facade classes in `inbound.py` and avoid circular imports.
- [ ] Add mypy-only usage proving async methods are awaitable and sync methods
  return generated models directly; add the file to mypy's explicit file list.
- [ ] Run sync tests, export tests, and mypy.

## Task 7: Documentation and changelogs

**Files:**
- Modify: `README.md`
- Modify: `sdks/typescript/README.md`
- Modify: `sdks/python/README.md`
- Modify: `docs/events.md`
- Modify: `plugins/e2a/docs/sdk.md`
- Modify: `web/public/sdk.md`
- Modify: `sdks/typescript/CHANGELOG.md`
- Modify: `sdks/python/CHANGELOG.md`

- [ ] Update all five guarded documentation consumers with equivalent TS and
  Python examples. Keep the lower-level `fetchMessage` / `fetch_message` path.
- [ ] Verify raw webhook input before calling the facade; state that the
  WebSocket stream is authenticated and hand-built objects are not trusted.
- [ ] Show `envelopeFrom`/`envelope_from`, `verified`, and `replyTargets` /
  `reply_targets`. State that From, Reply-To, bodies, filenames, and content
  types are untrusted and policy flags are not a comprehensive scan verdict.
- [ ] Capture reply results and branch on `status`, including
  `pending_review`; do not imply a 2xx response means delivery.
- [ ] Use `attachment.get`, explaining URL-vs-inline behavior and the size cap.
- [ ] Record the additive resource and cross-SDK conformance gate under both
  `Unreleased` sections. Do not bump package versions in this PR.

## Task 8: Verification, commits, and PR update

- [ ] TypeScript: run `npm test --workspace @e2a/sdk` and
  `npm run build --workspace @e2a/sdk`. The ignored build output may be
  inspected locally but must not be staged.
- [ ] Python: from `sdks/python`, run `pytest tests/ -v` and `mypy`.
- [ ] Run `node scripts/check-sdk-example-contracts.mjs` and
  `bash scripts/check-repository-text-integrity.sh`.
- [ ] Run `git diff --check origin/main...HEAD` and inspect the diff for scope.
  Existing OpenAPI compatibility, generated-code freshness, and live SDK
  contract jobs remain unchanged because this feature delegates to already
  covered HTTP resources.
- [ ] Commit in focused chunks using repository conventions, push
  `codex/inbound-email-ergonomics-design`, and update the existing draft PR.

## Final acceptance criteria

- Both languages consume the same facade conformance vectors and produce the
  same safe normalized projection.
- Invalid envelope/fetch-key shapes fail locally with typed validation errors
  and zero transport calls.
- Authentication identity and aligned DMARC verification are distinct.
- Reply targets, truncation, policy-flag limits, and untrusted content are
  explicit in APIs and docs.
- Reply/forward preserve SendResultView and `pending_review` semantics.
- Attachments use hydrated stable indices and `get()` accurately describes the
  metadata/URL/optional-inline response.
- Python async and sync clients expose genuinely async and blocking facades.
- No server behavior, wire-shape, package-version, or TypeScript `dist/`
  changes are committed; the OpenAPI/generated diff is description-only.
- Full SDK, type, documentation-integrity, and whitespace gates pass.

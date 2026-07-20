# Inbound Email Ergonomics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a typed, client-bound inbound email facade to the checked-in TypeScript and Python SDKs without changing the server or existing low-level SDK APIs.

**Architecture:** A new hand-written `inbound` resource validates a stable `email.received` event, hydrates its existing `MessageView`, and returns an ergonomic facade. Facades retain the original event and message, normalize common fields, delegate reply/forward to existing message resources, and fetch attachments lazily; Python adapts async facades at the existing sync-client bridge boundary.

**Tech Stack:** TypeScript, Vitest, OpenAPI-generated TypeScript models, Python 3.9+, Pydantic v2, pytest, mypy, existing e2a retry/error/sync-bridge layers.

---

## File structure

- Create `sdks/typescript/src/v1/inbound-email.ts` — TypeScript `InboundResource`, `InboundEmail`, and lazy `InboundAttachment`.
- Modify `sdks/typescript/src/v1/client.ts` — construct and expose `client.inbound`; export the existing message input/option types needed by the facade.
- Modify `sdks/typescript/src/v1/index.ts` — export the new public types.
- Modify `sdks/typescript/package.json` — include the new unit test in the explicit Vitest test list.
- Create `sdks/typescript/test/v1/inbound-email.test.ts` — runtime behavior and delegation tests.
- Modify `sdks/typescript/test/v1/client.types.ts` — compile-time public API tests.
- Rebuild `sdks/typescript/dist/` — committed package output.
- Create `sdks/python/src/e2a/v1/inbound_email.py` — async facade/resource plus sync adapters used by the bridge.
- Modify `sdks/python/src/e2a/v1/client.py` — construct and expose the async inbound resource.
- Modify `sdks/python/src/e2a/v1/sync_client.py` — convert returned async facades and attachments to blocking wrappers.
- Modify `sdks/python/src/e2a/v1/__init__.py` and `sdks/python/src/e2a/__init__.py` — stable exports.
- Create `sdks/python/tests/test_v1_inbound_email.py` — async hydration, normalization, delegation, validation, and lazy attachment tests.
- Modify `sdks/python/tests/test_v1_sync_client.py` — blocking facade parity and lifecycle tests.
- Modify `sdks/python/tests/test_exports.py` and add typing coverage under `sdks/python/tests/` — public export and method-shape checks.
- Modify `sdks/python/pyproject.toml` — include the new public-surface typecheck file in mypy's explicit file list.
- Modify `sdks/typescript/README.md`, `sdks/python/README.md`, and `README.md` — high-level inbound examples while retaining low-level examples.
- Modify both SDK changelogs — record the additive facade.

## Task 1: TypeScript facade behavior

**Files:**
- Create: `sdks/typescript/test/v1/inbound-email.test.ts`
- Create: `sdks/typescript/src/v1/inbound-email.ts`

- [ ] **Step 1: Write failing hydration and normalization tests**

Create a canonical `email.received` event and a stub message operations object. Assert that `InboundResource.fromEvent()` fetches by `delivered_to` plus `message_id`, preserves `event` and `message`, and normalizes every public field.

```ts
const event = {
  id: "evt_1",
  type: "email.received",
  schema_version: "1",
  created_at: "2026-07-20T08:00:00Z",
  data: {
    message_id: "msg_1",
    agent_email: "bot@agents.e2a.dev",
    direction: "inbound",
    conversation_id: "conv_1",
    from: "Alice <alice@example.com>",
    authenticated_from: "alice@example.com",
    to: ["bot@agents.e2a.dev"],
    cc: ["copy@example.com"],
    reply_to: ["reply@example.com"],
    delivered_to: "bot@agents.e2a.dev",
    subject: "Hello",
    auth_headers: {},
    received_at: "2026-07-20T08:00:00Z",
    attachments: [],
  },
};

const email = await inbound.fromEvent(event);
expect(get).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_1");
expect(email.id).toBe("msg_1");
expect(email.inbox).toBe("bot@agents.e2a.dev");
expect(email.authenticatedFrom).toBe("alice@example.com");
expect(email.text).toBe("message text");
expect(email.html).toBe("<p>message text</p>");
expect(email.receivedAt.toISOString()).toBe("2026-07-20T08:00:00.000Z");
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `npm exec --workspace @e2a/sdk vitest run test/v1/inbound-email.test.ts`

Expected: FAIL because `InboundResource` and `InboundEmail` do not exist.

- [ ] **Step 3: Implement the minimal TypeScript facade**

Define a narrow dependency interface so the new module composes the existing resource without exporting its concrete class:

```ts
export interface InboundMessageOperations {
  get(email: string, id: string): Promise<MessageView>;
  getAttachment(email: string, id: string, index: number, opts?: { inline?: boolean }): Promise<AttachmentView>;
  reply(email: string, id: string, body: ReplyInput, opts?: RequestOptions): Promise<SendResultView>;
  forward(email: string, id: string, body: ForwardInput, opts?: RequestOptions): Promise<SendResultView>;
}
```

Implement `InboundResource.fromEvent()` using `isEmailReceived`. On failure, throw `E2AValidationError` with `code: "invalid_email_received_event"`, `status: 0`, and `retryable: false`. Hydrate through `messages.get`, reject an invalid `received_at`, and build immutable normalized arrays.

```ts
async fromEvent(event: WebhookEvent): Promise<InboundEmail> {
  if (!isEmailReceived(event)) {
    throw invalidInboundEvent("expected a schema-v1 email.received event");
  }
  const message = await this.messages.get(event.data.delivered_to, event.data.message_id);
  return new InboundEmail(event, message, this.messages);
}
```

- [ ] **Step 4: Run the focused test and verify it passes**

Run: `npm exec --workspace @e2a/sdk vitest run test/v1/inbound-email.test.ts`

Expected: PASS.

- [ ] **Step 5: Add failing delegation and validation tests**

Cover:

```ts
await email.reply({ text: "Got it" }, { idempotencyKey: "reply:evt_1" });
expect(reply).toHaveBeenCalledWith(
  "bot@agents.e2a.dev",
  "msg_1",
  { text: "Got it" },
  { idempotencyKey: "reply:evt_1" },
);

await email.forward({ to: ["ops@example.com"], text: "FYI" });
expect(forward).toHaveBeenCalledWith(
  "bot@agents.e2a.dev",
  "msg_1",
  { to: ["ops@example.com"], text: "FYI" },
  {},
);

await email.attachments[0].fetch({ inline: true });
expect(getAttachment).toHaveBeenCalledWith(
  "bot@agents.e2a.dev",
  "msg_1",
  0,
  { inline: true },
);
```

Also assert wrong type, wrong schema version, missing fetch keys, and invalid `received_at` throw `E2AValidationError` before transport; absent optional arrays normalize to `[]`; absent parsed bodies yield `text === ""` and `html === undefined`.

- [ ] **Step 6: Complete delegation, attachment, and validation behavior**

Implement `reply`, `forward`, and `InboundAttachment.fetch` as direct delegates. Copy optional arrays when constructing the facade and use attachment metadata from the hydrated message so attachment indices match the REST source of truth.

- [ ] **Step 7: Run TypeScript facade tests**

Run: `npm exec --workspace @e2a/sdk vitest run test/v1/inbound-email.test.ts`

Expected: all focused tests PASS.

- [ ] **Step 8: Commit the TypeScript facade**

```bash
git add sdks/typescript/src/v1/inbound-email.ts sdks/typescript/test/v1/inbound-email.test.ts
git commit -m "feat(sdk-ts): add inbound email facade"
```

## Task 2: TypeScript client wiring and public types

**Files:**
- Modify: `sdks/typescript/src/v1/client.ts`
- Modify: `sdks/typescript/src/v1/index.ts`
- Modify: `sdks/typescript/package.json`
- Modify: `sdks/typescript/test/v1/client.types.ts`
- Modify: `sdks/typescript/test/v1/client.test.ts`

- [ ] **Step 1: Write failing client wiring and type tests**

Add runtime and compile-time assertions:

```ts
const client = new E2AClient({ apiKey: "e2a_test" });
expect(client.inbound).toBeDefined();

const emailPromise: Promise<InboundEmail> = client.inbound.fromEvent(receivedEvent);
emailPromise.then((email) => {
  const id: string = email.id;
  const sender: string = email.authenticatedFrom;
  const reply: Promise<SendResultView> = email.reply({ text: "ok" });
  void id; void sender; void reply;
});
```

- [ ] **Step 2: Run tests and verify the missing surface fails**

Run: `npm exec --workspace @e2a/sdk vitest run test/v1/client.test.ts`

Expected: FAIL because `E2AClient.inbound` is absent.

- [ ] **Step 3: Wire and export the resource**

Add `readonly inbound: InboundResource` to `E2AClient`, construct it with the existing `MessagesResource`, and export `InboundResource`, `InboundEmail`, `InboundAttachment`, and supporting public types from `v1/index.ts`. Keep `MessagesResource` private.

```ts
this.messages = new MessagesResource(new PromiseMessagesApi(config));
this.inbound = new InboundResource(this.messages);
```

Append `test/v1/inbound-email.test.ts` to the explicit `test:unit` Vitest file list in `sdks/typescript/package.json`, ensuring the normal `npm test --workspace @e2a/sdk` gate exercises the facade.

- [ ] **Step 4: Run client and type tests**

Run: `npm exec --workspace @e2a/sdk vitest run test/v1/client.test.ts`

Run: `npm run typecheck --workspace @e2a/sdk`

Expected: PASS with no TypeScript errors.

- [ ] **Step 5: Commit TypeScript wiring**

```bash
git add sdks/typescript/package.json sdks/typescript/src/v1/client.ts sdks/typescript/src/v1/index.ts sdks/typescript/test/v1/client.test.ts sdks/typescript/test/v1/client.types.ts
git commit -m "feat(sdk-ts): expose inbound resource"
```

## Task 3: Python async facade behavior

**Files:**
- Create: `sdks/python/src/e2a/v1/inbound_email.py`
- Create: `sdks/python/tests/test_v1_inbound_email.py`
- Modify: `sdks/python/src/e2a/v1/client.py`

- [ ] **Step 1: Write failing async hydration tests**

Use `httpx_mock` and `AsyncE2AClient` to exercise the real resource chain:

```py
async with AsyncE2AClient(api_key="e2a_test", base_url=BASE) as client:
    email = await client.inbound.from_event(event)

assert email.id == "msg_1"
assert email.inbox == "bot@agents.e2a.dev"
assert email.from_ == "Alice <alice@example.com>"
assert email.authenticated_from == "alice@example.com"
assert email.text == "message text"
assert email.html == "<p>message text</p>"
assert email.received_at.isoformat() == "2026-07-20T08:00:00+00:00"
assert email.event is event
assert email.message.id == "msg_1"
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run from `sdks/python`: `pytest tests/test_v1_inbound_email.py -v`

Expected: FAIL because `client.inbound` does not exist.

- [ ] **Step 3: Implement `AsyncInboundResource` and `AsyncInboundEmail`**

Create frozen/value-oriented facade classes that retain the existing async message resource privately. Validate with `is_email_received`, construct `E2AValidationError` directly for local invalid events, hydrate with `messages.get`, and parse `received_at` with the standard library into an aware `datetime`.

```py
async def from_event(self, event: EventLike) -> AsyncInboundEmail:
    if not is_email_received(event):
        raise E2AValidationError(
            code="invalid_email_received_event",
            message="expected a schema-v1 email.received event",
            status=0,
            retryable=False,
        )
    message = await self._messages.get(
        event.data["delivered_to"], event.data["message_id"]
    )
    return AsyncInboundEmail(event, message, self._messages)
```

Initialize `self.inbound = AsyncInboundResource(self.messages)` immediately after `self.messages` in `AsyncE2AClient.__init__`.

- [ ] **Step 4: Run the hydration tests and verify they pass**

Run from `sdks/python`: `pytest tests/test_v1_inbound_email.py -v`

Expected: hydration tests PASS.

- [ ] **Step 5: Add failing async behavior tests**

Verify:

```py
result = await email.reply(
    {"text": "Got it"}, idempotency_key="reply:evt_1"
)
assert result.id == "msg_reply"

await email.forward({"to": ["ops@example.com"], "text": "FYI"})
attachment = await email.attachments[0].fetch(inline=True)
assert attachment.index == 0
```

Assert the HTTP requests use the bound inbox/message ID and idempotency header. Add local rejection tests for wrong type, wrong schema, missing keys, and invalid timestamps, plus absent optional-array/body normalization.

- [ ] **Step 6: Implement async reply, forward, and lazy attachments**

Delegate without duplicating validation, retry, or transport logic:

```py
async def reply(self, body: Body, *, idempotency_key: Optional[str] = None) -> SendResultView:
    return await self._messages.reply(
        self.inbox, self.id, body, idempotency_key=idempotency_key
    )
```

Implement `forward` and `AsyncInboundAttachment.fetch` analogously.

- [ ] **Step 7: Run the complete async facade tests**

Run from `sdks/python`: `pytest tests/test_v1_inbound_email.py -v`

Expected: PASS.

- [ ] **Step 8: Commit the Python async facade**

```bash
git add sdks/python/src/e2a/v1/inbound_email.py sdks/python/src/e2a/v1/client.py sdks/python/tests/test_v1_inbound_email.py
git commit -m "feat(sdk-py): add async inbound email facade"
```

## Task 4: Python sync facade parity

**Files:**
- Modify: `sdks/python/src/e2a/v1/inbound_email.py`
- Modify: `sdks/python/src/e2a/v1/sync_client.py`
- Modify: `sdks/python/tests/test_v1_sync_client.py`

- [ ] **Step 1: Write failing sync parity tests**

Assert the sync client returns a blocking facade rather than leaking coroutine methods:

```py
with E2AClient(api_key="e2a_test", base_url=BASE) as client:
    email = client.inbound.from_event(event)
    assert isinstance(email, InboundEmail)
    assert not inspect.iscoroutinefunction(email.reply)
    reply = email.reply({"text": "Got it"}, idempotency_key="reply:evt_1")
    assert reply.id == "msg_reply"
    attachment = email.attachments[0].fetch(inline=True)
    assert attachment.index == 0
```

- [ ] **Step 2: Run the sync test and verify it fails**

Run from `sdks/python`: `pytest tests/test_v1_sync_client.py -v -k inbound`

Expected: FAIL because the async facade is returned unchanged or the sync facade is absent.

- [ ] **Step 3: Implement explicit sync adapters**

Add `InboundEmail` and `InboundAttachment` wrappers that expose the same value properties and use `_EventLoopBridge.submit()` for async operations. Extend `_wrap_value` with explicit checks before generic resource detection:

```py
if isinstance(value, AsyncInboundEmail):
    return InboundEmail(value, bridge)
if isinstance(value, AsyncInboundAttachment):
    return InboundAttachment(value, bridge)
```

Ensure `InboundEmail.attachments` converts every async attachment with the same bridge. Do not broaden generic wrapping behavior for unrelated SDK objects.

- [ ] **Step 4: Run sync parity tests**

Run from `sdks/python`: `pytest tests/test_v1_sync_client.py -v -k inbound`

Expected: PASS and no coroutine warnings.

- [ ] **Step 5: Run all sync-client tests**

Run from `sdks/python`: `pytest tests/test_v1_sync_client.py -v`

Expected: PASS.

- [ ] **Step 6: Commit sync parity**

```bash
git add sdks/python/src/e2a/v1/inbound_email.py sdks/python/src/e2a/v1/sync_client.py sdks/python/tests/test_v1_sync_client.py
git commit -m "feat(sdk-py): bridge inbound email to sync client"
```

## Task 5: Stable exports and package typing

**Files:**
- Modify: `sdks/python/src/e2a/v1/__init__.py`
- Modify: `sdks/python/src/e2a/__init__.py`
- Modify: `sdks/python/tests/test_exports.py`
- Create: `sdks/python/tests/typecheck_inbound_email.py`
- Modify: `sdks/python/pyproject.toml`

- [ ] **Step 1: Write failing export and typing tests**

Require these imports from both `e2a` and `e2a.v1`:

```py
from e2a.v1 import (
    AsyncInboundAttachment,
    AsyncInboundEmail,
    InboundAttachment,
    InboundEmail,
)
```

Add mypy-only usage proving async methods are awaitable and sync methods return generated response models directly. Add `tests/typecheck_inbound_email.py` to `[tool.mypy].files` in `pyproject.toml`; mypy only checks the explicitly listed files.

- [ ] **Step 2: Run tests and verify exports fail**

Run from `sdks/python`: `pytest tests/test_exports.py -v`

Expected: FAIL with missing inbound facade exports.

- [ ] **Step 3: Export all four facade types**

Import the async types from `inbound_email.py`, the sync types from
`sync_client.py` or the focused inbound module (according to the Task 4
implementation boundary), and list each name in both `__all__` values.

- [ ] **Step 4: Run export tests and mypy**

Run from `sdks/python`: `pytest tests/test_exports.py -v`

Run from `sdks/python`: `mypy`

Expected: PASS.

- [ ] **Step 5: Commit Python public exports**

```bash
git add sdks/python/pyproject.toml sdks/python/src/e2a/v1/__init__.py sdks/python/src/e2a/__init__.py sdks/python/tests/test_exports.py sdks/python/tests/typecheck_inbound_email.py
git commit -m "feat(sdk-py): export inbound email types"
```

## Task 6: Documentation, committed TypeScript output, and full verification

**Files:**
- Modify: `README.md`
- Modify: `sdks/typescript/README.md`
- Modify: `sdks/typescript/CHANGELOG.md`
- Modify: `sdks/python/README.md`
- Modify: `sdks/python/CHANGELOG.md`
- Modify: `sdks/typescript/dist/**`

- [ ] **Step 1: Update SDK documentation**

Add the same canonical flow in both languages while retaining the lower-level `fetchMessage` / `fetch_message` documentation:

```ts
const event = constructEvent(rawBody, signature, secret);
if (isEmailReceived(event)) {
  const email = await client.inbound.fromEvent(event);
  console.log(email.authenticatedFrom, email.subject, email.text);
  await email.reply({ text: "Got it!" });
}
```

```py
event = construct_event(raw_body, signature, secret)
if is_email_received(event):
    email = await client.inbound.from_event(event)
    print(email.authenticated_from, email.subject, email.text)
    await email.reply({"text": "Got it!"})
```

Document that callers must verify raw webhook input first, WebSocket events can use the same hydration method, `from_`/`from` is not the authenticated identity, and attachments are lazy.

- [ ] **Step 2: Update both changelogs**

Record the additive `inbound` resource, normalized facade fields, bound operations, lazy attachments, and Python sync/async parity under the current unreleased section.

- [ ] **Step 3: Run the full TypeScript SDK suite**

Run: `npm test --workspace @e2a/sdk`

Expected: typecheck, Vitest, and type tests all PASS.

- [ ] **Step 4: Build and inspect committed TypeScript output**

Run: `npm run build --workspace @e2a/sdk`

Run: `git diff --check -- sdks/typescript/dist`

Expected: build PASS; `dist/` includes the new facade and updated client/index output with no whitespace errors.

- [ ] **Step 5: Run the full Python SDK suite**

Run from `sdks/python`: `pytest tests/ -v`

Run from `sdks/python`: `mypy`

Expected: all tests and mypy PASS.

- [ ] **Step 6: Run repository-level targeted guards**

Run: `node scripts/check-sdk-version-sync.mjs`

Run: `git diff --check origin/main...HEAD`

Expected: both commands PASS. Confirm `git diff --name-only origin/main...HEAD` contains only the design/plan docs, SDK sources, tests, READMEs/changelogs, and TypeScript `dist/` output described above.

- [ ] **Step 7: Commit docs and generated output**

```bash
git add README.md sdks/typescript/README.md sdks/typescript/CHANGELOG.md sdks/typescript/dist sdks/python/README.md sdks/python/CHANGELOG.md
git commit -m "docs(sdk): document inbound email facade"
```

- [ ] **Step 8: Push the implementation branch**

Run: `git push origin codex/inbound-email-ergonomics-design`

Expected: the existing draft PR updates with the implementation commits.

## Final acceptance criteria

- Existing `constructEvent` / `construct_event`, guards, `fetchMessage` / `fetch_message`, and generated message models remain compatible.
- TypeScript exposes `client.inbound.fromEvent()` returning `InboundEmail`.
- Python async exposes `client.inbound.from_event()` returning `AsyncInboundEmail`.
- Python sync exposes the same call returning a blocking `InboundEmail`.
- Common fields are normalized consistently while `event` and `message` remain available.
- `authenticatedFrom` / `authenticated_from` is distinct from display `from` / `from_`.
- Reply, forward, idempotency options, and lazy attachment fetch delegate to existing resources.
- Invalid events fail locally with typed validation errors before transport.
- TypeScript tests/build and Python pytest/mypy pass.
- The draft PR contains the implementation, tests, docs, and committed TypeScript build output, with no unrelated worktree changes.

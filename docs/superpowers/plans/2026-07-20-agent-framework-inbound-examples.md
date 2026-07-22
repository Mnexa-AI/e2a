# Agent Framework Inbound Examples Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build runnable Python and TypeScript signed-webhook examples for OpenAI Agents SDK, Anthropic, LangChain, and Google ADK using the ergonomic e2a inbound facade and no-key deterministic dry runs.

**Architecture:** Each language has one transport host that verifies the webhook, hydrates `InboundEmail`, deduplicates the event, and performs the bound reply. Four thin adapters implement a shared `ReplyAgent` contract; production factories call official framework SDKs while tests inject deterministic runner functions.

**Tech Stack:** e2a SDK 5.2, FastAPI, pytest, mypy, Node.js ESM, TypeScript, Vitest, Express, OpenAI Agents SDK, Anthropic SDK, LangChain 1.x, Google ADK.

---

## File map

Python:

- `examples/agent-framework-webhooks/python/agent_webhooks/contracts.py` — `ReplyAgent` and inbound-resource protocols.
- `examples/agent-framework-webhooks/python/agent_webhooks/prompt.py` — safe normalized email-to-prompt projection.
- `examples/agent-framework-webhooks/python/agent_webhooks/delivery_state.py` — bounded in-memory event claims.
- `examples/agent-framework-webhooks/python/agent_webhooks/handler.py` — verified delivery, facade hydration, agent invocation, and bound reply.
- `examples/agent-framework-webhooks/python/agent_webhooks/app.py` — FastAPI wiring and framework selection.
- `examples/agent-framework-webhooks/python/agent_webhooks/adapters/*.py` — four official SDK adapters plus deterministic fake.
- `examples/agent-framework-webhooks/python/agent_webhooks/dry_run.py` — signed no-key lifecycle exercise.
- `examples/agent-framework-webhooks/python/tests/` — unit, adapter, and dry-run tests.

TypeScript:

- `examples/agent-framework-webhooks/typescript/src/contracts.ts` — `ReplyAgent` and inbound-resource interfaces.
- `examples/agent-framework-webhooks/typescript/src/prompt.ts` — safe normalized email-to-prompt projection.
- `examples/agent-framework-webhooks/typescript/src/delivery-state.ts` — bounded in-memory event claims.
- `examples/agent-framework-webhooks/typescript/src/handler.ts` — verified delivery, facade hydration, agent invocation, and bound reply.
- `examples/agent-framework-webhooks/typescript/src/app.ts` — Express wiring and framework selection.
- `examples/agent-framework-webhooks/typescript/src/adapters/*.ts` — four official SDK adapters plus deterministic fake.
- `examples/agent-framework-webhooks/typescript/src/dry-run.ts` — signed no-key lifecycle exercise.
- `examples/agent-framework-webhooks/typescript/test/` — unit, adapter, and dry-run tests.

Documentation and compatibility:

- `examples/agent-framework-webhooks/README.md` — canonical matrix tutorial.
- `examples/adk-cloud-webhook/*` — executable expanded ADK tutorial migrated to the same facade contract.
- `README.md`, `sdks/python/README.md`, `sdks/typescript/README.md` — links to canonical examples.
- `scripts/check-sdk-example-contracts.mjs`, `scripts/check-repository-text-integrity.sh` — regression guards.

### Task 1: Python safe prompt and delivery state

**Files:**
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/__init__.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/contracts.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/prompt.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/delivery_state.py`
- Create: `examples/agent-framework-webhooks/python/tests/test_prompt.py`
- Create: `examples/agent-framework-webhooks/python/tests/test_delivery_state.py`

- [ ] **Step 1: Write failing prompt and claim tests**

Use a small `SimpleNamespace` email fixture and assert the exact prompt plus
`new -> processing -> processed` and failure-release transitions:

```python
def test_email_prompt_uses_normalized_fields_only() -> None:
    email = SimpleNamespace(
        from_="Ada <ada@example.com>", subject="Question", text="Can you help?",
        verified=True, flagged=False,
        message=SimpleNamespace(raw_message="SECRET RAW MIME"),
    )
    prompt = email_prompt(email)
    assert prompt == (
        "From: Ada <ada@example.com>\nSubject: Question\n"
        "Sender DMARC verified: yes\nPolicy flagged: no\n\nCan you help?"
    )
    assert "SECRET RAW MIME" not in prompt

async def test_claim_release_and_completion() -> None:
    state = EventDeduper(max_processed=1)
    assert await state.claim("evt_1") == "new"
    assert await state.claim("evt_1") == "processing"
    await state.release("evt_1")
    assert await state.claim("evt_1") == "new"
    await state.complete("evt_1")
    assert await state.claim("evt_1") == "processed"
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd examples/agent-framework-webhooks/python && pytest tests/test_prompt.py tests/test_delivery_state.py -q`

Expected: collection fails because `agent_webhooks.prompt` and
`agent_webhooks.delivery_state` do not exist.

- [ ] **Step 3: Implement the minimal contracts, prompt, and deduper**

Define `ReplyAgent.reply(email: AsyncInboundEmail) -> str`, an
`AsyncInboundResource.from_event(...)` protocol, `email_prompt()` using only
the five normalized fields shown above, and the lock-protected bounded sets:

```python
class ReplyAgent(Protocol):
    async def reply(self, email: AsyncInboundEmail) -> str: ...

def email_prompt(email: AsyncInboundEmail) -> str:
    sender = email.from_ or "(missing)"
    return (
        f"From: {sender}\nSubject: {email.subject}\n"
        f"Sender DMARC verified: {'yes' if email.verified else 'no'}\n"
        f"Policy flagged: {'yes' if email.flagged else 'no'}\n\n{email.text}"
    )
```

Port the existing `EventDeduper` implementation from
`examples/adk-cloud-webhook/delivery_state.py`, retaining its positive-capacity
validation and bounded processed queue.

- [ ] **Step 4: Run tests and verify GREEN**

Run: `cd examples/agent-framework-webhooks/python && pytest tests/test_prompt.py tests/test_delivery_state.py -q`

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add examples/agent-framework-webhooks/python
git commit -m "feat(examples): add Python inbound delivery primitives"
```

### Task 2: Python verified inbound handler

**Files:**
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/handler.py`
- Create: `examples/agent-framework-webhooks/python/tests/test_handler.py`

- [ ] **Step 1: Write failing lifecycle tests**

Build a real signed `email.received` JSON body, fake only the inbound resource,
agent, and bound email, then assert:

```python
result = await handle_delivery(body, signature, "whsec_test", inbound, agent, state)
assert result == {"status": "replied", "conversation_id": "conv_1"}
inbound.from_event.assert_awaited_once()
agent.reply.assert_awaited_once_with(email)
email.reply.assert_awaited_once_with(
    {"text": "Thanks", "conversation_id": "conv_1"},
    idempotency_key="evt_1",
)
```

Add separate tests proving a bad signature makes zero downstream calls, a
duplicate makes one turn/reply, whitespace-only output returns `no_reply`, and
an agent or reply exception releases the claim so the same event can retry.

- [ ] **Step 2: Run the handler test and verify RED**

Run: `cd examples/agent-framework-webhooks/python && pytest tests/test_handler.py -q`

Expected: collection fails because `handle_delivery` does not exist.

- [ ] **Step 3: Implement `handle_delivery`**

Use the shipped verification and facade APIs directly:

```python
async def handle_delivery(body, signature, secret, inbound, agent, deduper):
    event = construct_event(body, signature, secret)
    if event.type != "email.received":
        return {"status": "ignored", "type": event.type}
    claim = await deduper.claim(event.id)
    if claim == "processed":
        return {"status": "duplicate", "event_id": event.id}
    if claim == "processing":
        raise DeliveryInProgress(event.id)
    try:
        email = await inbound.from_event(event)
        reply_text = (await agent.reply(email)).strip()
        if not reply_text:
            await deduper.complete(event.id)
            return {"status": "no_reply", "conversation_id": email.conversation_id}
        sent = await email.reply(
            {"text": reply_text, "conversation_id": email.conversation_id},
            idempotency_key=event.id,
        )
        await deduper.complete(event.id)
        status = "replied" if sent.status in {"sent", "accepted"} else sent.status
        return {"status": status, "conversation_id": email.conversation_id}
    except BaseException:
        await deduper.release(event.id)
        raise
```

- [ ] **Step 4: Run Python primitive and handler tests**

Run: `cd examples/agent-framework-webhooks/python && pytest tests/test_prompt.py tests/test_delivery_state.py tests/test_handler.py -q`

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add examples/agent-framework-webhooks/python
git commit -m "feat(examples): handle verified Python inbound email"
```

### Task 3: Python framework adapters

**Files:**
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/adapters/__init__.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/adapters/openai.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/adapters/anthropic.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/adapters/langchain.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/adapters/adk.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/adapters/fake.py`
- Create: `examples/agent-framework-webhooks/python/tests/test_adapters.py`

- [ ] **Step 1: Write failing result-extraction tests for all five adapters**

Inject async callables returning the official result shapes:

```python
assert await OpenAIReplyAgent(run=lambda _: awaitable(SimpleNamespace(final_output="OpenAI"))).reply(email) == "OpenAI"
assert await AnthropicReplyAgent(run=lambda _: awaitable(SimpleNamespace(content=[SimpleNamespace(type="text", text="Claude")]))).reply(email) == "Claude"
assert await LangChainReplyAgent(run=lambda _: awaitable({"messages": [SimpleNamespace(type="ai", content="LangChain")]})).reply(email) == "LangChain"
assert await ADKReplyAgent(run=lambda _: async_events(final_event("ADK"))).reply(email) == "ADK"
assert await FakeReplyAgent("Fake").reply(email) == "Fake"
```

Also test that Anthropic joins multiple text blocks, LangChain rejects a missing
final AI message, and ADK ignores non-final events.

- [ ] **Step 2: Run adapter tests and verify RED**

Run: `cd examples/agent-framework-webhooks/python && pytest tests/test_adapters.py -q`

Expected: collection fails because the adapter modules do not exist.

- [ ] **Step 3: Implement the adapters and official factories**

Each class calls `email_prompt(email)` and delegates to an injected callable.
Factories must use these current official entry points:

```python
# OpenAI Agents SDK
agent = Agent(name="Email assistant", instructions=INSTRUCTIONS,
              model=os.getenv("OPENAI_MODEL", "gpt-5.6"))
return OpenAIReplyAgent(lambda prompt: Runner.run(agent, prompt))

# Anthropic SDK
client = AsyncAnthropic()
return AnthropicReplyAgent(lambda prompt: client.messages.create(
    model=os.getenv("ANTHROPIC_MODEL", "claude-opus-4-8"), max_tokens=1024,
    system=INSTRUCTIONS, messages=[{"role": "user", "content": prompt}],
))

# LangChain 1.x
agent = create_agent(model=os.getenv("LANGCHAIN_MODEL", "openai:gpt-5.5"),
                     tools=[], system_prompt=INSTRUCTIONS)
return LangChainReplyAgent(lambda prompt: agent.ainvoke(
    {"messages": [{"role": "user", "content": prompt}]}
))

# Google ADK
agent = LlmAgent(name="email_assistant", model=os.getenv("ADK_MODEL", "gemini-flash-latest"),
                 instruction=INSTRUCTIONS)
sessions = InMemorySessionService()
runner = Runner(agent=agent, app_name=APP_NAME, session_service=sessions)
```

For ADK, `reply()` calls `get_session`/`create_session` using
`email.conversation_id` and a stable sender-derived user ID, streams
`runner.run_async(...)`, and returns the last final text event.

- [ ] **Step 4: Run adapter tests and verify GREEN**

Run: `cd examples/agent-framework-webhooks/python && pytest tests/test_adapters.py -q`

Expected: all adapter extraction tests pass without provider keys.

- [ ] **Step 5: Commit**

```bash
git add examples/agent-framework-webhooks/python
git commit -m "feat(examples): add Python agent framework adapters"
```

### Task 4: Python app, package, and no-key dry run

**Files:**
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/app.py`
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/dry_run.py`
- Create: `examples/agent-framework-webhooks/python/tests/test_app.py`
- Create: `examples/agent-framework-webhooks/python/tests/test_dry_run.py`
- Create: `examples/agent-framework-webhooks/python/pyproject.toml`
- Create: `examples/agent-framework-webhooks/python/.env.example`

- [ ] **Step 1: Write failing FastAPI and dry-run tests**

Assert `/health`, 401 for an invalid signature, 503 for
`DeliveryInProgress`, and one successful fake signed POST. Assert
`dry_run.main()` prints `status=replied` and one captured reply.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd examples/agent-framework-webhooks/python && pytest tests/test_app.py tests/test_dry_run.py -q`

Expected: collection fails because `app` and `dry_run` do not exist.

- [ ] **Step 3: Implement app selection and dry run**

`AGENT_FRAMEWORK` accepts exactly `openai`, `anthropic`, `langchain`, `adk`, or
`fake`. `create_app()` creates one `AsyncE2AClient`, one adapter, and one
`EventDeduper` in lifespan state, maps `E2AWebhookSignatureError` to 401 and
`DeliveryInProgress` to 503, and closes the client.

The dry run must sign `timestamp + b"." + body` with HMAC-SHA256, use a real
`AsyncInboundResource` over fake message operations, and call
`handle_delivery()` twice to prove the second call is a duplicate.

- [ ] **Step 4: Add exact package configuration**

Declare Python `>=3.10`, `e2a>=5.2,<6`, `fastapi`, `uvicorn`, `python-dotenv`,
`openai-agents`, `anthropic`, `langchain>=1,<2`, `langchain-openai`, and
`google-adk`; add dev dependencies `pytest`, `pytest-asyncio`, `httpx`, and
`mypy`. Add scripts/documented commands for uvicorn, pytest, mypy, and
`python -m agent_webhooks.dry_run`.

- [ ] **Step 5: Install and verify Python suite**

Run:

```bash
cd examples/agent-framework-webhooks/python
python3.12 -m venv .venv
.venv/bin/pip install -e ../../../sdks/python -e '.[dev]'
.venv/bin/pytest -q
.venv/bin/mypy agent_webhooks
.venv/bin/python -m agent_webhooks.dry_run
```

Expected: tests and mypy pass; dry run prints one reply and one duplicate.

- [ ] **Step 6: Commit**

```bash
git add examples/agent-framework-webhooks/python
git commit -m "feat(examples): ship runnable Python framework webhooks"
```

### Task 5: TypeScript safe prompt, delivery state, and handler

**Files:**
- Create: `examples/agent-framework-webhooks/typescript/src/contracts.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/prompt.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/delivery-state.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/handler.ts`
- Create: `examples/agent-framework-webhooks/typescript/test/handler.test.ts`

- [ ] **Step 1: Write failing TypeScript lifecycle tests**

Mirror the Python assertions using a signed body, a real `constructEvent`, a
fake inbound resource returning a bound-email-shaped object, and spies:

```ts
expect(await handleDelivery({ body, signature, secret, inbound, agent, deduper }))
  .toEqual({ status: "replied", conversationId: "conv_1" });
expect(inbound.fromEvent).toHaveBeenCalledOnce();
expect(agent.reply).toHaveBeenCalledWith(email);
expect(email.reply).toHaveBeenCalledWith(
  { text: "Thanks", conversationId: "conv_1" },
  { idempotencyKey: "evt_1" },
);
```

Add bad-signature, duplicate, processing, empty-output, and released-on-error
cases, plus the exact safe prompt test excluding `message.rawMessage`.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd examples/agent-framework-webhooks/typescript && npm test -- --run`

Expected: TypeScript/Vitest fails because the source modules do not exist.

- [ ] **Step 3: Implement contracts, prompt, deduper, and handler**

Use `constructEvent()` and `InboundResource.fromEvent()` through structural
interfaces. The handler follows the same state machine as Python and performs:

```ts
const email = await inbound.fromEvent(event);
const replyText = (await agent.reply(email)).trim();
const sent = await email.reply(
  { text: replyText, conversationId: email.conversationId },
  { idempotencyKey: event.id },
);
```

Map `sent`/`accepted` to `replied`, preserve other send statuses, and release
the event claim on any thrown error.

- [ ] **Step 4: Run tests and verify GREEN**

Run: `cd examples/agent-framework-webhooks/typescript && npm test -- --run`

Expected: lifecycle tests pass.

- [ ] **Step 5: Commit**

```bash
git add examples/agent-framework-webhooks/typescript
git commit -m "feat(examples): handle verified TypeScript inbound email"
```

### Task 6: TypeScript framework adapters

**Files:**
- Create: `examples/agent-framework-webhooks/typescript/src/adapters/openai.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/adapters/anthropic.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/adapters/langchain.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/adapters/adk.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/adapters/fake.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/adapters/index.ts`
- Create: `examples/agent-framework-webhooks/typescript/test/adapters.test.ts`

- [ ] **Step 1: Write failing result-extraction tests**

Inject promise/async-generator runners that yield the official result shapes and
assert `OpenAI`, `Claude`, `LangChain`, `ADK`, and `Fake` strings. Cover multiple
Anthropic text blocks, LangChain's final `AIMessage`, and ADK's final-response
event selection.

- [ ] **Step 2: Run adapter tests and verify RED**

Run: `cd examples/agent-framework-webhooks/typescript && npm test -- --run test/adapters.test.ts`

Expected: module resolution fails for all adapter files.

- [ ] **Step 3: Implement official TypeScript factories**

Use current official APIs:

```ts
// OpenAI Agents SDK
const agent = new Agent({ name: "Email assistant", instructions: INSTRUCTIONS,
  model: process.env.OPENAI_MODEL ?? "gpt-5.6" });
new OpenAIReplyAgent((prompt) => run(agent, prompt));

// Anthropic SDK
const client = new Anthropic();
new AnthropicReplyAgent((prompt) => client.messages.create({
  model: process.env.ANTHROPIC_MODEL ?? "claude-opus-4-8", max_tokens: 1024,
  system: INSTRUCTIONS, messages: [{ role: "user", content: prompt }],
}));

// LangChain 1.x
const agent = createAgent({ model: process.env.LANGCHAIN_MODEL ?? "openai:gpt-5.4",
  tools: [], systemPrompt: INSTRUCTIONS });
new LangChainReplyAgent((prompt) => agent.invoke({
  messages: [{ role: "user", content: prompt }],
}));

// Google ADK
const agent = new LlmAgent({ name: "email_assistant",
  model: process.env.ADK_MODEL ?? "gemini-flash-latest", instruction: INSTRUCTIONS });
const runner = new InMemoryRunner({ agent, appName: APP_NAME });
```

ADK uses `runner.sessionService.getOrCreateSession({ appName, userId,
sessionId })`, then consumes `runner.runAsync({ userId, sessionId, newMessage:
{ role: "user", parts: [{ text: prompt }] } })`.

- [ ] **Step 4: Run adapter tests and TypeScript compiler**

Run:

```bash
cd examples/agent-framework-webhooks/typescript
npm test -- --run test/adapters.test.ts
npm run typecheck
```

Expected: adapter tests and compile-time official SDK calls pass.

- [ ] **Step 5: Commit**

```bash
git add examples/agent-framework-webhooks/typescript
git commit -m "feat(examples): add TypeScript agent framework adapters"
```

### Task 7: TypeScript app, package, and no-key dry run

**Files:**
- Create: `examples/agent-framework-webhooks/typescript/src/app.ts`
- Create: `examples/agent-framework-webhooks/typescript/src/dry-run.ts`
- Create: `examples/agent-framework-webhooks/typescript/test/app.test.ts`
- Create: `examples/agent-framework-webhooks/typescript/test/dry-run.test.ts`
- Create: `examples/agent-framework-webhooks/typescript/package.json`
- Create: `examples/agent-framework-webhooks/typescript/tsconfig.json`
- Create: `examples/agent-framework-webhooks/typescript/vitest.config.ts`
- Create: `examples/agent-framework-webhooks/typescript/.env.example`

- [ ] **Step 1: Write failing app and dry-run tests**

Use Supertest to assert health, 401, 503, and a successful signed fake request.
Assert the dry run records one bound reply and marks the repeated event as a
duplicate.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd examples/agent-framework-webhooks/typescript && npm test -- --run test/app.test.ts test/dry-run.test.ts`

Expected: module resolution fails because app and dry-run modules do not exist.

- [ ] **Step 3: Implement Express app and dry run**

Configure `express.raw({ type: "application/json" })` so signature verification
receives original bytes. Select the same five framework names as Python, map
signature errors to 401 and processing collisions to 503, and close the
`E2AClient` on shutdown. The dry run signs a fixture with `createHmac`, uses a
real `InboundResource` over fake message operations, and calls the handler
twice.

- [ ] **Step 4: Add exact npm scripts and dependencies**

Use ESM, `engines.node >=24.13.0` (the official ADK TypeScript minimum), and
scripts `build`, `typecheck`, `test`, `start`, and `dry-run`.
Dependencies: `@e2a/sdk` through `file:../../../sdks/typescript`,
`@openai/agents`, `@anthropic-ai/sdk`,
`langchain@^1`, `@langchain/core`, `@langchain/openai`, `@google/adk`,
`express`, `dotenv`, and `zod`. Dev dependencies: TypeScript, tsx, Vitest,
Supertest, and the Express/Supertest Node type packages.

- [ ] **Step 5: Install and verify TypeScript suite**

Run:

```bash
cd examples/agent-framework-webhooks/typescript
npm install
npm test -- --run
npm run typecheck
npm run build
npm run dry-run
```

Expected: lockfile is generated; tests, typecheck, and build pass; dry run prints
one reply and one duplicate without provider keys.

- [ ] **Step 6: Commit**

```bash
git add examples/agent-framework-webhooks/typescript
git commit -m "feat(examples): ship runnable TypeScript framework webhooks"
```

### Task 8: Migrate the existing ADK example

**Files:**
- Modify: `examples/adk-cloud-webhook/webhook.py`
- Modify: `examples/adk-cloud-webhook/agent.py`
- Modify: `examples/adk-cloud-webhook/pyproject.toml`
- Modify: `examples/adk-cloud-webhook/README.md`
- Modify: `examples/adk-cloud-webhook/test_delivery_state.py`
- Modify: `examples/adk-cloud-webhook/test_live_integration.py`

- [ ] **Step 1: Change the existing AST contract test to require the facade**

Replace the old `client.webhooks.fetch_message(event)` and
`client.messages.reply(...)` assertions with:

```python
assert "await client.inbound.from_event(event)" in source
assert "await email.reply(" in source
assert "client.webhooks.fetch_message" not in source
assert "client.messages.reply" not in source
```

- [ ] **Step 2: Run the existing ADK tests and verify RED**

Run: `cd examples/adk-cloud-webhook && python -m unittest test_delivery_state.py -v`

Expected: facade contract fails against the old low-level calls.

- [ ] **Step 3: Migrate to the ergonomic facade**

Hydrate with `email = await client.inbound.from_event(event)`, format
`email.from_`, `email.subject`, `email.text`, use `email.conversation_id`, and
reply with `await email.reply(..., idempotency_key=event.id)`. Keep the existing
ADK session behavior and live fake runner, and update README diagrams and prose
to state that `construct_event` verifies the envelope before `from_event` fetches
the full message.

- [ ] **Step 4: Run existing ADK tests**

Run: `cd examples/adk-cloud-webhook && python -m unittest test_delivery_state.py -v`

Expected: all tests pass; live integration remains opt-in.

- [ ] **Step 5: Commit**

```bash
git add examples/adk-cloud-webhook
git commit -m "docs(examples): migrate ADK webhook to inbound facade"
```

### Task 9: Documentation and repository guards

**Files:**
- Create: `examples/agent-framework-webhooks/README.md`
- Modify: `README.md`
- Modify: `sdks/python/README.md`
- Modify: `sdks/typescript/README.md`
- Modify: `scripts/check-sdk-example-contracts.mjs`
- Modify: `scripts/check-repository-text-integrity.sh`
- Modify: `.github/workflows/test.yml`

- [ ] **Step 1: Add failing repository guard patterns**

Require all eight adapter files, both dry-run commands, and both facade spellings.
Reject `webhooks.fetch_message`, `webhooks.fetchMessage`, `messages.reply`, and
`client.messages.reply` inside the new suite and existing ADK webhook.

- [ ] **Step 2: Run guards and verify RED**

Run:

```bash
node scripts/check-sdk-example-contracts.mjs
bash scripts/check-repository-text-integrity.sh
```

Expected: missing canonical README/adapter/facade assertions fail.

- [ ] **Step 3: Write the canonical tutorial and links**

Document the eight-example matrix, shared lifecycle, subscription curl command,
per-framework env vars, `AGENT_FRAMEWORK` selection, Python and TypeScript
no-key dry runs, real server commands, reply status handling, at-least-once
delivery, signature-versus-DMARC distinction, untrusted prompt fields, and
in-memory state limitations. Add concise links from root and SDK READMEs.

- [ ] **Step 4: Run guards and verify GREEN**

Run the two commands from Step 2.

Expected: both guards pass.

- [ ] **Step 5: Add a dedicated examples CI job**

Add an `agent-framework-examples` job using Python 3.12 and Node 24.13.0. It
installs the local Python SDK plus the Python example, runs pytest/mypy/dry-run,
builds the local TypeScript SDK, installs the TypeScript example, and runs
Vitest/typecheck/build/dry-run. No provider secret is referenced by the job.

- [ ] **Step 6: Commit**

```bash
git add examples/agent-framework-webhooks README.md sdks/python/README.md sdks/typescript/README.md scripts .github/workflows/test.yml
git commit -m "docs(examples): document agent framework webhooks"
```

### Task 10: Full verification and handoff

**Files:**
- Modify only files needed to fix verification failures within this feature's scope.

- [ ] **Step 1: Run Python verification**

```bash
cd examples/agent-framework-webhooks/python
.venv/bin/pytest -q
.venv/bin/mypy agent_webhooks
.venv/bin/python -m agent_webhooks.dry_run
```

Expected: all pass without provider keys.

- [ ] **Step 2: Run TypeScript verification**

```bash
cd examples/agent-framework-webhooks/typescript
npm test -- --run
npm run typecheck
npm run build
npm run dry-run
```

Expected: all pass without provider keys.

- [ ] **Step 3: Run existing SDK and repository gates**

```bash
npm test --workspace @e2a/sdk
sdks/python/.venv/bin/pytest sdks/python/tests/ -q
sdks/python/.venv/bin/mypy
node scripts/check-sdk-example-contracts.mjs
bash scripts/check-repository-text-integrity.sh
git diff --check
```

Expected: SDK tests, type gates, guards, and whitespace check all pass.

- [ ] **Step 4: Review the final diff for secret and raw-MIME leakage**

Run:

```bash
git diff --stat origin/main...HEAD
rg -n "sk-|ANTHROPIC_API_KEY=.+[^.]|GOOGLE_API_KEY=.+[^.]|rawMessage|raw_message" examples/agent-framework-webhooks examples/adk-cloud-webhook
```

Expected: only placeholder keys are present; raw MIME appears only in negative
tests or explanatory security prose, never in prompt construction or logging.

- [ ] **Step 5: Commit any verification fixes**

```bash
git add examples/agent-framework-webhooks examples/adk-cloud-webhook README.md sdks/python/README.md sdks/typescript/README.md scripts/check-sdk-example-contracts.mjs scripts/check-repository-text-integrity.sh .github/workflows/test.yml
git commit -m "test(examples): verify agent framework integrations"
```

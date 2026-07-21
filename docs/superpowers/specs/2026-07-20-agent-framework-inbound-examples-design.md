# Agent Framework Inbound Examples Design

**Date:** 2026-07-20
**Status:** Approved for implementation

## Problem statement

The ergonomic inbound facade is shipped, but the repository does not show a
complete framework integration beyond the older ADK webhook tutorial. Users
need copyable Python and TypeScript examples that authenticate a public webhook,
hydrate an `InboundEmail`, run one agent turn, and reply without reconstructing
inbox or message identifiers themselves.

## Goals

- Provide Python and TypeScript integrations for OpenAI Agents SDK, Anthropic's
  direct Messages SDK, LangChain, and Google ADK.
- Make the e2a lifecycle identical across all frameworks: verify the signed
  delivery, fetch through the ergonomic inbound facade, run one model turn,
  and reply through the bound email object.
- Keep framework-specific code thin enough that readers can see the official
  API call without unrelated webhook plumbing.
- Provide deterministic fake runners so every integration can be installed,
  built, and dry-run without a provider API key or paid model call.
- Preserve at-least-once delivery safety by claiming event IDs and using the
  event ID as the reply idempotency key.
- Migrate the existing ADK webhook tutorial to the ergonomic facade without
  leaving stale examples or dead links.

## Non-goals

- No tools, handoffs, RAG, attachment ingestion, streaming, or long-term agent
  memory beyond what is needed to demonstrate one inbound turn.
- No production database implementation for event claims or framework state.
- No new e2a SDK behavior, server API, generated code, CLI, MCP, or dashboard
  changes.
- No live provider calls in CI.

## Repository shape

Add `examples/agent-framework-webhooks/` with one self-contained Python suite
and one self-contained TypeScript suite:

```text
examples/agent-framework-webhooks/
  README.md
  python/
    pyproject.toml
    agent_webhooks/
      app.py
      delivery_state.py
      prompt.py
      adapters/
        openai.py
        anthropic.py
        langchain.py
        adk.py
    tests/
  typescript/
    package.json
    tsconfig.json
    src/
      app.ts
      delivery-state.ts
      prompt.ts
      adapters/
        openai.ts
        anthropic.ts
        langchain.ts
        adk.ts
    test/
```

Each suite exposes a framework selector for local use, but every adapter remains
a directly readable file with no dynamic imports hidden from the tutorial.
The existing `examples/adk-cloud-webhook/` remains an executable compatibility
launcher for the canonical Python ADK adapter, so its documented setup and
existing repository links continue to work without duplicating webhook logic.
Its README explains the new canonical suite.

## Shared webhook lifecycle

The shared host owns only transport and delivery concerns:

1. Read the raw body and `X-E2A-Signature` header.
2. Call `construct_event` / `constructEvent` with `E2A_WEBHOOK_SECRET`. This is
   the signature-verification boundary; adapters never receive unverified data.
3. Ignore non-`email.received` event types.
4. Claim the stable event ID in the bounded, in-memory tutorial deduper.
5. Call `client.inbound.from_event(event)` / `client.inbound.fromEvent(event)`.
6. Pass the resulting safe, normalized `InboundEmail` fields to the selected
   adapter. Raw MIME and the full transport model are not included in prompts.
7. Reject an empty framework result without sending an empty email.
8. Call Python
   `email.reply({"text": reply_text, "conversation_id": email.conversation_id}, idempotency_key=event.id)`
   or TypeScript
   `email.reply({ text: replyText, conversationId: email.conversationId }, { idempotencyKey: event.id })`,
   then report the existing send status.
9. Mark the event complete only after the reply request succeeds; release the
   claim on failure so e2a can retry.

The examples document that signature verification authenticates the webhook
envelope, while `email.verified` represents the hydrated message's DMARC result.
They are separate decisions. Sender-controlled subject, body, and address fields
remain untrusted model input even when the delivery signature is valid.

## Adapter contract

Python adapters implement an async protocol equivalent to:

```python
class ReplyAgent(Protocol):
    async def reply(self, email: AsyncInboundEmail) -> str: ...
```

TypeScript adapters implement:

```ts
interface ReplyAgent {
  reply(email: InboundEmail): Promise<string>;
}
```

The real implementations use the current official SDK calls:

- OpenAI: `Agent` plus `Runner.run` in Python and `Agent` plus `run` in
  TypeScript.
- Anthropic: async `messages.create` in both official SDKs, extracting only
  text content blocks.
- LangChain: `create_agent` / `createAgent`, invoked with one user message and
  reduced to the final assistant text.
- ADK: the official Python and TypeScript ADK runners with in-memory session
  services, keyed by e2a conversation ID where the API requires a session.

Provider model names are environment-configurable and receive documented,
current defaults. Provider keys are required only for real mode.

## Fake-model dry runs

Each language suite provides a deterministic fake adapter through the same
`ReplyAgent` contract. Dry-run mode uses a fixture webhook event and a fake e2a
message/reply transport, but still executes the real shared host lifecycle,
including signature verification, facade hydration, prompt projection,
deduplication, and bound reply delegation. It prints the captured reply and
exits nonzero on an unexpected call or duplicate side effect.

Every framework adapter separates its official SDK factory from a narrow
injected runner callable. Adapter contract tests inject deterministic result
objects shaped like the official SDK response and assert text extraction. The
production factories and imports are always installed and compile-checked, so
the no-network tests do not hide SDK drift behind optional imports.

## Testing and verification

Tests are written before implementation and must prove:

- invalid signatures never fetch, invoke a framework, or reply;
- a valid `email.received` delivery calls the ergonomic facade exactly once;
- normalized email fields, not raw MIME, form the model prompt;
- all eight adapters return final reply text through their real result shapes;
- the bound `email.reply` path uses `event.id` as the idempotency key;
- duplicate deliveries cause one framework turn and one reply;
- empty model output is acknowledged without an empty reply;
- failures release the event claim for retry;
- Python passes pytest and static type checking;
- TypeScript passes its compiler, unit tests, and production build;
- both no-key dry-run commands execute successfully.

The repository's example-contract and text-integrity checks are extended so
future SDK changes cannot leave these examples on the low-level
`webhooks.fetch_message` / `messages.reply` pattern.

## Documentation

The suite README includes setup, webhook subscription, framework selection,
real-mode environment variables, no-key dry-run commands, and the trust model.
Root and SDK documentation link to the suite. Each adapter includes only the
minimum provider-specific explanation and links to its official framework
documentation.

## Rollout

This is an examples-only change. Dependency locks are committed where the
example toolchain produces them deterministically. No e2a package version is
bumped. CI runs fake paths only; live provider smoke tests remain opt-in.

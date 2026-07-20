# Inbound Email Ergonomics Design

**Date:** 2026-07-20
**Status:** Approved for planning

## Problem statement

The v5 SDKs expose the correct low-level inbound flow, but callers must compose
several concepts for every received email:

1. verify and parse a webhook with `construct_event` / `constructEvent`, or
   consume the equivalent WebSocket event;
2. narrow the envelope to `email.received`;
3. fetch the full `MessageView` from the event's `delivered_to` and
   `message_id` keys;
4. manually repeat those keys to reply, forward, or fetch attachments.

This is secure and contract-faithful, but it feels like a REST client rather
than an email SDK. The generated `MessageView` is intentionally a data model
and should not acquire client-bound behavior. The SDKs need one additive
ergonomic layer that composes a trusted event, its fetched message, and the
client operations that act on that message.

## Goals and non-goals

### Goals

- Add an `InboundEmail` domain facade to the TypeScript SDK and Python sync
  client, plus an `AsyncInboundEmail` facade to the Python async client.
- Add `client.inbound.fromEvent(event)` in TypeScript and
  `client.inbound.from_event(event)` in Python.
- Accept the shared event envelope produced by either webhook verification or
  the authenticated WebSocket stream.
- Expose normalized, discoverable properties for the common inbound-email
  fields while retaining the original event and generated message.
- Bind `reply` and `forward` to the correct inbox and message ID.
- Expose attachment metadata with lazy content retrieval.
- Preserve the existing retry, idempotency, error, credential-scope, and
  transport behavior by delegating to existing resources.
- Keep all existing low-level APIs fully supported.

### Non-goals

- No server, database, OpenAPI, CLI, MCP, or dashboard changes.
- No MIME parsing in either SDK.
- No eager attachment downloads.
- No replacement or mutation of generated `MessageView` models.
- No return of an unverified email object and no reintroduction of the v2
  getter-based verification gate.
- No automatic reply callback framework.
- No generic facade for non-`email.received` event types.
- No `markRead`, label-management, delete, or restore convenience methods in
  this first slice; callers retain `client.messages` for those operations.

## Relevant context and constraints

- `construct_event` / `constructEvent` already performs raw-body JSON parsing,
  per-webhook HMAC verification, and replay-window enforcement.
- `is_email_received` / `isEmailReceived` already narrows the stable v1
  `email.received` data payload.
- Webhooks, WebSockets, and persisted events use the same versioned envelope.
- `email.received` is metadata-only. `MessageView` obtained through the bearer-
  authenticated REST API remains the source of truth for content.
- `client.webhooks.fetch_message` / `fetchMessage` already resolves the event's
  fetch keys. It remains supported, although its namespace reflects its
  original webhook use and not the shared WebSocket use case.
- TypeScript generated models use camelCase while event wire payloads use
  snake_case. Python uses snake_case throughout.
- Python v5 has both `AsyncE2AClient` and a synchronous `E2AClient` bridge. A
  facade returned by the async client cannot leak coroutine methods through the
  sync facade.
- `from` is a display/reply identity and can differ from
  `authenticated_from`; the facade must keep both explicit and must not imply
  that `from` is authenticated.

## Proposed design

### 1. New inbound resource

Both clients gain an `inbound` resource alongside `messages`, `events`, and
`webhooks`.

TypeScript:

```ts
const email = await client.inbound.fromEvent(event);
```

Python async:

```py
email = await client.inbound.from_event(event)
```

Python sync:

```py
email = client.inbound.from_event(event)
```

`fromEvent` / `from_event`:

1. validates that the envelope is a stable schema-v1 `email.received` event;
2. resolves `delivered_to` and `message_id`;
3. delegates to the existing `messages.get` path;
4. returns a client-bound facade containing both the event and `MessageView`.

The resource is named `inbound`, rather than `webhooks`, because it accepts
events from both webhooks and WebSockets. The existing
`webhooks.fetchMessage` / `fetch_message` remains unchanged.

### 2. TypeScript `InboundEmail`

The TypeScript facade exposes:

```ts
class InboundEmail {
  readonly event: WebhookEvent & {
    type: "email.received";
    data: EmailReceivedData;
  };
  readonly message: MessageView;

  readonly id: string;
  readonly inbox: string;
  readonly conversationId?: string;
  readonly from: string;
  readonly authenticatedFrom: string;
  readonly to: string[];
  readonly cc: string[];
  readonly replyTo: string[];
  readonly subject: string;
  readonly text: string;
  readonly html?: string;
  readonly receivedAt: Date;
  readonly attachments: InboundAttachment[];

  reply(body: ReplyInput, options?: RequestOptions): Promise<SendResultView>;
  forward(body: ForwardInput, options?: RequestOptions): Promise<SendResultView>;
}
```

Normalized properties are derived without copying message bodies or attachment
bytes:

- `id` and `inbox` come from the event fetch keys.
- routing and identity metadata comes from the typed event payload;
  `authenticatedFrom` is never aliased to `from`.
- `text` and `html` come from `message.parsed`; absent text normalizes to the
  empty string and absent HTML remains `undefined`.
- `receivedAt` parses the event's required RFC 3339 timestamp into a `Date`,
  matching generated TypeScript date-time conventions.
- `event` and `message` preserve full-fidelity access for fields that do not
  warrant facade aliases.

`reply` and `forward` delegate to the existing `MessagesResource` with the
stored inbox and message ID. They accept the same request bodies and
`RequestOptions`, including caller-supplied idempotency keys.

### 3. Python facades and sync parity

The async client returns `AsyncInboundEmail`; its `reply` and `forward` methods
are coroutines. The sync client returns `InboundEmail`; its corresponding
methods block through the existing event-loop bridge.

Both Python facades expose the same data properties in snake_case:

- `event`, `message`
- `id`, `inbox`, `conversation_id`
- `from_`, `authenticated_from`
- `to`, `cc`, `reply_to`
- `subject`, `text`, `html`, `received_at`
- `attachments`

`received_at` is a timezone-aware `datetime`, consistent with generated Python
date-time models. `text` normalizes to `""`; `html` remains optional.

The implementation must explicitly adapt the facade at the sync bridge
boundary. A returned `AsyncInboundEmail` must not be treated as ordinary data
and exposed unchanged by `E2AClient`. The sync representation delegates its
operations through the same `_EventLoopBridge`, so it preserves the async
client's retry and lifecycle behavior without duplicating transport logic.

### 4. Lazy attachments

Each facade exposes attachment metadata immediately but not attachment bytes.

TypeScript `InboundAttachment` provides camelCase metadata plus:

```ts
fetch(options?: { inline?: boolean }): Promise<AttachmentView>;
```

Python `AsyncInboundAttachment` provides:

```py
await attachment.fetch(inline=False)
```

Python `InboundAttachment` provides the blocking equivalent:

```py
attachment.fetch(inline=False)
```

Each attachment stores only its metadata, inbox, message ID, and index, then
delegates to `messages.getAttachment` / `get_attachment`. The server's existing
inline-size cap and short-lived download URL behavior remain authoritative.

### 5. Exports and documentation

The new resource and facade types are exported from the stable v1 entry points
and top-level convenience entry points in both SDKs. TypeScript's committed
`dist/` is rebuilt.

The SDK READMEs and root SDK examples gain a concise inbound example showing:

1. `construct_event` / `constructEvent`;
2. the existing type guard;
3. `client.inbound.from_event` / `fromEvent`;
4. normalized field access and a bound reply.

The documentation keeps the lower-level path visible for callers that only
need event metadata or a raw `MessageView`.

## Edge cases and failure handling

- A non-`email.received` event, unknown schema version, or event missing either
  fetch key is rejected locally before an HTTP request. Use the SDK's existing
  client-side validation error convention rather than a raw `KeyError`,
  `TypeError`, or generic `Error`.
- A synthetically constructed envelope is not automatically trustworthy.
  Documentation continues to require `construct_event` / `constructEvent` for
  webhook input. REST credential scoping still prevents fetching another
  account's message, but does not replace webhook verification.
- API authorization, missing message, malformed response, rate-limit, and
  connection failures propagate through the existing typed error hierarchy.
- A message with no parsed body yields `text == ""` and optional/absent HTML.
- Missing optional recipient arrays normalize to empty arrays/lists.
- Invalid `received_at` is treated as an invalid trusted response/event rather
  than silently producing an invalid date.
- Attachment fetch failures use existing typed API errors. Repeated `fetch`
  calls are independent; the facade does not cache short-lived URLs or bytes.
- Bound operations after client closure produce the existing `client_closed`
  behavior in Python and the existing transport behavior in TypeScript.
- Caller-supplied idempotency options pass through unchanged. Omitted keys keep
  the SDK's existing mint-once-per-call behavior.

## Scalability and extensibility notes

- Event notifications stay bounded because hydration remains explicit and
  performs one message fetch only when requested.
- Attachment contents remain lazy, preventing accidental memory growth in
  agents processing large messages.
- Keeping `event` and `message` public prevents facade growth from becoming a
  release blocker; uncommon fields remain available without adding aliases.
- The new `inbound` namespace can later host deliberately related inbound
  helpers, but this design does not add generic event dispatch, automatic
  handling, or non-email facades.
- Additional bound message operations can be added additively if usage data
  justifies them. The first version deliberately limits behavior to `reply`,
  `forward`, and attachment retrieval.

## Verification strategy

### TypeScript

- Unit-test successful event hydration and every normalized field.
- Verify `fromEvent` rejects wrong type, wrong schema version, and missing keys
  before transport.
- Verify `reply` and `forward` use the stored inbox/message ID and preserve
  caller idempotency keys.
- Verify attachment fetch uses the correct stable index and options.
- Add compile-time type tests for event narrowing, facade properties, and
  method inputs.
- Run the complete TypeScript SDK tests and build; confirm committed `dist/`
  matches source.

### Python

- Mirror all hydration, validation, bound-operation, and attachment tests for
  `AsyncE2AClient`.
- Add sync-client parity tests proving `InboundEmail.reply`, `forward`, and
  attachment `fetch` are blocking methods and do not return coroutine objects.
- Verify lifecycle and typed errors pass through the sync bridge.
- Extend export and typing tests for all public facade types.
- Run pytest and mypy for the Python SDK.

### Cross-SDK contract checks

- Use the shared canonical `email.received` fixture so both SDKs derive the
  same normalized values.
- Confirm existing `constructEvent`, type guards, `fetchMessage`, message
  operations, WebSocket behavior, and generated models remain unchanged.

## Rollout

This is an additive SDK-only feature. It can ship in a minor SDK release after
both language implementations, documentation, committed TypeScript build
output, and package-version synchronization are ready. No server coordination
or migration is required.

## Open questions

There are no blocking product questions for the first slice. The following are
explicitly deferred until real usage justifies them:

- bound read-state, label, delete, or restore operations;
- an all-in-one raw webhook verification-and-hydration method;
- normalized quoted-history stripping beyond the server's current parsed body;
- facades for outbound lifecycle or domain events.

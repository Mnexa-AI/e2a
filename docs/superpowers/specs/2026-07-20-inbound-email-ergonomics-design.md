# Inbound Email Ergonomics Design

**Date:** 2026-07-20
**Status:** Approved for implementation; revised after PR review

## Problem statement

The v5 SDKs expose the correct low-level inbound flow, but callers must verify
or receive an event, validate its shape, fetch the full message using two wire
keys, and repeat those keys for every reply, forward, and attachment request.
The generated `MessageView` should remain a transport model, so the SDKs need
an additive domain facade that composes a trusted event, its hydrated message,
and the existing client operations that act on it.

## Goals

- Add `client.inbound.fromEvent(event)` in TypeScript and
  `client.inbound.from_event(event)` in Python.
- Return `InboundEmail` in TypeScript and Python sync, and
  `AsyncInboundEmail` in Python async.
- Accept schema-v1 `email.received` envelopes from verified webhooks and the
  authenticated WebSocket stream.
- Validate the envelope and both REST fetch keys at runtime before transport.
- Expose normalized message, authentication, policy, recipient, body,
  timestamp, and attachment metadata while retaining `.event` and `.message`.
- Bind reply and forward to the correct inbox and message ID.
- Preserve existing retry, idempotency, error, scope, and transport behavior by
  delegating to the existing resources.
- Add a shared, language-neutral conformance-vector gate for this facade only.

## Non-goals

- No server behavior, database, CLI, MCP, or dashboard changes. The facade uses
  the canonical inbound authentication contract already exposed by the server.
- No MIME parsing, eager attachment downloads, or mutation of generated
  `MessageView` models.
- No persisted `EventView` adapter in this slice. Its generated naming differs
  by language; callers can adapt persisted events explicitly until a real use
  case justifies a public adapter.
- No promise that message content is prompt-safe. The server does not currently
  expose every content-scan outcome on `MessageView`.
- No automatic reply callback framework or generic facade for other events.
- No read-state, label, delete, restore, or refresh conveniences.

## Trust and source-of-truth model

- Raw webhook input must first pass `constructEvent` / `construct_event`, which
  verifies the signature and replay window. WebSocket events arrive through an
  authenticated SDK connection. A hand-built object is not made trustworthy by
  `fromEvent` / `from_event`.
- The facade performs its own structural runtime validation. Existing type
  guards do not prove that `message_id` and `delivered_to` are non-empty
  strings, and Python's current guard does not narrow `data` structurally.
- `email.received` is metadata-only. The bearer-authenticated
  `messages.get(...)` response is authoritative for content, recipients,
  attachments, timestamps, authentication results, and policy flags.
- `.event` is the original notification and `.message` is the hydrated
  point-in-time snapshot. The facade does not silently refresh either object.
- All sender-controlled strings, bodies, filenames, content types, and reply
  targets remain untrusted input.

## Public shape

Both clients gain an `inbound` resource alongside `messages`, `events`, and
`webhooks`. The namespace is intentional: `webhooks.fetchMessage` remains the
low-level raw-message helper, while `inbound.fromEvent` accepts webhook or
WebSocket envelopes and returns a domain facade.

TypeScript:

```ts
class InboundEmail {
  readonly event: EmailReceivedEvent;
  readonly message: MessageView;

  readonly id: string;
  readonly inbox: string;
  readonly conversationId?: string;
  readonly from: string | null;
  readonly envelopeFrom: string | null;
  readonly verified: boolean;
  readonly authentication: Authentication | null;
  readonly to: string[];
  readonly cc: string[];
  readonly replyTo: string[];
  readonly replyTargets: string[];
  readonly subject: string;
  readonly text: string;
  readonly html?: string;
  readonly textTruncated: boolean;
  readonly receivedAt: Date;
  readonly flagged: boolean;
  readonly flagReason?: string;
  readonly attachments: InboundAttachment[];

  reply(body: ReplyInput, options?: RequestOptions): Promise<SendResultView>;
  forward(body: ForwardInput, options?: RequestOptions): Promise<SendResultView>;
}
```

Python exposes the same values in snake case. The async client returns
`AsyncInboundEmail`, whose operations are coroutines; the sync client returns
`InboundEmail`, whose operations block through the existing event-loop bridge.

## Field semantics

- `id`, `inbox`, and `conversationId` come from the hydrated `MessageView`
  after the event fetch keys have selected it.
- `from` / `from_` is the parsed RFC 5322 From identity and is nullable and
  sender-controlled.
- `envelopeFrom` / `envelope_from` is the SMTP envelope identity recorded by
  the hydrated message. It is nullable for a null reverse path or providerless
  delivery; identity alone is not proof of a passing verdict.
- `authentication` exposes the structured SPF, per-signature DKIM, and DMARC
  evidence. `verified` is true only when DMARC status is `pass`; absent
  authentication is not verified.
- `replyTo` / `reply_to` is the declared Reply-To list. `replyTargets` /
  `reply_targets` is Reply-To when non-empty, otherwise the parsed From
  identity available on MessageView (`[from]`). It is an inspection preview;
  the server remains authoritative because it resolves recipients again from
  the stored raw MIME when replying.
  These are the destinations the server's reply flow will use; they may be
  attacker-controlled and are not necessarily authenticated.
- `text` prefers `message.parsed.text`, which is the server's quote-stripped,
  capped parsed body. It is `""` only when `parsed` is absent. `html` remains
  optional. `textTruncated` / `text_truncated` exposes `parsed.truncated`.
- `receivedAt` / `received_at` comes from the generated
  `MessageView.createdAt` / `created_at`, avoiding a second RFC 3339 parser and
  preserving Python 3.9 compatibility.
- `flagged` and `flagReason` / `flag_reason` expose the current inbound-policy
  gate fields only. They are not a comprehensive prompt-injection verdict.
  Review/block decisions do not produce `email.received`, and the current API
  does not expose the denormalized content-scan `scan_action` on MessageView.
- Arrays are immutable/copy-on-construction in TypeScript and value-oriented
  copies in Python. Generated MessageView recipient arrays are required, so
  missing arrays indicate a malformed response rather than a valid empty case.

`toJSON()` in TypeScript and Python `repr`/safe projection must contain only
normalized metadata. They must omit raw MIME, attachment URLs/data, transport
objects, and client internals. Advanced callers can opt into `.message`.

## Validation and failure handling

`fromEvent` / `from_event` accepts the public webhook/WebSocket envelope type
and then checks:

1. `schema_version === "1"`;
2. `type === "email.received"`;
3. `data` is an object/mapping;
4. `data.message_id` and `data.delivered_to` are non-empty strings.

Failure raises the existing `E2AValidationError` convention with code
`invalid_email_received_event`, status `0`, and `retryable: false`, before any
HTTP call. API, lifecycle, and transport failures propagate unchanged.

Reply and forward return the existing `SendResultView`. Callers must inspect
`status`, including `pending_review`; a successful HTTP response does not imply
delivery. Examples must not discard this result.

## Attachments

Hydrated `MessageView.attachments` is the sole attachment source of truth.
Each facade item exposes the generated metadata, including stable `index`,
`filename`, `contentType` / `content_type`, `size`, and `contentId` /
`content_id`, plus:

```ts
get(options?: { inline?: boolean }): Promise<AttachmentView>;
```

Python provides async and blocking `get(inline=False)` equivalents. The name
is deliberately not `fetch`: by default the server returns metadata and a
short-lived download URL, not bytes. `inline=True` may return base64 data only
within the server's existing 256 KiB cap. Calls are uncached and preserve the
existing typed errors.

## Python sync boundary

`sync_client._wrap_attr` returns coroutine results directly from
`_EventLoopBridge.submit()`; it does not subsequently run `_wrap_value` on the
result. Therefore an async facade would otherwise leak through the sync
client. Add explicit sync `InboundEmail` and `InboundAttachment` adapters at
the inbound resource boundary. Do not broaden generic wrapping for unrelated
SDK objects.

## InboundEmail conformance gate

Add shared JSON vectors under `sdks/testdata/inbound-email/`, consumed by both
SDK unit suites. Each vector contains an event, hydrated MessageView response,
and expected safe normalized projection. Include at least:

- a full case with structured authentication, parsed text/HTML, a
  stable attachment, and divergent Reply-To;
- a minimal case with no parsed body or attachments;
- an adversarial case with failed authentication, policy flag, truncated body,
  misleading From/Reply-To/envelope identity, and untrusted attachment metadata;
- invalid cases for event type, schema, missing/null/empty fetch keys, and
  malformed data.

The same vectors lock field names and semantics across TypeScript, Python
async, and Python sync. Language-specific tests additionally gate delegation,
typed errors, blocking/awaitable behavior, exports, and compile-time types.
This is a facade behavior gate, not a replacement for the existing OpenAPI
compatibility and generated-code freshness gates.

Live contract-server tests continue to cover the HTTP resources to which the
facade delegates. The facade's distinctive contract—normalization and binding
of an already-typed MessageView—is deterministically covered by the shared
vectors; adding a second seeded server harness would not exercise additional
facade semantics in this SDK-only slice.

## Documentation and rollout

Update every checked documentation consumer enforced by repository guards:
root README, both SDK READMEs, `docs/events.md`, and canonical
`plugins/e2a/docs/sdk.md` (synced to `web/public/sdk.md`). The
examples must verify raw webhooks, show `replyTargets`, describe untrusted
content and policy-flag limits, use `attachment.get`, and branch on
`SendResultView.status`.

Record the addition under both changelogs' `Unreleased` sections. Do not bump
package versions in the feature PR; the release owner selects the next minor
version during the normal publish workflow. TypeScript `dist/` is ignored and
must not be committed.

## Deferred work

- expose content-scan outcome on the server/OpenAPI if a comprehensive
  high-level safety verdict is desired;
- adapt persisted EventView envelopes;
- add refresh/read-state/label/delete/restore helpers;
- add facades for other event types.

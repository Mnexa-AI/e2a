# API Reference

This is a human-readable **overview** of the e2a `/v1` REST surface, organized by
resource. It is intentionally not an exhaustive endpoint-by-endpoint table — that
would rot. The canonical, always-current contract is the generated OpenAPI 3.1
spec:

> **Source of truth:** [`api/openapi.yaml`](../api/openapi.yaml). It is emitted
> directly from the typed Huma handlers in `internal/httpapi/` and CI fails if the
> committed copy drifts from the live server. Every path, query parameter,
> request body, and response shape is defined there. If anything here disagrees
> with the spec, **the spec wins.**

For **usage** (ergonomic clients, pagination helpers, webhook verification, the
MCP tool surface), see:

- TypeScript SDK — [`sdks/typescript/README.md`](../sdks/typescript/README.md) (`@e2a/sdk`)
- Python SDK — [`sdks/python/README.md`](../sdks/python/README.md) (`e2a`)
- MCP server — [`mcp/README.md`](../mcp/README.md) (`@e2a/mcp-server`)
- Webhook events & replay — [`events.md`](events.md)

## Conventions

- **Base path.** Every endpoint below is under `/v1` unless explicitly noted
  (`/api/health`, `/api/feedback`, and the WebSocket channel are the exceptions).
- **Auth.** `Authorization: Bearer <api_key>`. Keys are **scoped**:
  - `scope=account` — workspace admin: manage agents, domains, API keys,
    webhooks, and resolve reviews.
  - `scope=agent` — bound to a single inbox; can act only as that one agent and
    cannot manage account-level resources or approve its own held messages.

  The unauthenticated exceptions are `GET /api/health`, `GET /v1/info`,
  `POST /api/feedback`, and the HITL magic-link routes (which carry a signed `t`
  token instead).
- **Path parameters with `@`/`+`.** Agent (and suppression/domain) paths are
  addressed by a full email/host (`/v1/agents/{email}/…`). **Percent-encode the
  segment**: `@` → `%40` and — importantly — `+` → `%2B`. A bare `+` in a path is
  often decoded to a space by clients/proxies, which silently corrupts
  plus-tagged addresses (`a+tag@x.com`). The official SDKs encode this for you;
  hand-rolled clients must do it themselves.
- **Pagination.** List endpoints return `{ items, next_cursor }`; pass
  `next_cursor` back as `?cursor=…` to page forward. The SDKs auto-page.
- **Idempotency.** Five mutating operations honor an opt-in `Idempotency-Key`
  header: `sendMessage`, `replyToMessage`, `forwardMessage`, `approveReview`,
  and `rotateWebhookSecret`. Semantics:
  - **Replay.** A retry with the same key and a **byte-identical** body replays
    the first request's response instead of re-executing the side effect (the
    dedup hash covers the route + the raw body bytes, so the same key on a
    different route or with re-serialized JSON does not match).
  - **Dedup window: at least 24 hours.** Completed keys are remembered for a
    minimum of 24 hours after completion. Treat 24h as the published floor —
    a deployment may remember keys longer, never shorter.
  - **`409 idempotency_in_flight`** — a request with the same key is still
    executing. Retry-able: wait for the first request to finish, then retry
    unchanged (same key, byte-identical body) to have its response replayed.
  - **`422 idempotency_key_reuse`** — the key was already used with a
    *different* request body. Do **not** blind-retry this one: a legitimate
    retry must resend the byte-identical body, and a genuinely new request
    needs a fresh key.
  - **Best-effort.** Dedup is best-effort, not transactional: under
    idempotency-store degradation or a mid-request crash the protection
    degrades to at-least-once — a keyed retry may re-execute the operation
    rather than replay the cached response.
- **Errors.** Non-2xx responses use a single `ErrorEnvelope` shape; branch on
  `error.code` (see [Error codes](#error-codes) below for the full vocabulary).
- **Capacity limits — the permanent `402` / `429` split.** Two different limits
  can block a write, and they are **permanently distinct** — branch on the HTTP
  status:
  - **`402 limit_exceeded`** is a **quota** (a stock/flow cap): monthly-message
    allowance, storage bytes, agent/domain counts. A retry alone will not clear
    it — surface an upgrade/quota path. `error.details` is a `LimitExceededDetails`
    whose `resource` (`agents | domains | messages_month | storage_bytes`) keys the
    cap to `usage.<resource>` / `limits.max_<resource>` on `GET /v1/account`.
  - **`429 rate_limited`** is a **throughput / request-rate** limit (e.g. the
    per-agent send rate). It is transient and retry-able: wait
    `error.details.retry_after_seconds` (mirrored on the `Retry-After` header),
    then the same request succeeds.

  This is frozen GA semantics: `402` = QUOTA, `429` = RATE. Clients (and the
  official SDKs — `E2ALimitExceededError` vs `E2ARateLimitError`) must branch on
  the status, never conflate the two. The write operations that declare both are
  `sendMessage` / `replyToMessage` / `forwardMessage` / `testAgent` / `createAgent`
  (`registerDomain` declares only `402`; `approveReview` declares only `429`).

## Error codes

`error.code` is the stable, machine-branchable discriminator of the error
contract. It is an **open set**: treat it as a string and tolerate unknown
values — new codes may be added over time without a version bump. Branch on the
codes you handle and fall back to the HTTP status otherwise. The catalog below
is exhaustive for the current server (a drift test pins the source to this
vocabulary); codes are never renamed or removed within `/v1` — that would be
breaking.

Naming families: `invalid_*` = a validation refinement of `invalid_request`;
`*_not_found` = a specific missing (sub)resource; `*_taken` = the requested
identifier is already claimed (409). The official SDKs map these families to
their typed error classes code-first, so an unfamiliar member of a family still
lands in the right class.

Retry guidance: unless noted, 4xx codes are **not** retryable (fix the request
or the state first); `rate_limited`, `idempotency_in_flight`, and 5xx
`internal_error`/`limits_unavailable` are the retryable ones.

| Code | Status | Meaning |
| --- | --- | --- |
| **Auth / policy** | | |
| `unauthorized` | 401 | Missing or invalid credentials (REST and the WebSocket handshake). |
| `forbidden` | 403 | Authenticated but not allowed (key scope, cross-tenant access). |
| `blocked_by_policy` | 403 | The outbound message was blocked by the agent's outbound policy gate. |
| **Validation** | | |
| `invalid_request` | 400 / 422 | The canonical input-validation code — malformed (400) or semantically invalid (422). `error.details` carries the per-field list. |
| `invalid_cursor` | 400 | Bad pagination cursor — drop it and re-fetch from the start. |
| `invalid_filter` | 400 | Bad list-filter parameter (messages/conversations/events). |
| `invalid_domain`, `invalid_slug`, `invalid_recipient`, `invalid_attachment`, `invalid_template`, `invalid_event_type`, `invalid_webhook_url`, `invalid_expires_at`, `invalid_scope` | 400 | Field/resource-specific refinements of `invalid_request`. |
| `reserved_domain` | 400 | The domain is reserved by the deployment (e.g. the shared domain). |
| `too_many_recipients` | 400 | Send/reply/forward recipient count over the cap. |
| `template_render_failed`, `template_rendered_empty` | 400 | Template send: rendering failed / produced an empty body. |
| `recipient_suppressed` | 422 | A recipient is on the account suppression list — un-suppress or drop it. |
| **Not found / gone** | | |
| `not_found` | 404 | No such resource (agents, messages, webhooks, …). |
| `attachment_not_found`, `template_not_found`, `starter_template_not_found` | 404 | The `*_not_found` family — a specific sub-resource is missing. |
| `gone` | 410 | The event exists but is past the 30-day retention window. |
| **Conflict / state** | | |
| `conflict` | 409 | Generic state conflict (e.g. redelivery to a webhook that never matched the event). |
| `agent_taken`, `domain_taken`, `alias_taken` | 409 | The `*_taken` family — the requested identifier (agent address, domain, template alias) is already claimed. |
| `message_not_pending` | 409 | The review hold was already resolved (approved/rejected/expired). |
| `webhook_disabled` | 409 | Operation requires an enabled webhook. |
| `webhook_cooldown` | 409 | The webhook was auto-disabled and cannot be re-enabled until the cooldown elapses — retryable after the cooldown. |
| `domain_not_registered` | 400 | Create-agent on a domain the account has not registered. |
| `domain_has_agents` | 400 | Domain delete blocked while agents exist on it. |
| `domain_not_verified` | 400 / 403 | Domain verification pending — 400 on create-agent, 403 on send paths. |
| **Capacity — see the 402/429 split above** | | |
| `limit_exceeded` | 402 | Plan **quota** (stock/flow cap); `details` is `LimitExceededDetails`. Not retryable. |
| `rate_limited` | 429 | Request-**rate** limit; wait `details.retry_after_seconds` / `Retry-After`, then retry. |
| `template_limit_reached`, `webhook_limit_reached` | 400 | Fixed per-account count caps (not plan quotas) — delete one first. |
| **Idempotency** | | |
| `idempotency_in_flight` | 409 | Same key still executing — wait, then retry the byte-identical request to replay it. |
| `idempotency_key_reuse` | 422 | Same key, different body — caller bug; never blind-retry. |
| **Size** | | |
| `payload_too_large` | 413 | Request body / total attachments over the cap. |
| `attachment_too_large` | 413 | `?inline=true` requested for an attachment over the inline cap — use `download_url`. |
| **Availability** | | |
| `not_implemented` | 501 | The feature (API keys, reviews, suppressions) is not available on this deployment. Not retryable. |
| `events_log_disabled` | 501 | The events log is disabled on this deployment (expected on some hosted configurations). Not retryable. |
| `limits_unavailable` | 503 | The limits subsystem is not available — transient, retryable. |
| **Server / fallback** | | |
| `internal_error` | 5xx | Server-side failure; safe to retry with backoff unless the message says otherwise. |
| `method_not_allowed` | 405 | Fallback code (wrong HTTP method on a real route). |
| `unsupported_media_type` | 415 | Fallback code (non-JSON request body). |
| `error` | other 4xx | Generic fallback for any otherwise-unmapped status (e.g. 406). |

The SDKs additionally synthesize the client-side code `connection_error`
(status `0`) when no HTTP response was received at all; it never comes from the
server and is always retryable.

## Versioning & stability

The `/v1` surface is the **stable, generally-available contract** as of e2a 1.0.
Our commitment, and what you can rely on:

- **No breaking changes within `/v1`.** We will not remove an endpoint, remove a
  response field, rename anything, tighten a type, or change documented semantics
  under `/v1`. A breaking change means a new major version path (`/v2`), and the
  two would run side by side during a published migration window.
- **Additive changes can happen anytime** and are *not* breaking: new endpoints,
  new optional request fields, and **new response fields**. Clients must ignore
  fields they don't recognize. This is machine-readable in the spec: every
  **response** schema declares `additionalProperties: true` (a client generated
  from the spec tolerates additive fields), while every **request** schema stays
  strict (`additionalProperties: false`) — an unknown request field is rejected
  with a 422, which is intentional input validation (it catches typos like
  `body` vs `text`), not a stability concern.
- **Experimental surfaces are marked `x-stability: experimental`** in the spec
  (operations, schemas, and individual fields — e.g. the `template_*` fields on
  send) and `(beta)` in prose — today: templates, starter templates, and the
  agent protection config. They are **exempt from the
  freeze**: they may change or be removed without a major version. Where only
  specific *values* of a stable field are experimental (the screening +
  review-hold event types `email.flagged`, `email.blocked`,
  `email.pending_review`, `email.review_approved`, `email.review_rejected`),
  the field carries `x-experimental-values` listing exactly those values —
  their payloads may still change; all other event types are stable. Anything
  not marked experimental is stable surface.
- **Enums in responses are open.** Treat any `type` / `*_status` / `event_type`
  value as an open string set: we may introduce new values (e.g. a new event
  type or delivery state) without a major bump, so a client **must not crash on
  an unknown value** — handle it as a default/passthrough case. (The official
  SDKs already do this.) Enum values you *send* in requests are validated and
  rejected if unknown — that's intentional and not a stability concern.
- **Version discovery.** `GET /v1/info` reports the running API version (and
  deployment flags such as whether shared-domain slug registration is enabled),
  so clients can adapt instead of hard-coding assumptions.
- **Deprecation & sunset.** Once `/v1` is GA, if we ever need to wind something
  down it stays functional and is marked `deprecated` in the OpenAPI spec; we
  will not remove it within GA `/v1`. (Pre-GA, the API is still being frozen:
  the legacy agent-path `…/messages/{id}/approve|reject` endpoints were removed
  in favor of the account-scoped `/v1/reviews/{id}/approve|reject` queue.)

The canonical machine-readable contract is always
[`api/openapi.yaml`](../api/openapi.yaml); CI fails if it drifts from the server.

## Resources

The surface is **agent-first**: messages, conversations, and the real-time
channel all hang off an agent (inbox). Reviews, events, webhooks, domains, and
account/key management are account-level.

### Account (`/v1/account`)

Workspace identity, plan limits, keys, suppressions, and data rights.

- `GET /v1/account` — whoami: the authenticated principal (user + scope, plus
  `agent_address` for agent-scoped keys), plan caps, and current usage. Works for
  both scopes. (Public *deployment* discovery is the separate `GET /v1/info`.)
- `DELETE /v1/account?confirm=DELETE` — permanently delete the account and cascade
  all owned data; returns per-table row counts (GDPR Art. 17). Irreversible.
- `GET /v1/account/export` — JSON dump of every record the account owns (GDPR
  Art. 15). Omits internal identifiers; see [data-handling.md](data-handling.md).
  Each exported message carries `attachments` as the same typed
  `AttachmentMeta` metadata (`{filename, content_type, size_bytes, index}`,
  `size_bytes` = decoded payload) the live API uses; the attachment **bytes**
  of sent/inbound messages are inside the exported `raw_message`. A held
  draft's (`pending_review`) staged attachment bytes are internal transient
  storage and are not inlined.
- `GET/POST /v1/account/api-keys`, `DELETE /v1/account/api-keys/{id}?confirm=DELETE`
  — mint (plaintext shown once), list (metadata only), and revoke API keys.
  Account scope only.
- `GET /v1/account/suppressions`, `DELETE /v1/account/suppressions/{address}?confirm=DELETE`
  — the recipient suppression list (auto-added on hard bounce/complaint; sends to
  a suppressed address fail with `recipient_suppressed`). Delete to un-suppress.

Every `DELETE` endpoint requires the `?confirm=DELETE` query param (a required
`enum: [DELETE]`); a missing or wrong value is rejected before the delete runs.
The SDKs and CLI supply it automatically for their typed `delete(...)` calls.

**Uniform delete responses.** Every `DELETE` returns `200 OK` with a small
typed deletion object — never `204 No Content`. The base shape is
`{"deleted": true, "<identity key>": ...}` where the identity key matches the
resource's identity field: `id` for webhooks/templates/API keys, `email` for
agents, `domain` for domains, `address` for suppressions. `deleted` is always
`true` — a failed delete is an error envelope, never `deleted: false`.
Cascading deletes may additionally carry receipt counts (all additive):
`DELETE /v1/agents/{email}` includes `messages_deleted`, and `DELETE
/v1/account` returns the full per-table `DeleteUserDataResult` receipt on top
of `deleted: true`.

### Domains (`/v1/domains`)

Custom sending/receiving domains and their DNS verification.

- `GET /v1/domains`, `POST /v1/domains` — list / register (returns required MX +
  TXT records and the DKIM selector/key).
- `GET /v1/domains/{domain}`, `DELETE /v1/domains/{domain}?confirm=DELETE` —
  fetch / delete (delete deprovisions the sending identity; irreversible).
- `POST /v1/domains/{domain}/verify` — verify ownership via the TXT record.

### Agents (`/v1/agents`)

An agent is an addressable inbox. Its email must be on a verified domain you own,
or on the deployment's shared domain (see `GET /v1/info`).

- `GET /v1/agents`, `POST /v1/agents` — list / register (body `{ email, name? }`).
- `GET/PATCH/DELETE /v1/agents/{email}` — fetch / rename / delete. `PATCH` updates
  the display name only; screening/protection config lives on the sub-resource
  below. `DELETE` requires `?confirm=DELETE`.
- `GET/PUT /v1/agents/{email}/protection` — **(beta)** read / wholesale-replace the
  agent's protection posture: inbound/outbound trust gate, content-scan
  sensitivity, and the hold-queue mechanism (TTL + expiration action). Setting the
  outbound gate to `review` (or enabling the scan) is what turns on HITL holds.
  Account scope only. Beta — shape may change before it is declared stable.
- `POST /v1/agents/{email}/test` — send a platform test email to the agent's own
  address to confirm inbound delivery.

### Messages (`/v1/agents/{email}/messages`)

The message surface is agent-scoped: the agent in the path is the sender (there is
no `from` field). `reply`, `forward`, and `attachments` are sub-resources of a
single message.

- `GET …/messages` — list inbound + outbound with filters (`direction`,
  `read_status`, `sort`, `from`, `subject_contains`, `conversation_id`, `labels`,
  `since`, `until`) and cursor pagination. Held outbound drafts appear with
  `status=pending_review`.
- `POST …/messages` — send a new email (a new thread). Returns `202 Accepted` for
  every non-terminal outcome — `pending_review` when the agent's protection policy
  holds it for review, or `accepted` when the async pipeline durably queues it —
  and `200 OK` for the terminal-synchronous `sent`. The send result
  `status` is an open set — known values `accepted | sent | pending_review |
  review_approved | failed`. **Always branch on `status`, not the HTTP code.**
  `accepted` (async pipeline) means the message is durably persisted and queued;
  the terminal outcome then arrives via the `email.sent` / `email.failed` webhook
  events or `GET …/messages/{id}`. `provider_message_id` is absent until the
  message is actually sent. Optional `?wait=sent` holds the request until the
  message reaches a terminal-or-held state or a bounded timeout (a synchronous
  server treats it as a no-op).
- **`delivery_status`** on a message follows `accepted → sending → sent →
  delivered | deferred | bounced | complained | failed`. Note **`sent` ≠
  `delivered`**: `sent` means the upstream provider (SES) accepted the message,
  not that the recipient's server did. Delivery/bounce/complaint are per-recipient
  async outcomes reported later via SNS and the corresponding webhook events.
- `GET …/messages/{id}` — fetch one message (inbound or outbound), including the
  raw message and inbound auth headers. Reading an unread inbound message flips it
  to `read`.
- `PATCH …/messages/{id}` — apply a labels delta (`add_labels` / `remove_labels`).
- `POST …/messages/{id}/reply`, `POST …/messages/{id}/forward` — reply to /
  forward a message; `202` when held for review.
- `GET …/messages/{id}/attachments/{index}` — attachment metadata + a short-lived
  `download_url` (so binary bytes never stream through an agent's context);
  `?inline=true` returns base64 `data` for small attachments.

**Outbound attachment limits** (send / reply / forward, enforced on the **decoded**
bytes — not the base64 wire size): at most **10 attachments** per message, each
**≤ 10 MB**, and **≤ 25 MB combined**. Too many attachments → `400 invalid_request`;
an attachment or combined total over its size limit → `413 payload_too_large` (the
limit and offending size are in `error.details`). On `forward`, the limits apply to
the **combined** set — the original message's carried-over attachments plus any you
supply. These are conservative starting limits and may be raised over time.

**Byte semantics** — the field name `size_bytes` appears at two levels with two
different meanings:

- **On a message** (`MessageView` / `MessageSummaryView` / the export's
  `Message`): the **raw MIME byte length** of the whole stored message —
  headers + bodies + attachments *as transported* (i.e. base64-encoded
  attachment parts count at their encoded size). It is the octet length of
  `raw_message`.
- **On an attachment** (`attachments[]` on a message, the attachment endpoint,
  and the `email.received` event's attachment metadata): the **decoded payload
  size** — the byte count of the file after the Content-Transfer-Encoding is
  undone; exactly what `download_url` serves, what the 256 KB `?inline` cap is
  checked against, and what the outbound attachment limits above are enforced
  on. Because base64 inflates by ~4/3, a message's `size_bytes` is expected to
  exceed the sum of its attachments' `size_bytes` plus body text.
- **Storage-quota accounting** (`usage.storage_bytes` vs
  `limits.max_storage_bytes` on `GET /v1/account`): per stored message, the
  raw MIME length (the message-level `size_bytes`) **plus** any retained
  held-draft body/attachment columns (these exist only while a message is
  `pending_review` and are scrubbed on terminal transitions) — so for
  sent/inbound messages, storage usage is exactly the sum of their
  `size_bytes`.

**Outbound composed-message ceiling** (send / reply / forward and an outbound
HITL approval after merging reviewer overrides): **10 MiB (10,485,760 bytes)**,
measured as the UTF-8 byte lengths of `subject + text + html` plus the **decoded**
attachment bytes. This is independent of the larger 25 MB aggregate attachment
allowance: a request can satisfy every attachment limit and still exceed the
composed ceiling once its subject and bodies are included. A breach returns
`413 payload_too_large`. Direct send/reply/forward errors include
`error.details.composed_bytes` and `error.details.max_composed_bytes`
(`10485760`); callers should treat `error.details` as optional on other paths.

> **Note:** approve/reject a held message via the account-scoped **Reviews**
> queue below (`POST /v1/reviews/{id}/approve|reject`), which addresses holds by
> id with no inbox email needed. (The older per-message
> `POST …/messages/{id}/approve|reject` endpoints were removed in the pre-GA
> vocabulary freeze.)

### Conversations (`/v1/agents/{email}/conversations`)

Threads derived from `messages.conversation_id`.

- `GET …/conversations` — list threads (`since`/`until`, cursor).
- `GET …/conversations/{id}` — one thread with participants, labels, and member
  messages.

### Reviews (`/v1/reviews`)

The unified review queue: every message held in `pending_review` across the
account's inboxes — outbound drafts awaiting send approval **and** inbound
messages held by a screening gate. **Account-scoped credentials only**; an agent
cannot see or resolve its own holds (self-approval would defeat the gate).

- `GET /v1/reviews`, `GET /v1/reviews/{id}` — list the queue / full detail of one
  held message.
- `POST /v1/reviews/{id}/approve` — branches on direction: an outbound draft is
  sent via SES (honors `Idempotency-Key` + optional reviewer overrides); an
  inbound hold is released to the inbox. Returns `202 Accepted` with
  `status=accepted` when the outbound delivery is durably queued for async
  submission; synchronous `sent` and inbound `review_approved` outcomes return
  `200 OK`.
- `POST /v1/reviews/{id}/reject` — outbound draft discarded (never sent); inbound
  hold dropped (never reaches the agent; payload retained, hidden, for forensics).

### Webhooks (`/v1/webhooks`)

Webhook subscribers (the delivery side of the event log). Each webhook carries its
own **per-webhook signing secret** that signs the payloads sent to it.

- `GET /v1/webhooks`, `POST /v1/webhooks` — list / create (the secret is returned
  once, at creation).
- `GET/PATCH /v1/webhooks/{id}`, `DELETE /v1/webhooks/{id}?confirm=DELETE` —
  fetch / partial-update (`url`/`events`/`filters` are full-replace when present)
  / delete.
- `POST /v1/webhooks/{id}/rotate-secret` — mint a new secret; the previous one
  stays valid for a 24h grace window.
- `GET /v1/webhooks/{id}/deliveries` — the per-webhook delivery log (debug view).
- `POST /v1/webhooks/{id}/test` — fire a one-off synthetic delivery.

To verify an inbound webhook payload, pass the webhook's signing secret to the SDK
helper — `construct_event(body, header, secret)` /
`constructEvent(body, header, secret)` does parse + verify in one call. See the
[Python](../sdks/python/README.md#quick-start) and
[TypeScript](../sdks/typescript/README.md#verify-a-webhook) SDK READMEs.

<a id="webhook-signing-secrets"></a>
> **Signing.** Webhook deliveries are signed per-webhook with the `whsec_`
> secret (rotatable via the `rotate-secret` route above). The relay's
> `X-E2A-Auth-*` headers and the HITL approval / magic-link tokens are signed by
> the deployment-wide HMAC secret (`E2A_HMAC_SECRET`), its sole signer.

### Events (`/v1/events`)

The durable, queryable log of every event e2a emits to webhook subscribers
(30-day retention), and the source of truth for replay. See
[events.md](events.md) for the event taxonomy, reconciliation pattern, and replay
semantics.

- `GET /v1/events` — filter by `type`/`agent_email`/`conversation_id`/`message_id`
  and time range; cursor pagination.
- `GET /v1/events/{id}` — one event (returns `410 Gone` past retention).
- `POST /v1/events/{id}/redeliver` — re-enqueue delivery for an event (to one
  webhook or all originally-matched). Returns `202 Accepted`: the redelivery is
  durably enqueued for async submission (per-delivery `status: pending`, or
  `scheduled` for the fan-out), not delivered synchronously. Receivers must dedup
  on event id.

## Real-time delivery (WebSocket)

`GET /v1/agents/{email}/ws` — WebSocket for real-time inbound. Authenticated by
the `Authorization: Bearer <api_key>` handshake header (the credential never
appears in the URL). Not part of the OpenAPI document (it is not an HTTP
request/response operation).

The server pushes the SAME versioned event envelope a webhook delivery
carries — `{type, id, schema_version, created_at, data}` with the
`email.received` payload (`EmailReceivedData`; see
[events.md](events.md#envelope-and-typed-payloads)) — so one parser serves
both channels, and the event `id` (identical across channels for the same
event) lets a consumer dedup WS-vs-webhook. Tolerate unknown `type` values:
future WS event kinds arrive in the same envelope. Metadata only; fetch full
content via `GET /v1/agents/{email}/messages/{id}`:

```json
{
  "type": "email.received",
  "id": "evt_62eb7644b075459043c358bc6448d754",
  "schema_version": "1",
  "created_at": "2026-04-24T10:00:00.123456789Z",
  "data": {
    "message_id": "msg_abc123",
    "agent_email": "bot@your-domain.com",
    "direction": "inbound",
    "conversation_id": "conv_xyz",
    "from": "alice@example.com",
    "authenticated_from": "alice@example.com",
    "to": ["bot@your-domain.com"],
    "delivered_to": "bot@your-domain.com",
    "subject": "Meeting tomorrow",
    "auth_headers": {},
    "received_at": "2026-04-24T10:00:00.123456789Z"
  }
}
```

On connect, all unread messages are drained as `email.received` events
automatically. Live events carry the same marshaled event envelope as the
webhook delivery — identical fields and event id; byte layout may differ
(JSON key order/escaping is not contractual). The drain-on-reconnect rebuild
carries the full auth contract (`authenticated_from` + the signed
`auth_headers`, persisted with the message) and diverges from the original
event in exactly two ways: `attachments` is omitted (the raw message is not
loaded by the drain query), and `created_at`/`received_at` are the message
row's time rather than the original event's publish time — the full message
(fetched separately) always has everything.

### Connection lifecycle & close codes

**One connection per agent.** The server holds at most one WebSocket per
agent: when a newer connection for the same agent completes its handshake,
it wins, and the older connection is closed with code **4000 `replaced`**.
WS is an opportunistic push channel on top of the durable pollable inbox and
webhook subscriptions — if you need fan-out to several consumers, use
webhooks (or poll), not multiple sockets.

Close codes are a frozen part of the API contract. Standard codes keep their
standard semantics; e2a-specific conditions use application codes in the
4000–4999 range:

| Code | Reason token | Meaning | Client action |
|---|---|---|---|
| `1000` | *(empty)* | Normal closure. | None — expected after your own close. |
| `1001` | `shutting_down` | Server shutdown/restart (e.g. a deploy). | Reconnect with backoff. |
| `1001` | `ping_timeout` | The server dropped an unresponsive connection (missed keepalive pong). Usually observed as a `1006` abnormal close instead, since the peer is already gone. | Reconnect with backoff. |
| `1006` | *(n/a — never sent; synthesized locally)* | Abnormal close / network drop. | Reconnect with backoff. |
| `1008` | *(human-readable message)* | Genuine policy rejection of an **established** connection. Reserved: the server does not currently close established connections with 1008 — all credential/ownership rejections happen at the handshake as HTTP errors (below). | Do **not** reconnect — retrying the same connection cannot succeed. |
| `1011` | *(human-readable message)* | Internal server error. | Reconnect with backoff. |
| `4000` | `replaced` | A **newer connection for this agent** superseded this one (one-connection-per-agent). Benign — but the superseded client must stop: auto-reconnecting would steal the socket back from its replacement and loop. | Do **not** reconnect. Surface the condition (SDKs raise/emit `E2AConnectionReplacedError`). |
| `4001`–`4999` | — | Reserved for future e2a-specific terminal conditions (e.g. agent deleted mid-connection). | Treat any unrecognized 4xxx as terminal: do **not** auto-reconnect. |

Reason strings on e2a-specific closes are short stable `snake_case` tokens
(`replaced`, `shutting_down`, `ping_timeout`) — safe to branch on, though
clients should branch on the **code** first; reasons on standard codes may be
human-readable text.

Handshake rejections (missing/invalid key → `401`, agent-scoped key for a
different agent → `403`, nonexistent or not-your agent → `404`) happen
**before** the upgrade and return the canonical HTTP error envelope, never a
close code. The SDKs treat those as fatal too (typed error, no retry loop).

SDK behavior (TS `WSListener`/`WSStream`, Python `WSStream`, and the CLI
`listen` command, which inherits from the TS SDK): transient closes reconnect
with exponential backoff; `4000 replaced`, `1008`, unknown 4xxx, and fatal
handshake rejections stop the stream with a typed error. The CLI prints a
`listener replaced` explanation and exits `5` (permanent — retry wrappers
must not rerun it).

## HITL magic links

These accept a signed `t` query parameter (from notification emails) instead of an
API key, so a reviewer can approve/reject from any mail client without auth. They
live under `/v1` because the paths are the literal links embedded in notification
emails (not part of the OpenAPI document):

- `GET`/`POST` `/v1/approve?t=…` — approve a held message via signed token.
- `GET`/`POST` `/v1/reject?t=…` — reject a held message via signed token.

## Meta / unauthenticated

- `GET /v1/info` — public deployment discovery: `shared_domain`,
  `slug_registration_enabled`, `public_url`, `version`. CLIs/SDKs hit this to
  self-configure from a single base URL.
- `GET /api/health` — health check.
- `POST /api/feedback` — submit feedback (rate-limited per-IP).

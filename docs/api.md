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
- **Idempotency.** Mutating send/approve/rotate operations honor an
  `Idempotency-Key` header. See the spec for which operations accept it.
- **Errors.** Non-2xx responses use a single `ErrorEnvelope` shape; branch on
  `error.code`.

## Versioning & stability

The `/v1` surface is the **stable, generally-available contract** as of e2a 1.0.
Our commitment, and what you can rely on:

- **No breaking changes within `/v1`.** We will not remove an endpoint, remove a
  response field, rename anything, tighten a type, or change documented semantics
  under `/v1`. A breaking change means a new major version path (`/v2`), and the
  two would run side by side during a published migration window.
- **Additive changes can happen anytime** and are *not* breaking: new endpoints,
  new optional request fields, and **new response fields**. Clients must ignore
  fields they don't recognize.
- **Enums in responses are open.** Treat any `type` / `*_status` / `event_type`
  value as an open string set: we may introduce new values (e.g. a new event
  type or delivery state) without a major bump, so a client **must not crash on
  an unknown value** — handle it as a default/passthrough case. (The official
  SDKs already do this.) Enum values you *send* in requests are validated and
  rejected if unknown — that's intentional and not a stability concern.
- **Version discovery.** `GET /v1/info` reports the running API version (and
  deployment flags such as whether shared-domain slug registration is enabled),
  so clients can adapt instead of hard-coding assumptions.
- **Deprecation & sunset.** If we ever need to wind something down, it stays
  functional and is marked `deprecated` in the OpenAPI spec; we will not remove
  it within `/v1`. Endpoints currently marked deprecated (the agent-path
  `…/messages/{id}/approve|reject`, superseded by `/v1/reviews/{id}/approve|reject`)
  keep working for the life of `/v1`.

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
- `GET/POST /v1/account/api-keys`, `DELETE /v1/account/api-keys/{id}` — mint
  (plaintext shown once), list (metadata only), and revoke API keys. Account
  scope only.
- `GET /v1/account/suppressions`, `DELETE /v1/account/suppressions/{address}` —
  the recipient suppression list (auto-added on hard bounce/complaint; sends to a
  suppressed address fail with `recipient_suppressed`). Delete to un-suppress.

### Domains (`/v1/domains`)

Custom sending/receiving domains and their DNS verification.

- `GET /v1/domains`, `POST /v1/domains` — list / register (returns required MX +
  TXT records and the DKIM selector/key).
- `GET/DELETE /v1/domains/{domain}` — fetch / delete.
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
- `POST …/messages` — send a new email (a new thread). `202` + `pending_review`
  when the agent's protection policy holds it for review. The send result
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

> **Note:** the older per-message
> `POST …/messages/{id}/approve` and `…/reject` endpoints still exist for
> back-compat but are **deprecated** — use the account-scoped **Reviews** queue
> below, which addresses holds by id with no inbox email needed.

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
  inbound hold is released to the inbox.
- `POST /v1/reviews/{id}/reject` — outbound draft discarded (never sent); inbound
  hold dropped (never reaches the agent; payload retained, hidden, for forensics).

### Webhooks (`/v1/webhooks`)

Webhook subscribers (the delivery side of the event log). Each webhook carries its
own **per-webhook signing secret** that signs the payloads sent to it.

- `GET /v1/webhooks`, `POST /v1/webhooks` — list / create (the secret is returned
  once, at creation).
- `GET/PATCH/DELETE /v1/webhooks/{id}` — fetch / partial-update
  (`url`/`events`/`filters` are full-replace when present) / delete.
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
  webhook or all originally-matched). Receivers must dedup on event id.

## Real-time delivery (WebSocket)

`GET /v1/agents/{email}/ws` — WebSocket for real-time inbound. Authenticated by
the `Authorization: Bearer <api_key>` handshake header (the credential never
appears in the URL). Not part of the OpenAPI document (it is not an HTTP
request/response operation).

The server pushes lightweight JSON notifications (metadata only); fetch full
content via `GET /v1/agents/{email}/messages/{id}`:

```json
{
  "message_id": "msg_abc123",
  "conversation_id": "conv_xyz",
  "from": "alice@example.com",
  "delivered_to": "bot@your-domain.com",
  "subject": "Meeting tomorrow",
  "received_at": "2026-04-24T10:00:00Z"
}
```

On connect, all unread messages are drained as notifications automatically. The
full message payload (fetched separately) includes the parsed `to`, `cc`, and
`reply_to` header lists; the lightweight notification omits them since the agent
fetches the body anyway.

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

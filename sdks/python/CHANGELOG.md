# Changelog

## 5.0.0

Breaking: the async client class was renamed, and the freed name now ships a
synchronous client.

### Changed
- **`E2AClient` → `AsyncE2AClient`.** The 4.x client was async-only, but its
  name inverted the Python-ecosystem convention (httpx, openai, anthropic:
  plain name = sync client, `Async*` prefix = async client) and squatted the
  name the synchronous client needs. The class, exports (`e2a` and `e2a.v1`),
  docs, and examples all now use `AsyncE2AClient`; its behavior is unchanged.
  Migration from 4.x is mechanical: `from e2a.v1 import AsyncE2AClient`.

### Added
- **`E2AClient` — the synchronous client** — under the name the rename freed
  (there is deliberately no compatibility alias to the async client). It is a
  facade over `AsyncE2AClient`: a background daemon thread runs an event loop
  for the client's lifetime and every call bridges the corresponding async
  coroutine onto it, so there is exactly one implementation of
  resources/retries/typed errors/pagination and the two surfaces cannot drift.
  - Identical constructor (`api_key`, `base_url`, `max_retries`,
    `max_elapsed_ms`, `timeout_ms`) and resource tree; typed `E2AError`
    subclasses propagate unwrapped, so `except E2ALimitExceededError:` works
    the same as in async code.
  - List endpoints return a **sync pager**: plain `for` iteration, plus
    `page(cursor)` / `to_list(limit=N)` / `for_each(fn)`.
  - `client.listen(address)` returns a plain iterable of `WSNotification`;
    `close()` from another thread unblocks a pending iteration cleanly.
  - Lifecycle: use as a context manager or call `close()` (idempotent). An
    unclosed client is cleaned up at GC/interpreter exit and cannot hang
    shutdown.
  - **Async-context guard:** calling any sync method while an event loop is
    running in the current thread raises a guiding `RuntimeError`
    ("use AsyncE2AClient") instead of blocking the loop. 4.x code that still
    imports `E2AClient` now gets the sync client — update those imports to
    `AsyncE2AClient`.
## 4.3.0

### Breaking (pre-GA)
- **`AgentIdentity.webhook_healthy` (bool) replaced by `AgentIdentity.webhook_status`
  (optional string enum).** The bool could not distinguish "no webhook
  configured" from "healthy". The new field is an open set — tolerate unknown
  values. Known values: `none` (no webhook matches the agent), `healthy` (an
  enabled matching webhook, no terminally-failed delivery in the last 24h),
  `failing` (an enabled matching webhook had a terminally-failed delivery in
  the last 24h), `disabled` (matching webhooks exist but all are manually
  disabled), `auto_disabled` (all matching webhooks disabled, at least one by
  the chronic-failure sweep). `AgentIdentity` only appears in the account
  export (`account.export()`), so most integrations are unaffected.

## 4.2.0

Additive, no breaking changes.

### Fixed
- **`templates.list()` / `templates.list_starters()` silently truncated to the
  first page.** Both ignored the server's `next_cursor` and stopped after one
  request, dropping every template/starter past page one. They now thread the
  cursor and auto-page to completion like every other `.list()` (TS SDK
  parity), and accept a `limit=` per-page size.

### Added
- **`AutoPager.page(cursor=None)`** — the manual, caller-driven pagination
  primitive (parity with the TS SDK's `pager.page()`): fetch a SINGLE page and
  get back a `Page` of `items` + `next_cursor`. Pass the previous page's
  `next_cursor` to resume (e.g. checkpoint/restart from a queue); a `None`
  `next_cursor` in the result means there are no more pages. The page size is
  the `limit` baked into the list call that produced the pager.

## 4.1.0

Additive, no breaking changes.

### Added
- **`E2ALimitExceededError`** — the typed error for a `402 limit_exceeded`
  response (a per-account **quota** cap: monthly messages, storage, agent/domain
  counts). It is **not** retryable. This completes the permanent GA split with
  `E2ARateLimitError` (`429 rate_limited`, a request-**rate**/throughput limit,
  which **is** retryable): branch on the exception type (equivalently the HTTP
  status) — `402` → surface a quota/upgrade path, `429` → back off
  `retry_after_seconds` and retry. A `402` previously surfaced as the base
  `E2AError`; it now surfaces as this subclass (still an `E2AError`, so existing
  `except E2AError` handlers are unaffected).

Additive, no breaking changes.

### Added
- `email.received` is a metadata-only notification; `webhooks.fetch_message(event)`
  + the `EmailReceivedPayload` type fetch the full message (body + attachments)
  on demand (#321).
- Per-axis SES sending status surfaced on the domain/sending types (#309).
- DKIM verification support (#312).

## 4.0.0

Breaking: the domain DNS-records shape changed (server #304).

### Changed
- **`DomainView.dns_records` is now a single purpose-tagged array
  (`list[DNSRecord]`).** Each record carries `type`, `name`, `value`,
  `priority`, `purpose`, and `status`. Replaces the old
  `dns_records.{ mx, txt, dkim }` object and the separate `sending_dns_records`
  list. Address records by `purpose` (`ownership`, `inbound_mx`, `dkim`,
  `mail_from_mx`, `mail_from_spf`) rather than `dns_records.mx`/`.txt`/`.dkim`.
  The MAIL FROM records live in the same list (returned at registration when the
  sending feature is enabled), and each record now has a per-record `status`
  (`verified`/`pending`/`missing`/`failed`). `purpose` and `status` are open
  sets — tolerate unknown values.

## 3.0.0

Breaking redesign. The SDK is now a namespaced, **async-only** `E2AClient`
wrapping a generated client over the agent-scoped `/v1` API surface, with a
typed error hierarchy, automatic retries + idempotency, and async
auto-pagination.

### Changed
- **Namespaced, async-only surface.** Resources are grouped under the client:
  `client.agents`, `client.messages`, `client.conversations`, `client.domains`,
  `client.events`, `client.webhooks`, `client.account`. Per-agent methods take
  the agent `address` as the first argument
  (`await client.messages.send(address, {...})`,
  `await client.messages.list(address).to_list(limit=...)`,
  `await client.messages.get(address, id)`,
  `await client.messages.reply(address, id, {...})`). Use the client as an async
  context manager (`async with E2AClient() as client:`).
- **Webhook verification.** Verify and decode a delivery with the standalone
  `construct_event(raw_body, signature_header, secret)`, which checks the
  `X-E2A-Signature` header and returns a typed event (raising
  `E2AWebhookSignatureError` on a bad signature). Per-webhook `whsec_…` secrets,
  Stripe-style.
- **Typed errors.** Failures raise `E2AError` subclasses (`E2ANotFoundError`,
  `E2AConflictError`, `E2AValidationError`, `E2ARateLimitError`,
  `E2AWebhookSignatureError`, …) carrying `.code`, `.status`, `.request_id`, and
  `.retryable`.

### Removed
- The flat methods `send` / `reply` / `get_messages` / `get_message` and the
  per-call `agent_email` inference. Pass the agent `address` explicitly.
- The lower-level `E2AApi` class.
- The synchronous client — the SDK is async-only.
- `InboundEmail` / `AsyncInboundEmail` and the `parse_webhook` / `parse` +
  `verify_signature()` flow. Replaced by `construct_event`. There is no
  unverified-email type and no field-access gating.

## 2.5.0

### Added
- Generated types for the per-user resource-limits primitive that
  shipped with #158: `LimitsInfo`, `LimitsCaps`, `LimitsUsage`. These
  describe the response shape of `GET /api/v1/users/me/limits`, which
  the hosted dashboard uses to render the upgrade affordance and the
  "you've used X of Y" surface. The high-level `E2AClient` doesn't
  yet expose a typed helper for this endpoint — it's surfaced as a
  dashboard-only concern today, and SDK consumers querying their own
  usage should call `/agents` / `/messages` directly. The types are
  emitted so anyone consuming the raw OpenAPI generation has the
  shapes available.

### Notes
- No runtime client behavior changed in this release. If you're not
  using the limits primitive (self-host deployments without a paid
  tier), 2.5.0 is functionally identical to 2.4.0.

## 2.4.0

### Added
- `idempotency_key` parameter on `E2AClient.approve_message()` and its
  async counterpart (and on the lower-level `E2AApi.approve_message()`).
  Approve fires a real SES send, so without a stable key a retry after
  a transient failure could double-send. When supplied it's threaded
  through as the `Idempotency-Key` header; when omitted the SDK mints
  a fresh UUIDv4 per call — that gives network-layer retry safety only.
  Supply a stable key derived from the review event (typically the
  pending `message_id`) to dedupe across an explicit retry loop.
- `sort`, `from_`, `subject_contains`, `conversation_id`, `since`,
  `until` kwargs on `E2AApi.list_messages()` and the high-level
  `E2AClient.get_messages()` (sync + async). `sort` defaults
  server-side to newest-first; pass `"asc"` for FIFO polling. The
  substring filters are case-insensitive and capped at 200 chars
  server-side. `since` / `until` accept RFC3339 timestamps and
  bracket `created_at`. Filter values are encoded into `next_token`,
  so continuation requests must keep the same filter values.

### Changed
- **Default sort flipped to newest-first** on `GET /messages`. Prior
  releases silently returned oldest-first for `direction=inbound` (the
  SDK default) and newest-first for `direction=all`. A polling agent
  that relied on FIFO drain order should now pass `sort="asc"` to
  preserve the old behavior.
- `agent_mode` is now a required field on `RegisterAgentRequest`. The
  server previously silently defaulted to `"cloud"` and then 400'd
  with a cryptic "webhook_url is required" message; it now explicitly
  rejects requests missing `agent_mode` with a clear error. Pydantic
  v2 will raise a validation error if you instantiate the request
  without it. Set `agent_mode="local"` or `"cloud"` explicitly.

## 2.3.0

### Added
- `idempotency_key` parameter on `E2AClient.send()` / `.reply()` and their async
  counterparts (and on the lower-level `E2AApi.send_email()` /
  `reply_to_message()`). When supplied, it is sent as the `Idempotency-Key`
  header so the server can deduplicate retries of the same send/reply. When
  omitted, the SDK generates a fresh UUIDv4 per call — that gives
  network-layer retry safety only; supply a stable key derived from the
  triggering event (e.g. the inbound message id or a job id) to deduplicate
  across an explicit retry loop.
- `InboundEmail.reply_to` and `AsyncInboundEmail.reply_to` (`list[str]`) — the
  parsed `Reply-To:` header from the inbound message, surfaced as a first-class
  field so consumers no longer need to re-parse `raw_message` with stdlib
  `email.message_from_bytes()`. Empty list when the header is absent; the SDK
  never silently falls back to `sender`. Use this when the sender is a no-reply
  notifications mailbox (Granola, GitHub, CI bots) and you need the actual
  correspondent.
- `MessageSummary.reply_to` (`list[str]`) on the REST polling path — the list
  endpoint now mirrors the same field.
- `reply_to` added to `unverified_payload` for forensic inspection without
  unlocking gated access.

### Reply-To trust path (decision)
`reply_to` is trusted on the same terms as `to`, `cc`, `recipient`,
`subject`, and the body fields: the e2a server parses it from
`raw_message`, places it in the JSON envelope, and TLS protects the wire
to your webhook URL. Treat the field as trustworthy once
`verify_signature()` succeeds **and** you're confident in your
relay-to-webhook connection (or via `client.get_message(...)`, which uses
the authenticated REST channel).

**What `verify_signature()` does not prove:** the HMAC binds a fixed set
of auth headers and `body_hash = SHA-256(raw_message)`. It does not sign
the JSON envelope itself, and the SDK reads `reply_to`, `to`, `cc`, etc.
from that envelope rather than re-parsing `raw_message`. So an attacker
who can modify the JSON wrapping after signing — but cannot modify
`raw_message` or the signed headers — can rewrite `reply_to` and the
HMAC will still verify. TLS to your webhook URL is the actual integrity
layer for the envelope fields; the HMAC is defense-in-depth for proven
origin and covers the body bytes. If you need byte-exact assurance for a
specific field, re-parse it from `raw_message` (whose integrity
`body_hash` *does* cover).

**Also not guaranteed:** upstream-DKIM coverage of `Reply-To:`. If the
original sender's DKIM signature did not sign `Reply-To` (whether
because they didn't sign it, or there was no DKIM at all), a MITM
between sender and e2a could have rewritten the header before it reached
the relay. e2a does not re-verify or surface per-header DKIM coverage
today — the `Authentication-Results` / SPF/DKIM surface is unchanged.
For routing decisions where attacker-controlled `Reply-To` would matter,
also confirm `email.is_verified` and that the sender's domain is one you
expect.

We chose to keep `reply_to` populated whenever it's present (rather than
masking it on partially-trusted messages or exposing a `reply_to_signed`
flag) so the field shape stays uniform with `to`/`cc` and consumers can
make their own policy decision. The trust model is documented on the
property docstring.

### Wire change
The webhook payload schema now includes an optional `reply_to: string[]`
field. Existing consumers that ignore unknown fields are unaffected; older
SDK versions parsing the same payload continue to work and simply do not
see the new key.

### Other generated-type additions
The high-level surface above is what most consumers will touch. For users
of `client.api.*` or `e2a.v1.generated.*` directly, the following backend
endpoints / fields also landed since 2.2.0 and are reflected in the
regenerated types:

- Per-record DNS verification — separate MX / SPF / DKIM diagnostic
  responses on the domain-verification endpoints.
- Enriched `DashboardAgent` — `Inbound7d`, `Outbound7d`, `Pending`,
  `LastDelivery`, `WebhookHealthy` fields on the dashboard list.
- OAuth 2.1 authorization-server endpoints (fosite-backed) used by the
  MCP server flow.
- Per-domain DKIM key generation endpoint.
- One-time signing-secret reveal on creation.
- Pending-review polish — provenance, quoted-inbound, headers-preview,
  draft-footer fields on the review payload.

These are additive and don't break existing 2.2.0 callers.

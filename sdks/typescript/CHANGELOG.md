# Changelog

## 5.0.0

Breaking: the WebSocket frame is now the versioned event envelope (server #456).

### Changed
- **The WebSocket frame is the versioned event envelope** — the same
  `{type, id, schema_version, created_at, data}` shape a webhook delivery
  carries, so one parser (and one dedup key: the event `id`) serves both
  channels. Frames were previously a flat ad-hoc notification object.
- **`WSNotification` and the `"notification"` emitter event are removed.**
  Listen for `"event"` on `WSListener` (or iterate `client.listen(...)`)
  and use the new `WSEvent` type — an alias of `WebhookEvent`. Narrow with
  the type guards (e.g. `isEmailReceived(event)`) and read the payload from
  `event.data`.

### Added
- **Typed per-event payloads** for the nine stable event types
  (`EmailReceivedData`, `EmailSentData`, `EmailFailedData`,
  `EmailDeliveredData`, `EmailBouncedData`, `EmailComplainedData`,
  `DomainSendingVerifiedData`, `DomainSendingFailedData`,
  `DomainSuppressionAddedData`, plus `AttachmentMeta`) with narrowing guards
  (`isEmailReceived`, `isEmailSent`, …) shared by the webhook and WS
  channels. The shapes are locked to the server's committed golden fixtures.

## 4.2.0

### Breaking (pre-GA)
- **`AgentIdentity.webhookHealthy` (boolean) replaced by `AgentIdentity.webhookStatus`
  (optional string enum).** The bool could not distinguish "no webhook
  configured" from "healthy". The new field is an open set — tolerate unknown
  values. Known values: `none` (no webhook matches the agent), `healthy` (an
  enabled matching webhook, no terminally-failed delivery in the last 24h),
  `failing` (an enabled matching webhook had a terminally-failed delivery in
  the last 24h), `disabled` (matching webhooks exist but all are manually
  disabled), `auto_disabled` (all matching webhooks disabled, at least one by
  the chronic-failure sweep). `AgentIdentity` only appears in the account
  export (`account.export()`), so most integrations are unaffected.

## 4.1.0

Additive, no breaking changes.

### Added
- **`E2ALimitExceededError`** — the typed error for a `402 limit_exceeded`
  response (a per-account **quota** cap: monthly messages, storage, agent/domain
  counts). It is **not** retryable. This completes the permanent GA split with
  `E2ARateLimitError` (`429 rate_limited`, a request-**rate**/throughput limit,
  which **is** retryable): branch on the error subclass (equivalently the HTTP
  status) — `402` → surface a quota/upgrade path, `429` → back off
  `retryAfterSeconds` and retry. A `402` previously surfaced as the base
  `E2AError`; it now surfaces as this subclass (still an `E2AError` via
  `instanceof`, so existing catch-all handling is unaffected).

Additive, no breaking changes.

### Added
- `email.received` is a metadata-only notification; `webhooks.fetchMessage(event)`
  + the `EmailReceivedPayload` type fetch the full message (body + attachments)
  on demand (#321).
- Per-axis SES sending status surfaced on the domain/sending types (#309).
- DKIM verification support (#312).

## 4.0.0

Breaking: the domain DNS-records shape changed (server #304).

### Changed
- **`DomainView.dns_records` is now a single purpose-tagged array
  (`DNSRecord[]`).** Each record carries `type`, `name`, `value`, `priority`,
  `purpose`, and `status`. Replaces the old `dns_records.{ mx, txt, dkim }`
  object and the separate `sending_dns_records` array. Address records by
  `purpose` (`ownership`, `inbound_mx`, `dkim`, `mail_from_mx`, `mail_from_spf`)
  rather than `dns_records.mx`/`.txt`/`.dkim`. The MAIL FROM records live in the
  same array (returned at registration when the sending feature is enabled), and
  each record now has a per-record `status`
  (`verified`/`pending`/`missing`/`failed`). `purpose` and `status` are open
  sets — tolerate unknown values.

## 3.0.0

Breaking redesign. The SDK now wraps a generated `/v1` client behind a
namespaced, resource-oriented `E2AClient`, with a typed error hierarchy,
automatic retries + idempotency, and auto-pagination. Targets the e2a v1 API.

### Changed
- **Namespaced resources.** Flat methods are gone. Resources are grouped under
  the client: `client.agents`, `client.messages`, `client.conversations`,
  `client.domains`, `client.events`, `client.webhooks`, `client.account`.
  Per-agent methods take the agent `address` as the first argument
  (`client.messages.send(address, {...})`,
  `client.messages.list(address).toArray({ limit })`,
  `client.messages.get(address, id)`,
  `client.messages.reply(address, id, {...})`).
- **Webhook verification.** Verify and decode a delivery with the standalone
  `constructEvent(rawBody, signatureHeader, secret)`, which checks the
  `X-E2A-Signature` header and returns a typed `WebhookEvent` (throwing
  `E2AWebhookSignatureError` on a bad signature). Per-webhook `whsec_…` secrets,
  Stripe-style.
- **Typed errors.** Failures throw `E2AError` subclasses (`E2ANotFoundError`,
  `E2AConflictError`, `E2AValidationError`, `E2ARateLimitError`,
  `E2AWebhookSignatureError`, …) carrying `.code`, `.status`, `.requestId`, and
  `.retryable`.

### Removed
- The flat methods `getMessages` / `getMessage` / `send` / `reply` and the
  per-call address inference. Pass the agent `address` explicitly.
- `client.parse` / `client.parseWebhook` and `InboundEmail`. Replaced by
  `constructEvent`.

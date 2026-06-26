# Changelog

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

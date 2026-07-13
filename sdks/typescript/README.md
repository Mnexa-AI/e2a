# e2a TypeScript SDK

TypeScript/Node.js SDK for [e2a](https://e2a.dev) — email for AI agents.

## Install

```bash
npm install @e2a/sdk
```

The SDK major version tracks the SDK package's own breaking changes and is
independent of the API version path (`/v1`): SDK 4.x targets the e2a v1 API.

## Upgrading to 4.0

4.0 is a breaking change to the domain DNS-records shape (server #304).
`DomainView.dns_records` is now a single purpose-tagged `DNSRecord[]` array
instead of the old `dns_records.{ mx, txt, dkim }` object (and the separate
`sending_dns_records` array is gone). Each record carries `type`, `name`,
`value`, `priority`, `purpose`, and a per-record `status`. Address records by
`purpose` (`ownership`, `inbound_mx`, `dkim`, `mail_from_mx`, `mail_from_spf`)
rather than `dns_records.mx`/`.txt`/`.dkim` — the MAIL FROM records now live in
the same array. `purpose` and `status` are open sets, so tolerate unknown
values. No other public symbols changed.

## Upgrading from 2.x to 3.0

3.0 is a breaking redesign. The SDK now wraps a generated `/v1` client behind a
namespaced, resource-oriented surface, with a typed error hierarchy, automatic
retries + idempotency, and auto-pagination.

- **Namespaced resources.** Flat methods are gone. `client.getMessages()` →
  `client.messages.list(address)`, `client.getMessage(id)` →
  `client.messages.get(address, id)`, `client.send(...)` →
  `client.messages.send(address, body)`, etc. Per-agent calls take an explicit
  `address` — the SDK no longer infers it.
- **Webhook verification.** `client.parse` / `client.parseWebhook` /
  `InboundEmail` are removed. Verify and parse a delivery with the standalone
  `constructEvent(rawBody, header, secret)`, which returns a typed
  `WebhookEvent`. Signatures are per-webhook (`whsec_…`), Stripe-style.
- **Typed errors.** Failures throw `E2AError` subclasses
  (`E2ANotFoundError`, `E2AConflictError`, `E2AValidationError`,
  `E2ARateLimitError`, …) carrying `.code`, `.status`, `.requestId`, and
  `.retryable`.

```diff
- const { messages } = await client.getMessages({ status: "unread" });
- const email = await client.getMessage(messages[0].messageId);
- await email.reply("Thanks!");
+ const messages = await client.messages.list(address, { status: "unread" }).toArray({ limit: 50 });
+ await client.messages.reply(address, messages[0].messageId, { body: "Thanks!" });
```

## Quick Start

```typescript
import { E2AClient } from "@e2a/sdk/v1";

const client = new E2AClient(); // reads E2A_API_KEY; baseUrl defaults to https://api.e2a.dev
const address = "my-agent@agents.e2a.dev";
```

### Poll an inbox

```typescript
// List endpoints return an AutoPager: iterate, or collect with a required limit.
for await (const m of client.messages.list(address, { status: "unread" })) {
  const email = await client.messages.get(address, m.messageId);
  console.log(email.subject, email.body?.text);
  await client.messages.reply(address, m.messageId, { body: "Got it!" });
}
```

### Send mail

```typescript
await client.messages.send(address, {
  to: ["alice@example.com"],
  subject: "Hello",
  body: "Hi from my agent!",
  htmlBody: "<p>Hi!</p>",
  cc: ["carol@example.com"],
});
```

Unsafe writes (`send` / `reply` / `forward` / `approve`) auto-mint an
`Idempotency-Key` and reuse it across retries, so a network blip can't
double-send. Supply a stable key to also survive a process restart:

```typescript
await client.messages.send(address, body, { idempotencyKey: deriveFromEvent(evt) });
```

### Verify a webhook

Each subscription is signed with its own `whsec_…` secret. `constructEvent`
verifies the `X-E2A-Signature` header (replay-protected) and returns a typed
event in one call. **Pass the raw request body** — re-stringified JSON won't
match the signature.

```typescript
import { constructEvent, E2AWebhookSignatureError } from "@e2a/sdk/v1";

app.post("/webhook", express.raw({ type: "application/json" }), async (req, res) => {
  let event;
  try {
    event = constructEvent(req.body, req.header("X-E2A-Signature"), process.env.E2A_WEBHOOK_SECRET);
  } catch (e) {
    if (e instanceof E2AWebhookSignatureError) return res.status(400).end();
    throw e;
  }
  if (event.type === "email.received") {
    // metadata-only notification — fetch the full message (body + attachments)
    const msg = await client.webhooks.fetchMessage(event);
  }
  res.json({ ok: true });
});
```

During a secret rotation you can pass an array of secrets — a delivery is
accepted if any one matches: `constructEvent(body, header, [oldSecret, newSecret])`.

## Resources

`client.agents`, `client.messages`, `client.conversations`, `client.domains`,
`client.events`, `client.webhooks`, `client.account` (with
`client.account.suppressions`), plus `client.info()`. Each method maps to a
`/v1` operation; per-agent methods take the agent `address` as the first
argument.

### `new E2AClient(options?)`

| Option         | Type     | Default                 | Description                              |
|----------------|----------|-------------------------|------------------------------------------|
| `apiKey`       | `string` | `E2A_API_KEY` env       | Account (`e2a_acct_`) or agent key/token |
| `baseUrl`      | `string` | `https://api.e2a.dev`   | API base URL (override for self-host)    |
| `maxRetries`   | `number` | `2`                     | Retries on 429/5xx/connection            |
| `maxElapsedMs` | `number` | —                       | Optional total deadline across attempts  |
| `timeoutMs`    | `number` | `30000`                 | Per-attempt request timeout (see below)  |

`timeoutMs` bounds each individual attempt; a timed-out attempt is treated as a
retryable connection failure, so it composes with `maxRetries`/`maxElapsedMs`.
Setting `timeoutMs: 0` removes the SDK timeout entirely — a request is then
bounded only by the runtime's own `fetch` default (effectively unbounded in
Node). Note this differs from the Python SDK, where `timeout_ms=0` falls back to
the HTTP transport's built-in 300s ceiling rather than going unbounded.

### Errors

Every failure throws an `E2AError` (or subclass) with `.code` (the stable
machine code from the response envelope), `.status`, `.requestId`, and
`.retryable`. Subclasses: `E2AAuthError` (401), `E2APermissionError` (403),
`E2ANotFoundError` (404), `E2AConflictError` (409), `E2AValidationError` (422),
`E2AIdempotencyError`, `E2ALimitExceededError` (402 — a **quota** cap; not
retryable), `E2ARateLimitError` (429 — a request-**rate** limit; retryable after
`retryAfterSeconds`), `E2AServerError` (5xx), `E2AConnectionError` (no response),
`E2AWebhookSignatureError` (local verify failure). The 402/429 split is
permanent — branch on the subclass: 402 → surface a quota/upgrade path, 429 →
back off and retry.

> Note: e2a hides the existence of agents you don't own — `agents.get` of an
> unknown address returns `403` (`E2APermissionError`), not `404`.

### Pagination

List methods return an `AutoPager<T>` — an `AsyncIterable` that threads the
cursor for you. Use `for await`, or `.toArray({ limit })` (the limit is
required, to bound memory on a large inbox), or `.forEach(fn)` (return `false`
to stop early).

## WebSocket (real-time delivery for local agents)

Agents receive lightweight notifications over a WebSocket; auth is the
`Authorization: Bearer <api_key>` handshake header (the key never appears in the
URL) — no public URL needed.

```typescript
import { E2AClient, isEmailReceived } from "@e2a/sdk/v1";

const client = new E2AClient({ apiKey: "e2a_..." });

for await (const event of client.listen("bot@agents.e2a.dev")) {
  if (!isEmailReceived(event)) continue; // tolerate future event kinds
  // Lightweight metadata only — fetch the body when you want it.
  const email = await client.webhooks.fetchMessage(event);
  console.log(event.data.from, event.data.subject);
}
```

`client.listen(address)` (address falls back to `E2A_AGENT_EMAIL`) returns a
`WSStream` that is **both** an `AsyncIterable<WSEvent>` and an
`EventEmitter` — each item is the same versioned `{type, id, schema_version,
created_at, data}` envelope a webhook delivery carries, so
`client.webhooks.fetchMessage(event)` works on either channel. Use
`.on("error" | "close", …)` for connection-level events and `.close()` to
stop. Reconnects with exponential backoff (1s → 30s, configurable via
`maxBackoffMs`). The lower-level `WSListener` is also exported for advanced
use.

## Conversation threading

`conversationId` is an opaque string that ties multiple emails to one thread
across the email boundary. Pass it on any `send` / `reply` and e2a surfaces it
on the recipient's inbound — via `In-Reply-To` for humans, or a forge-resistant
`X-E2A-Conversation-Id` header for same-platform agent-to-agent mail. It is not
a security boundary; for sender identity check the message's `auth`. On first
contact from a human it arrives `null` — assign one yourself if you want to
thread.

## License

Apache-2.0 — see [LICENSE](https://github.com/Mnexa-AI/e2a/blob/main/LICENSE) and [NOTICE](https://github.com/Mnexa-AI/e2a/blob/main/NOTICE) in the upstream repo.

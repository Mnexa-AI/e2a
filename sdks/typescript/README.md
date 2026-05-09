# e2a TypeScript SDK

TypeScript/Node.js SDK for [e2a](https://e2a.dev) — email for AI agents.

## Install

```bash
npm install @e2a/sdk
```

## Upgrading from 1.x to 2.0

Webhook-parsed emails now refuse to expose claim fields (`sender`, `subject`, `textBody`, …) until the HMAC signature is verified — `email.sender` throws `UnverifiedEmailError` instead of silently returning attacker-controllable data. The one-line fix is to switch `client.parse(body)` → `client.parseWebhook(body)`:

```diff
- const email = await client.parse(req.body);
+ const email = await client.parseWebhook(req.body);
```

`parseWebhook` reads the secret from `E2A_WEBHOOK_SECRET`; set it before upgrading. If you must inspect the payload before verifying, use `email.unverifiedPayload`. REST-fetched emails (`client.getMessage`) are unaffected — they're pre-verified via the bearer token. Full background in the [PR](https://github.com/Mnexa-AI/e2a/pull/57).

## Quick Start

### Webhook (cloud agents)

Webhook payloads are HMAC-signed. The SDK gates field access behind verification — accessing `email.sender`, `email.subject`, etc. on an unverified payload throws `UnverifiedEmailError`. Use `client.parseWebhook(...)` to parse + verify in one call:

```typescript
import { E2AClient } from "@e2a/sdk";

const client = new E2AClient(); // uses E2A_API_KEY env var

app.post("/webhook", async (req, res) => {
  let email;
  try {
    email = await client.parseWebhook(req.body); // reads E2A_WEBHOOK_SECRET
  } catch {
    return res.status(401).end();
  }
  console.log(`From: ${email.sender}, Subject: ${email.subject}`);
  await email.reply("Got it!");
  res.json({ ok: true });
});
```

Get a signing secret from the dashboard's Settings → Webhook signing secrets (or `POST /api/v1/users/me/signing-secrets`). Set it as `E2A_WEBHOOK_SECRET` so `parseWebhook` picks it up automatically, or pass it explicitly: `client.parseWebhook(body, "whsec_...")`.

### Polling

```typescript
import { E2AClient } from "@e2a/sdk";

const client = new E2AClient({
  apiKey: "e2a_...",
  agentEmail: "my-agent@agents.e2a.dev",
});

// List unread messages
const { messages } = await client.getMessages({ status: "unread" });

// Read a specific message
const email = await client.getMessage(messages[0].messageId);
console.log(email.textBody);

// Reply
await email.reply("Thanks!");
```

### Send a new email

```typescript
await client.send(["alice@example.com"], "Hello", "Hi from my agent!");

// With CC, BCC, and HTML body
await client.send(["alice@example.com", "bob@example.com"], "Hello", "Hi!", {
  htmlBody: "<p>Hi!</p>",
  cc: ["carol@example.com"],
  bcc: ["dave@example.com"],
});
```

## API

### `new E2AClient(options?)`

| Option       | Type     | Default                | Description                    |
|-------------|----------|------------------------|--------------------------------|
| `apiKey`    | `string` | `E2A_API_KEY` env var  | Your API key                   |
| `agentEmail`| `string` | `E2A_AGENT_EMAIL` env  | Agent email address            |
| `baseUrl`   | `string` | `https://e2a.dev`      | API base URL                   |
| `timeout`   | `number` | `30000`                | Request timeout in ms          |

### `client.parseWebhook(body, secret?)` → `Promise<InboundEmail>`

Parse + HMAC-verify a webhook payload in one call. Reads `E2A_WEBHOOK_SECRET` if `secret` is omitted; throws on bad signature. Recommended entry point for webhook handlers.

### `client.parse(body)` → `Promise<InboundEmail>`

**Deprecated since 2.2 — will be removed in 3.0.** Use `parseWebhook` for webhook handlers, or call `parseWebhook` and read `email.unverifiedPayload` after the verification failure for inspection without verification. Calling `parse` logs a one-time deprecation warning to `console.warn`.

Parses a webhook payload (Buffer, string, or object) into an `InboundEmail` and returns it in the unverified state — accessing claim fields like `sender` or `subject` throws `UnverifiedEmailError` until `email.verifySignature()` succeeds.

### `client.getMessages(opts?)` → `Promise<MessageList>`

Fetch messages. Options: `status` ("unread", "read", "all"), `pageSize`, `token`.

### `client.getMessage(messageId)` → `Promise<InboundEmail>`

Fetch a single message with full content. Returns a pre-verified email (the bearer token already authenticated the channel) — no `verifySignature` step needed.

### `client.reply(messageId, body, opts?)` → `Promise<SendResult>`

Reply to a message. Options: `htmlBody`, `replyAll`, `cc`, `bcc`, `conversationId`, `attachments`.

### `client.send(to, subject, body, opts?)` → `Promise<SendResult>`

Send a new email. `to` is `string[]`. Options: `htmlBody`, `cc`, `bcc`, `conversationId`, `attachments`.

### `InboundEmail`

| Property         | Type              | Description                        |
|-----------------|-------------------|------------------------------------|
| `messageId`     | `string`          | Unique message ID                  |
| `conversationId`| `string \| null`  | Thread/conversation ID (see below) |
| `sender`        | `string`          | Sender email address               |
| `recipient`     | `string`          | Per-delivery target — your agent's address |
| `to`            | `string[]`        | Parsed `To:` header — every address from the original message |
| `cc`            | `string[]`        | Parsed `Cc:` header (empty when no CCs) |
| `subject`       | `string`          | Email subject                      |
| `textBody`      | `string`          | Plain-text body                    |
| `htmlBody`      | `string \| null`  | HTML body                          |
| `attachments`   | `Attachment[]`    | File attachments                   |
| `auth`          | `AuthHeaders`     | Authentication headers             |
| `isVerified`    | `boolean`         | Whether sender identity is verified|
| `unverifiedPayload` | `WebhookPayload` | Raw payload pre-verification — escape hatch for inspection; treat as untrusted |
| `reply(body)`   | method            | Reply to this email                |

All claim fields above (everything except `auth`, `rawMessage`, `verified`, `isVerified`, `unverifiedPayload`) are gated — accessing them on an unverified webhook payload throws `UnverifiedEmailError`. Call `email.verifySignature(secret?)` first (reads `E2A_WEBHOOK_SECRET` by default), or use `client.parseWebhook(body)` which combines parse + verify. `email.unverifiedPayload` is an escape hatch for inspection before verifying — treat its contents as untrusted.

Exported error class: `UnverifiedEmailError extends Error`.

## Conversation threading

`conversationId` is an opaque string that lets your agent tie multiple
emails to a single thread across the email boundary. Pass it on any
`send()` / `reply()`, and e2a surfaces it on the recipient's inbound —
whether the recipient is a human (via `In-Reply-To` threading) or another
e2a agent (via a custom `X-E2A-Conversation-Id` header, honored only for
same-platform mail so external senders cannot forge it).

```ts
client.on("message", async (email) => {
  const convId = email.conversationId ?? generateId();

  const reply = await buildReply(email);
  await email.reply(reply.text, {
    conversationId: convId,
    htmlBody: reply.html,
  });
});
```

### When is `conversationId` populated?

| Inbound type | Sender passed `conversationId`? | What you see |
|---|---|---|
| First email from a human | n/a — humans don't pass it | `null` — **assign one yourself** if you want to thread |
| Human reply to your prior outbound | n/a | The id you passed on your outbound |
| Another e2a agent's new send | **yes, recommended** | The sender's asserted id |
| Another e2a agent's new send | no | `null` |
| Another e2a agent's reply | either way | Your earlier outbound's id unless the sender asserted another |

Rules of thumb:

- **Always pass `conversationId`** when tagging an outbound as part of a
  known thread. It's the only way the recipient's webhook sees it.
- On first contact from a human, assign a new id yourself before replying.
- Handle `null` — it happens on first contact from humans and from external
  senders you haven't interacted with before.
- `conversationId` is not a security boundary. For sender identity, check
  `email.auth` / `email.isVerified`.

### Agent-to-agent threads

For e2a-to-e2a traffic, `conversationId` arrives on the very first message
with no prior round trip required:

```ts
await agentA.send(["bob@agent.acme.com"], "Can you handle this?", bodyText, {
  conversationId: "task-2026-04-19-7f3a",
});
// Agent B's webhook immediately sees conversationId="task-2026-04-19-7f3a"
```

## WebSocket (real-time delivery for local agents)

Local-mode agents can receive notifications in real time over a
WebSocket. No public URL needed; auth happens via the `?token=` query
parameter.

```ts
import { E2AClient } from "@e2a/sdk";

const client = new E2AClient({ apiKey: "e2a_..." });

for await (const notif of client.listen({ agentEmail: "bot@agents.e2a.dev" })) {
  // The notification is lightweight metadata only — no body, no REST call.
  console.log(`From: ${notif.from}, Subject: ${notif.subject}`);

  // Fetch the full email when you actually want it.
  const detail = await client.api.getMessage(notif.recipient, notif.message_id);
  // ...
}
```

`client.listen()` returns a [`WSStream`](src/v1/ws.ts) which is **both**
an `AsyncIterable<WSNotification>` and an `EventEmitter` — pick whichever
access pattern fits.

```ts
const stream = client.listen({ agentEmail: "bot@agents.e2a.dev" });
stream.on("error", (err) => console.error("WS error:", err));
stream.on("close", (code, reason) => console.log("WS closed:", code, reason));

for await (const notif of stream) {
  // ...
}

// Call stream.close() to terminate iteration cleanly.
```

`WSNotification` mirrors the Python SDK's dataclass and the server's
wire shape:

| Field | Type | Notes |
|---|---|---|
| `message_id` | `string` | Pass to `client.api.getMessage(...)` for the body |
| `from` | `string` | Sender |
| `recipient` | `string` | Per-delivery target (your agent's address) |
| `subject` | `string` | |
| `received_at` | `string` | RFC 3339 timestamp |
| `conversation_id` | `string?` | Threading; absent on first contact |

Reconnects with exponential backoff (1s → 30s by default,
configurable via `maxBackoffMs`). The protocol is server-to-client
only; the client never sends application frames.

### Lower-level `WSListener`

Prefer `client.listen()`. The underlying [`WSListener`](src/v1/ws.ts)
class is also exported for advanced use (e.g. wiring up a custom
EventEmitter pattern without iteration), but most consumers should use
`client.listen()`.

## License

Apache-2.0 — see [LICENSE](https://github.com/Mnexa-AI/e2a/blob/main/LICENSE) and [NOTICE](https://github.com/Mnexa-AI/e2a/blob/main/NOTICE) in the upstream repo.

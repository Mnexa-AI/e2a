# e2a TypeScript SDK

TypeScript/Node.js SDK for [e2a](https://e2a.dev) — email for AI agents.

## Install

```bash
npm install @e2a/sdk
```

## Quick Start

### Webhook (cloud agents)

```typescript
import { E2AClient } from "e2a";

const client = new E2AClient(); // uses E2A_API_KEY env var

app.post("/webhook", async (req, res) => {
  const email = await client.parse(req.body);
  console.log(`From: ${email.sender}, Subject: ${email.subject}`);
  await email.reply("Got it!");
  res.json({ ok: true });
});
```

### Polling

```typescript
import { E2AClient } from "e2a";

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

### `client.parse(body)` → `Promise<InboundEmail>`

Parse a webhook payload (Buffer, string, or object) into an `InboundEmail`.

### `client.getMessages(opts?)` → `Promise<MessageList>`

Fetch messages. Options: `status` ("unread", "read", "all"), `pageSize`, `token`.

### `client.getMessage(messageId)` → `Promise<InboundEmail>`

Fetch a single message with full content.

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
| `recipient`     | `string`          | Recipient (your agent) address     |
| `subject`       | `string`          | Email subject                      |
| `textBody`      | `string`          | Plain-text body                    |
| `htmlBody`      | `string \| null`  | HTML body                          |
| `attachments`   | `Attachment[]`    | File attachments                   |
| `auth`          | `AuthHeaders`     | Authentication headers             |
| `isVerified`    | `boolean`         | Whether sender identity is verified|
| `reply(body)`   | method            | Reply to this email                |

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

## License

MIT

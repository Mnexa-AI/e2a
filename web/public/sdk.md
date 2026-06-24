# sdk.md

Typed clients for the e2a REST API + webhook verification, for when you're
driving e2a from your own code (a webhook handler, a worker) rather than over
MCP. Two SDKs, same surface.

- **TypeScript** — `@e2a/sdk` (npm) · [README](https://github.com/Mnexa-AI/e2a/blob/main/sdks/typescript/README.md)
- **Python** — `e2a` (PyPI) · [README](https://github.com/Mnexa-AI/e2a/blob/main/sdks/python/README.md)

Prefer a typed client; if you're calling the REST API raw, see
[Raw REST](#raw-rest-without-an-sdk) below. The exhaustive contract is
https://e2a.dev/openapi.yaml, and the auth model (API key vs OAuth) is in
https://e2a.dev/auth.md.

## Install

```bash
npm install @e2a/sdk        # TypeScript / Node
pip install e2a             # Python (async)
```

## Quick start

### TypeScript

```ts
import { E2AClient } from "@e2a/sdk";

const client = new E2AClient({ apiKey: process.env.E2A_API_KEY });

// Send (held drafts come back status="pending_review", not an error)
await client.messages.send("bot@agents.e2a.dev", {
  to: ["person@example.com"],
  subject: "Hello from my agent",
  body: "This was sent by an AI agent via e2a.",
});

// Reply in-thread to an inbound message
await client.messages.reply("bot@agents.e2a.dev", messageId, {
  body: "Thanks — handled.",
});
```

### Python

```python
from e2a.v1 import E2AClient

async with E2AClient(api_key=os.environ["E2A_API_KEY"]) as client:
    await client.messages.send("bot@agents.e2a.dev", {
        "to": ["person@example.com"],
        "subject": "Hello from my agent",
        "body": "This was sent by an AI agent via e2a.",
    })

    await client.messages.reply("bot@agents.e2a.dev", message_id, {
        "body": "Thanks — handled.",
    })
```

## Receiving mail (webhook → fetch → reply)

The webhook delivery is a metadata trigger; fetch the parsed message by id, then
reply. Always **verify the signature** first — `construct_event` parses + checks
the HMAC and throws on a bad/forged/replayed delivery.

### TypeScript

```ts
import { constructEvent, E2AClient } from "@e2a/sdk";

// in your HTTP handler, with the RAW request body:
const event = constructEvent(rawBody, req.headers["x-e2a-signature"], WEBHOOK_SECRET);
if (event.type === "email.received") {
  const { recipient, message_id } = event.data as { recipient: string; message_id: string };
  const msg = await client.messages.get(recipient, message_id); // typed: from, subject, parsed.text, attachments
  // …decide a reply…
  await client.messages.reply(recipient, message_id, { body: "On it." });
}
```

### Python

```python
from e2a.v1 import construct_event, E2AWebhookSignatureError

try:
    event = construct_event(body, request.headers["X-E2A-Signature"], WEBHOOK_SECRET)
except E2AWebhookSignatureError:
    raise HTTPException(401, "bad signature")

if event.type == "email.received":
    data = event.data
    inbound = await client.messages.get(data["recipient"], data["message_id"])
    # inbound.var_from, inbound.subject, inbound.parsed.text
    await client.messages.reply(data["recipient"], data["message_id"], {"body": "On it."})
```

A full, runnable example (FastAPI + Google ADK agent, webhook → agent turn →
reply) is at
[examples/adk-cloud-webhook](https://github.com/Mnexa-AI/e2a/tree/main/examples/adk-cloud-webhook).

## Human-in-the-loop

Held messages (outbound drafts + screened inbound) are the account-scoped review
queue. With an account-scoped key:

```ts
const held = await client.reviews.list().toArray();   // both directions
await client.reviews.approve(id);                      // outbound: send; inbound: release
await client.reviews.reject(id, { reason: "spam" });
```

```python
held = await client.reviews.list().to_list()
await client.reviews.approve(message_id)
await client.reviews.reject(message_id, {"reason": "spam"})
```

## Real-time (no webhook)

Open a notification stream instead of hosting a webhook:

```ts
for await (const n of client.listen("bot@agents.e2a.dev")) {
  const msg = await client.messages.get(n.recipient, n.message_id);
}
```

```python
async for n in client.listen("bot@agents.e2a.dev"):
    msg = await client.messages.get(n.recipient, n.message_id)
```

## Raw REST (without an SDK)

No SDK for your language? Call the API directly. Base URL
`https://api.e2a.dev/v1/...`, JSON in/out, bearer auth on every request:

```
Authorization: Bearer <e2a_acct_… | e2a_agt_… | OAuth access token>
```

Conventions:

- **Pagination** — list endpoints take `?cursor=` and return `next_cursor`
  (null when exhausted).
- **Errors** — non-2xx bodies are `{"error": {"code", "message", "request_id"}}`;
  branch on the machine `code`.
- **Idempotency** — sends (`send`/`reply`/`forward`/approve) accept an
  `Idempotency-Key` header; a retried call replays instead of double-sending.
- **Scopes** — account keys manage agents/domains/keys/reviews; agent keys are
  pinned to one inbox.

The endpoint map, exact request/response bodies, enums, and error codes are all
in the OpenAPI 3.1 contract — generated from the live handlers, so it never
lags: **https://e2a.dev/openapi.yaml**. The core resources are `agents`
(inboxes), `messages` (send/reply/forward/get/list/attachments), `reviews`
(HITL), `domains`, `webhooks`, `events`, and `account`.

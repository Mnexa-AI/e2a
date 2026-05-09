# e2a Python SDK

Python SDK for the [e2a protocol](https://e2a.dev) — email-to-agent authentication.

## Install

```bash
pip install e2a
```

For WebSocket real-time delivery:

```bash
pip install e2a[ws]
```

## Upgrading from 1.x to 2.0

Webhook-parsed emails now refuse to expose claim fields (`sender`, `subject`, `text_body`, …) until the HMAC signature is verified — `email.sender` raises `UnverifiedEmailError` instead of silently returning attacker-controllable data. The one-line fix is to switch `client.parse(body)` → `client.parse_webhook(body)`:

```diff
- email = client.parse(await request.body())
+ email = client.parse_webhook(await request.body())
```

`parse_webhook` reads the secret from `E2A_WEBHOOK_SECRET`; set it before upgrading. If you must inspect the payload before verifying, use `email.unverified_payload`. REST-fetched emails (`client.get_message`) are unaffected — they're pre-verified via the bearer token. Full background in the [PR](https://github.com/Mnexa-AI/e2a/pull/57).

## Import paths

The stable, pinned API surface lives under `e2a.v1`:

```python
from e2a.v1 import E2AClient, AsyncE2AClient, E2AApi
```

Top-level `e2a` imports remain available as convenience aliases to the current stable version, but use `e2a.v1` in examples, production code, and version-pinned integrations.

## Quick start

```python
from e2a.v1 import E2AClient

# Reads E2A_API_KEY from environment automatically
client = E2AClient()

# Or pass explicitly:
# client = E2AClient(api_key="e2a_your_api_key")
```

Mount the webhook in your web framework:

Webhook payloads are HMAC-signed. The SDK gates field access behind verification — accessing `email.sender`, `email.subject`, etc. on an unverified payload raises `UnverifiedEmailError`. Use `client.parse_webhook(...)` to parse + verify in one call:

**FastAPI:**
```python
from fastapi import FastAPI, Request, HTTPException

app = FastAPI()

@app.post("/webhook")
async def webhook(request: Request):
    try:
        email = client.parse_webhook(await request.body())  # reads E2A_WEBHOOK_SECRET
    except PermissionError:
        raise HTTPException(401, "bad signature")
    # ValueError is raised if no secret is configured — let it 500 so a misconfig
    # surfaces loudly, or catch it here to return a clearer message.
    print(f"From: {email.sender}, Subject: {email.subject}")
    email.reply("Thanks for reaching out!")
    return {"ok": True}
```

**Flask:**
```python
from flask import Flask, request, abort

app = Flask(__name__)

@app.post("/webhook")
def webhook():
    try:
        email = client.parse_webhook(request.get_data())
    except PermissionError:
        abort(401)
    email.reply("Thanks for reaching out!")
    return {"ok": True}
```

Get a signing secret from the dashboard's Settings → Webhook signing secrets (or `POST /api/v1/users/me/signing-secrets`). Set it as `E2A_WEBHOOK_SECRET` so `parse_webhook` picks it up automatically, or pass it explicitly: `client.parse_webhook(body, secret="whsec_...")`.

## Raw vs high-level API

The SDK has two layers:

- **`E2AApi`** / **`AsyncE2AApi`** — raw typed HTTP client. Returns generated Pydantic models. Uses `/api/v1/` paths.
- **`E2AClient`** / **`AsyncE2AClient`** — high-level wrapper. Returns parsed `InboundEmail` objects with `.reply()`.

Access the raw layer through `client.api`:

```python
from e2a.v1 import E2AClient

client = E2AClient(api_key="e2a_...")

# High-level: returns InboundEmail with parsed MIME, .reply(), etc.
email = client.get_message("msg_123")

# Raw: returns generated MessageDetail Pydantic model
detail = client.api.get_message("bot@agents.e2a.dev", "msg_123")
```

## Conversation threading

e2a supports an opaque `conversation_id` that lets your agent track multi-turn
threads across the email boundary. Pass it on any `send()` or `reply()`, and
e2a will surface it on the recipient's inbound payload when they respond —
whether the other side is a human replying from Gmail or another e2a agent.

### The basic loop

```python
@app.post("/webhook")
async def webhook(request: Request):
    email = client.parse_webhook(await request.body())

    if email.conversation_id:
        # Follow-up — route to the existing conversation
        conversation = get_conversation(email.conversation_id)
    else:
        # First contact — create a new conversation and pick an id for it
        conversation = create_conversation(sender=email.sender)

    response = conversation.generate_reply(email)

    # Tag the reply so future messages in this thread are linked
    email.reply(
        body=response.text,
        html_body=response.html,
        conversation_id=conversation.id,
    )
    return {"ok": True}
```

Same idea for a new outbound:

```python
result = client.send(
    to="alice@example.com",
    subject="Following up",
    body="Hi Alice, just checking in.",
    conversation_id="conv_abc123",
)
# When Alice replies, the webhook will include conversation_id="conv_abc123"
```

### When is `email.conversation_id` populated?

| Inbound type | Sender passed `conversation_id`? | What you see |
|---|---|---|
| First email from a human (new thread) | n/a — humans don't pass it | `None` — **you must assign one** if you want to thread subsequent messages |
| Human reply to an earlier email from your agent | n/a | The id you passed on your outbound (recovered via `In-Reply-To`) |
| Another e2a agent sending you a new message | **yes, recommended** | The sender's asserted id (carried on a custom header) |
| Another e2a agent sending you a new message | no | `None` |
| Another e2a agent replying to you | either way | Your earlier outbound's id, unless the sender asserted a different one |

Rules of thumb:

- **Always pass `conversation_id`** when you're tagging an outbound as part of a known thread. It's the only way the *recipient's* webhook will see it.
- On first contact from a human, **assign a new id yourself** and stash it before you reply. After that, `email.conversation_id` will keep threading the conversation.
- Don't look up the id from `email.sender` alone — the same person can have many parallel threads.

### Agent-to-agent conversations

If the recipient is another e2a-managed agent, `conversation_id` passed on
`send()` arrives on the recipient's inbound on the very first message — no
prior exchange needed. e2a carries it across on a custom header
(`X-E2A-Conversation-Id`) for same-platform traffic. External senders
(Gmail, Outlook, …) can't forge this header: it's only honored when the
message originates from our own relay.

```python
# Agent A initiates a thread with Agent B
await client_a.send(
    to=["bob@agent.acme.com"],
    subject="Can you handle this?",
    body="Details in the body.",
    conversation_id="task-2026-04-19-7f3a",
)

# Agent B's webhook immediately sees conversation_id="task-2026-04-19-7f3a"
# on the very first message — no round-trip required.
```

### What `conversation_id` is *not*

- Not globally unique; not a primary key in e2a's DB. e2a treats it as an
  opaque string tagged on each message.
- Not a security boundary. Don't rely on it for authentication — check
  `email.auth_headers` for verified sender identity.
- Not guaranteed on every message. Design your code to handle `None`
  (typically: first contact from a human, or an external sender you've
  never interacted with before).

## Attachments

### Receiving attachments

Inbound email attachments are automatically parsed and available on
`email.attachments`:

```python
email = client.parse_webhook(body)
for att in email.attachments:
    print(f"{att.filename} ({att.content_type}, {att.size} bytes)")
    save_file(att.filename, att.data)
```

### Sending attachments

Pass `Attachment` objects when sending or replying:

```python
from e2a.v1 import Attachment

# Read a file
with open("report.pdf", "rb") as f:
    pdf_data = f.read()

# Send with attachment
client.send(
    to="alice@example.com",
    subject="Your report",
    body="See attached.",
    attachments=[
        Attachment(
            filename="report.pdf",
            content_type="application/pdf",
            data=pdf_data,
            size=len(pdf_data),
        )
    ],
)

# Or reply with attachment
email.reply(
    "Here's the file you requested.",
    attachments=[
        Attachment(filename="data.csv", content_type="text/csv", data=csv_bytes, size=len(csv_bytes))
    ],
)
```

## Async support

For async frameworks like FastAPI, use `AsyncE2AClient`. Same interface,
all I/O methods are async:

```python
from e2a.v1 import AsyncE2AClient

client = AsyncE2AClient()  # reads E2A_API_KEY from env

@app.post("/webhook")
async def webhook(request: Request):
    email = await client.parse_webhook(await request.body())
    await email.reply("Thanks!", conversation_id="conv_123")
    return {"ok": True}
```

## WebSocket (real-time delivery for local agents)

Local-mode agents can receive emails in real time via WebSocket using the
async `listen()` method. No public URL needed.

```bash
pip install e2a[ws]
```

```python
import asyncio
from e2a.v1 import AsyncE2AClient

async def main():
    async with AsyncE2AClient(api_key="e2a_...") as client:
        async for notif in client.listen("my-bot@agents.e2a.dev"):
            # The notification is lightweight metadata only — no body, no REST call.
            print(f"From: {notif.from_}, Subject: {notif.subject}")

            # Fetch the full email when you actually want it.
            email = await client.get_message(notif.message_id)
            await email.reply("Got it!")

asyncio.run(main())
```

`listen()` yields `WSNotification` objects — the lightweight metadata
the server pushes (`message_id`, `from_`, `recipient`, `subject`,
`received_at`, `conversation_id`). It does **not** auto-fetch the body:
that's the caller's call. This matches the server design (small WS
frames, explicit REST fetch) and lets callers skip messages without a
network round-trip.

Reconnects automatically with exponential backoff (1s, 2s, 4s, ... up
to 30s by default). Protocol is server-to-client only — the client
never sends application frames.

**`WSNotification` fields:**

| Field | Type | Notes |
|---|---|---|
| `message_id` | `str` | Pass to `client.get_message(...)` to fetch the body |
| `from_` | `str` | Sender. Trailing underscore: `from` is a Python keyword |
| `recipient` | `str` | Per-delivery target (your agent's address) |
| `subject` | `str` |  |
| `received_at` | `str` | RFC 3339 timestamp |
| `conversation_id` | `str \| None` | Threading; `None` for first contact |

**`listen()` parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `agent_email` | `str` | `client.agent_email` | Agent email to listen for |
| `reconnect` | `bool` | `True` | Auto-reconnect on disconnect |
| `max_backoff` | `float` | `30.0` | Maximum reconnect delay (seconds) |

## Agent and domain management

```python
from e2a.v1 import E2AClient

client = E2AClient(api_key="e2a_...")

# Register a shared-domain agent using a slug (just the local part, not a full email).
# The server appends @agents.e2a.dev automatically.
result = client.register_agent("my-bot")        # slug only, e.g. "my-bot"
print(result.email)  # my-bot@agents.e2a.dev

# Custom domain agent — use the `email` parameter with a full email address.
# The domain must be registered and DNS-verified first.
result = client.register_agent(email="support@mycompany.com", agent_mode="cloud", webhook_url="https://mycompany.com/webhook")

# List agents
agents = client.list_agents()

# Domain management
client.register_domain("mycompany.com")
client.verify_domain("mycompany.com")
client.list_domains()
client.delete_domain("mycompany.com")
```

## Sending emails

Send outbound emails directly:

```python
result = client.send(
    to="alice@example.com",
    subject="Hello from my agent",
    body="Hi Alice!",
    conversation_id="conv_abc123",  # optional
)
print(result.status, result.message_id)
```

## InboundEmail

| Field | Type | Description |
|---|---|---|
| `message_id` | `str` | Unique e2a message ID |
| `conversation_id` | `str \| None` | Your thread ID from a prior reply, or `None` for first contact |
| `sender` | `str` | Sender email address |
| `recipient` | `str` | Per-delivery target — your agent's address |
| `to` | `list[str]` | Parsed `To:` header — every address from the original message |
| `cc` | `list[str]` | Parsed `Cc:` header (empty when no CCs) |
| `subject` | `str` | Email subject line |
| `text_body` | `str` | Plain-text email body |
| `html_body` | `str \| None` | HTML email body, if present |
| `attachments` | `list[Attachment]` | File attachments (empty list if none) |
| `received_at` | `str \| None` | Timestamp when the message was received |
| `is_verified` | `bool` | Whether the sender's identity is verified |
| `auth` | `AuthHeaders` | Full authentication details |
| `raw_message` | `bytes` | Raw RFC 2822 email bytes |

All claim fields (`message_id`, `sender`, `recipient`, `to`, `cc`, `subject`, `text_body`, `html_body`, `attachments`, `conversation_id`, `received_at`) are gated — accessing them on an unverified webhook payload raises `UnverifiedEmailError`. Always-available regardless of verification: `auth`, `raw_message`, `is_verified`, `verified`, `unverified_payload`. Emails returned by `client.get_message(...)` are pre-verified (the bearer token already authenticated the channel). `client.get_messages(...)` returns lightweight `MessageSummary` items, not `InboundEmail`, so the gate doesn't apply.

**Methods:**

- `email.verify_signature(secret=None)` → `bool` — verifies the HMAC; falls back to `E2A_WEBHOOK_SECRET`. Sets the verified flag on success so claim fields become accessible.
- `email.reply(body, html_body=None, conversation_id=None, attachments=None)` → `SendResult`
- `email.unverified_payload` — escape hatch for inspection (debugging, logging) without verifying. Treat as untrusted.

## API Reference

### `E2AClient(api_key=None, agent_email=None, base_url="https://e2a.dev")`

High-level sync client. `api_key` falls back to `E2A_API_KEY` env var.

- `client.parse_webhook(body, secret=None)` → `InboundEmail` — parse + HMAC-verify (recommended for webhook handlers). Reads `E2A_WEBHOOK_SECRET` if no secret is passed; raises `PermissionError` on bad signature.
- `client.parse(body)` → `InboundEmail` — *deprecated since 2.2, removed in 3.0.* Accepts bytes, str, dict, or `MessageDetail` and returns an unverified email. Use `parse_webhook` for webhook handlers, or `email.unverified_payload` for inspection without verification. Calling `parse` emits a `DeprecationWarning`.
- `client.get_message(message_id)` → `InboundEmail` — pre-verified (REST channel auth)
- `client.get_messages(status="unread", page_size=50)` → `MessageList`
- `client.reply(message_id, body, ...)` → `SendResult`
- `client.send(to, subject, body, ...)` → `SendResult`
- `client.api` → `E2AApi` (raw typed access)

### `AsyncE2AClient(api_key=None, agent_email=None, base_url="https://e2a.dev")`

Same as `E2AClient` — all I/O methods are `async`. `parse()` is sync (no I/O needed).

- `client.listen(agent_email=None, reconnect=True, max_backoff=30.0)` → `AsyncIterator[WSNotification]` (requires `e2a[ws]`). Yields lightweight notifications; call `await client.get_message(notif.message_id)` to fetch the body.
- `client.api` → `AsyncE2AApi` (raw typed async access)

### Models

- `InboundEmail` / `AsyncInboundEmail` — parsed email with `.reply()`
- `Attachment` — `filename`, `content_type`, `data` (bytes), `size`
- `SendResult` — `status`, `message_id`, `method`
- `AuthHeaders` — `verified`, `sender`, `entity_type`, `domain_check`, `delegation`, `signature`, `timestamp`, `message_id`, `body_hash`

### Exceptions

- `E2AApiError` — API error (has `status_code` and `message`)
- `UnverifiedEmailError` — raised on `InboundEmail` claim-field access before `verify_signature()` has succeeded
- `PermissionError` — raised by `parse_webhook` on bad signature

## License

Apache-2.0 — see [LICENSE](https://github.com/Mnexa-AI/e2a/blob/main/LICENSE) and [NOTICE](https://github.com/Mnexa-AI/e2a/blob/main/NOTICE) in the upstream repo.

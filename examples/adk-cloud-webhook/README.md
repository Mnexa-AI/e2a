# ADK + e2a: an email inbox for a Google ADK agent

A minimal end-to-end example: a Google [Agent Development Kit](https://adk.dev/)
agent receives email through an [e2a](https://e2a.dev) cloud-mode webhook,
runs a turn, and replies — keeping a per-thread conversation memory by
mapping e2a's `conversation_id` to ADK's `session_id`.

```
human (Gmail)
    |
    v  SMTP
e2a relay
    |
    v  HTTPS POST /webhook (signed)
this app
    |
    +-- construct_event (verify HMAC + decode to a typed event)
    +-- look up / create ADK session keyed by conversation_id
    +-- runner.run_async(...)
    +-- client.messages.reply(address, message_id, {body, conversation_id})
    |
    v
e2a relay -> SMTP -> human
```

## Prerequisites

- Python 3.10+
- An e2a account with a registered **cloud-mode** agent. Sign up at
  [e2a.dev](https://e2a.dev), create an agent, set its mode to `cloud`.
  You'll need its **API key**, an **HMAC signing secret** (create one
  on the dashboard's **Webhook secrets** page — copy it the moment it's
  shown, it's not retrievable later), and a public webhook URL (see
  "Exposing the webhook" below).
- A Google AI Studio API key for Gemini —
  [aistudio.google.com/apikey](https://aistudio.google.com/apikey).

## Setup

```bash
cd examples/adk-cloud-webhook
python -m venv .venv && source .venv/bin/activate
pip install -e .
cp .env.example .env
# edit .env with your three secrets
```

## Run the webhook

```bash
uvicorn webhook:app --host 0.0.0.0 --port 18080 --reload
```

Health check:

```bash
curl http://localhost:18080/health
# {"status":"ok"}
```

## Exposing the webhook

The webhook needs a public HTTPS URL e2a can POST to. For local
development, [ngrok](https://ngrok.com/) or
[cloudflared](https://github.com/cloudflare/cloudflared) work well:

```bash
ngrok http 18080
# Forwarding  https://abc123.ngrok.io -> http://localhost:18080
```

Subscribe that public URL to your agent's inbound mail. Webhooks are a separate
resource (`/v1/webhooks`) — pass the agent's address as a filter (replace
`<YOUR_AGENT_EMAIL>` and `<YOUR_API_KEY>`):

```bash
curl -X POST https://e2a.dev/v1/webhooks \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://abc123.ngrok.io/webhook",
    "events": ["email.received"],
    "filters": {"agent_ids": ["<YOUR_AGENT_EMAIL>"]}
  }'
```

## Try it

Send a plain email from any address to your agent's e2a email. Watch the
uvicorn logs — you'll see the inbound POST, ADK pick a session, the agent
generate a reply, and the reply post back to e2a. Reply to the agent's
message from your inbox and you'll see ADK reuse the same session — the
agent remembers the prior turn.

## How `conversation_id` keeps memory across turns

Email is stateless at the SMTP layer. e2a re-creates threading by
propagating an opaque `conversation_id` through each round-trip:

1. **First inbound** has `conversation_id = None` (the human just
   started a thread). The webhook mints `conv_<random>` and uses it as
   the ADK `session_id` when creating the session.
2. The webhook calls `client.messages.reply(address, message_id,
   {"body": text, "conversation_id": "conv_<random>"})`. e2a stamps
   `X-E2A-Conversation-Id: conv_<random>` on the outbound message and
   remembers the binding to the message ID.
3. **Subsequent inbound** (the human replies in their mail client) lands
   with the same `conversation_id` set — recovered from `In-Reply-To` /
   `References` lookup. The webhook calls `get_session` with that ID and
   gets back the existing ADK session, so the agent sees full prior
   context on the next `runner.run_async` call.

This is the entire trick. The webhook is ~30 lines of business logic;
ADK does the actual memory work, e2a does the actual email work.

See [webhook.py](webhook.py) for the implementation, including
[HMAC signature verification](https://github.com/Mnexa-AI/e2a/blob/main/sdks/python/README.md#quick-start)
which you should *always* do on a public webhook before trusting any
field on the parsed payload.

## What this example deliberately doesn't show

- **Persistence + multi-worker safety.** `InMemorySessionService` loses
  everything on restart. It also lives in *one* Python process — running
  `uvicorn ... --workers 4` shards sessions per-worker, so the same
  conversation can land on different workers and lose memory at random.
  For anything beyond the demo, use `DatabaseSessionService` (Postgres /
  SQLite) — see [ADK sessions docs](https://adk.dev/sessions/session/).
- **Tools.** The agent has no tools beyond text generation. Add them to
  `agent.py` once the email loop works end-to-end.
- **Attachments.** Attachment metadata lives on the `MessageView` you
  fetch with `client.messages.get(address, message_id)` (the typed event
  from `construct_event` carries the message payload); this example
  ignores it. ADK's `Content` supports inline data parts if you want to
  feed attachments to a vision model.
- **HITL.** The agent replies immediately. If you want human approval
  before the reply goes out, enable HITL on the agent
  ([docs](https://github.com/Mnexa-AI/e2a/blob/main/README.md)) and the
  `client.messages.reply(...)` call returns `status: "pending_approval"`
  with the reply held for review.

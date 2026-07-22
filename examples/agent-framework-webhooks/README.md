# Minimal agent webhooks

Two small, runnable references show the same authenticated inbound flow with
the OpenAI Agents SDK:

- [Python webhook](python/) and its [OpenAI agent](python/agent_webhooks/agent.py)
  ([official OpenAI Agents SDK documentation](https://openai.github.io/openai-agents-python/)).
- [TypeScript webhook](typescript/) and its [OpenAI agent](typescript/src/agent.ts)
  ([official OpenAI Agents SDK documentation](https://openai.github.io/openai-agents-js/)).

The delivery code is deliberately provider-neutral. To use Anthropic,
LangChain, or Google ADK, keep the handler and replace only the small model
call; compact substitutions are shown below.

## Shared delivery lifecycle

Both webhooks:

1. enforce a 1 MiB body limit while preserving the exact signed bytes;
2. verify and parse with `construct_event` (Python) or `constructEvent`
   (TypeScript) before fetching mail or calling a model;
3. ignore non-`email.received` events and claim `event.id` before processing;
4. hydrate the ergonomic facade with `client.inbound.from_event(event)` or
   `client.inbound.fromEvent(event)`;
5. reuse the email conversation ID, or derive a collision-safe, retry-stable
   conversation anchor for first contact;
6. project normalized fields into an untrusted model prompt;
7. send through the bound `email.reply(...)`, using `event.id` as the
   idempotency key; and
8. release the event claim on failure so webhook delivery can retry.

An `accepted` or `sent` send becomes `replied`. Other send statuses, including
`pending_review`, pass through unchanged. Empty model output becomes
`no_reply` and sends nothing.

Webhook delivery is at least once. The included in-memory claim prevents a
duplicate side effect only within one tutorial process, while the stable reply
idempotency key protects retries. Production deployments should claim
`event.id` in durable shared storage with a unique constraint.

## Run the Python example

Python 3.10 or newer is required (CI uses 3.12).

```bash
cd examples/agent-framework-webhooks/python
python3.12 -m venv .venv
source .venv/bin/activate
pip install -e ../../../sdks/python -e '.[dev]'
pytest -q
mypy agent_webhooks tests
python -m agent_webhooks.dry_run
```

For a live webhook, copy `.env.example`, set `E2A_API_KEY`,
`E2A_WEBHOOK_SECRET`, and `OPENAI_API_KEY`, then start it:

```bash
agent-framework-webhooks
```

## Run the TypeScript example

Node 18 or newer is required.

```bash
npm ci
npm run build --workspace @e2a/sdk
cd examples/agent-framework-webhooks/typescript
npm ci
npm test -- --run
npm run typecheck
npm run build
npm run dry-run
```

For a live webhook, copy `.env.example`, set `E2A_API_KEY`,
`E2A_WEBHOOK_SECRET`, and `OPENAI_API_KEY`, then start it:

```bash
npm start
```

`E2A_API_KEY` should be scoped to the receiving agent. The webhook signing
secret is returned once when the subscription is created. `E2A_API_URL` is
optional and defaults to the hosted API. `OPENAI_MODEL` optionally overrides
the example's default model.

The dry runs need no provider or e2a credentials. Each signs a first-contact
fixture and executes signature verification, facade hydration, safe prompt
projection, bound reply delegation, and duplicate handling without network
access.

## Create the subscription

Expose port 8000 at a public HTTPS URL, then use a separate account-scoped key
to create the account-level subscription:

```bash
curl -X POST https://api.e2a.dev/v1/webhooks \
  -H "Authorization: Bearer <YOUR_ACCOUNT_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://your-public-host.example/webhook",
    "events": ["email.received"],
    "filters": {"agent_emails": ["agent@agents.e2a.dev"]}
  }'
```

Copy the returned `whsec_...` value into `E2A_WEBHOOK_SECRET`, then send mail
to the filtered agent address.

## Trust boundaries

`construct_event(...)` / `constructEvent(...)` verifies the webhook
envelope's HMAC signature and replay timestamp. This proves that the payload
came from the holder of the webhook signing secret; application code does not
need a second signature check. It is separate from `email.verified`, which
reports DMARC alignment for the hydrated message.

A valid webhook signature does not make the sender, subject, or content
trustworthy. A DMARC pass authenticates a domain, not a person or the truth of
the message. The model prompt includes only normalized `from`, `subject`,
plain-text body, `verified`, and `flagged` values, all treated as untrusted
input. Raw MIME, the full message view, and attachments are excluded from
prompts and logs.

## Anthropic

Keep the verified webhook handler unchanged and replace the OpenAI agent body
with a Messages call. Join only returned text blocks. See the official
[Python SDK](https://platform.claude.com/docs/en/api/client-sdks#python) and
[TypeScript SDK](https://platform.claude.com/docs/en/api/client-sdks#typescript)
documentation.

```python
from anthropic import AsyncAnthropic

message = await AsyncAnthropic().messages.create(
    model="claude-opus-4-8",
    max_tokens=1024,
    system=REPLY_INSTRUCTIONS,
    messages=[{"role": "user", "content": email_prompt(email)}],
)
reply = "\n".join(block.text for block in message.content if block.type == "text")
```

## LangChain

Create one agent at application startup, invoke it with the same normalized
prompt, and return the final assistant message text. See the official
[Python agent documentation](https://docs.langchain.com/oss/python/langchain/agents)
and [JavaScript agent documentation](https://docs.langchain.com/oss/javascript/langchain/agents).

```python
from langchain.agents import create_agent

agent = create_agent(model="openai:gpt-5.5", tools=[], system_prompt=REPLY_INSTRUCTIONS)
result = await agent.ainvoke({"messages": [{"role": "user", "content": email_prompt(email)}]})
reply = result["messages"][-1].content
```

If a framework returns structured content, extract and join only its text
blocks before calling `email.reply(...)`.

## Google ADK

Use the effective e2a conversation ID as ADK's `sessionId` and an opaque,
inbox-scoped sender identity as `userId`. Then run the safe prompt through the
session and take text only from the final response event:

```python
async for agent_event in runner.run_async(
    user_id=sender_user_id,
    session_id=conversation_id,
    new_message=types.Content(role="user", parts=[types.Part(text=email_prompt(email))]),
):
    if agent_event.is_final_response() and agent_event.content:
        reply = "\n".join(part.text or "" for part in agent_event.content.parts)
```

Create or load the session before running it. The expanded
[ADK webhook tutorial](../adk-cloud-webhook/README.md) covers collision-safe
sender identities, first-contact conversation mapping, and durable session
storage. See the official ADK [Python](https://adk.dev/get-started/python/)
and [TypeScript](https://adk.dev/get-started/typescript/) documentation.

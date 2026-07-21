# Agent framework webhooks

Copyable e2a webhook integrations for four agent stacks in both Python and
TypeScript. Every adapter uses the same authenticated inbound lifecycle; the
provider-specific file only turns normalized email fields into one model turn.

| Framework | Python | TypeScript | Runtime credential |
|---|---|---|---|
| OpenAI Agents SDK | [`openai.py`](python/agent_webhooks/adapters/openai.py) | [`openai.ts`](typescript/src/adapters/openai.ts) | `OPENAI_API_KEY` |
| Anthropic Messages SDK | [`anthropic.py`](python/agent_webhooks/adapters/anthropic.py) | [`anthropic.ts`](typescript/src/adapters/anthropic.ts) | `ANTHROPIC_API_KEY` |
| LangChain | [`langchain.py`](python/agent_webhooks/adapters/langchain.py) | [`langchain.ts`](typescript/src/adapters/langchain.ts) | `OPENAI_API_KEY` |
| Google ADK | [`adk.py`](python/agent_webhooks/adapters/adk.py) | [`adk.ts`](typescript/src/adapters/adk.ts) | `GEMINI_API_KEY` or `GOOGLE_API_KEY` |

The adapters follow the official framework APIs: [OpenAI Agents
SDK](https://openai.github.io/openai-agents-python/), [Anthropic client
SDKs](https://docs.anthropic.com/en/api/client-sdks), [LangChain
agents](https://docs.langchain.com/oss/python/langchain/agents), and [Google
ADK](https://google.github.io/adk-docs/).

## What happens for each delivery

Both hosts perform the same steps:

1. Read the exact request bytes and `X-E2A-Signature` header.
2. Verify and parse with `construct_event` (Python) or `constructEvent`
   (TypeScript) before any fetch, model call, or reply.
3. Ignore events other than `email.received`, then claim `event.id` in the
   example deduper.
4. Hydrate the ergonomic email object with
   `client.inbound.from_event(event)` or `client.inbound.fromEvent(event)`.
5. Run the selected adapter on a small projection of normalized fields.
6. Reply through the bound `email.reply(...)`, using `event.id` as the
   idempotency key, and return the e2a send status.

An `accepted` or `sent` send is reported as `replied`. Other statuses, including
`pending_review`, pass through unchanged so an application can surface a HITL
hold. Empty model output is acknowledged as `no_reply` and does not send mail.

e2a webhooks are delivered at least once. The in-memory claim plus reply
idempotency key prevents a duplicate side effect within this single-process
tutorial. On failure the claim is released for retry. In production, replace
the in-memory deduper with a durable unique claim on `event.id`.

## Run without provider keys

The fake adapter signs a fixture delivery and executes the real shared path,
including signature verification, facade hydration, prompt projection,
deduplication, and bound reply delegation. It makes no provider or e2a network
request.

Python (3.10+, with 3.12 used in CI):

```bash
cd examples/agent-framework-webhooks/python
python3.12 -m venv .venv
source .venv/bin/activate
pip install -e ../../../sdks/python -e '.[dev]'
pytest -q
mypy agent_webhooks
python -m agent_webhooks.dry_run
```

TypeScript (Node 24.13+ is required by the ADK package):

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

Each dry run prints one `status=replied`, one `status=duplicate`, and the single
captured deterministic reply.

## Configure a real framework

Copy the language-specific environment template, set the two e2a credentials,
and select exactly one of `openai`, `anthropic`, `langchain`, or `adk`:

```bash
cp .env.example .env
# E2A_API_KEY=e2a_...
# E2A_WEBHOOK_SECRET=whsec_...
# AGENT_FRAMEWORK=openai
```

`E2A_API_KEY` should be an agent-scoped key for the inbox being fetched and
replied from. `E2A_WEBHOOK_SECRET` is the signing secret returned once when the
subscription is created. `E2A_API_URL` is optional and defaults to the hosted
API.

| `AGENT_FRAMEWORK` | Required environment | Optional model override (default) |
|---|---|---|
| `openai` | `OPENAI_API_KEY` | `OPENAI_MODEL` (`gpt-5.6`) |
| `anthropic` | `ANTHROPIC_API_KEY` | `ANTHROPIC_MODEL` (`claude-opus-4-8`) |
| `langchain` | `OPENAI_API_KEY` | `LANGCHAIN_MODEL` (Python: `openai:gpt-5.5`; TypeScript: `openai:gpt-5.4`) |
| `adk` | `GEMINI_API_KEY` or `GOOGLE_API_KEY` | `ADK_MODEL` (`gemini-flash-latest`) |

ADK can instead use Vertex AI with `GOOGLE_GENAI_USE_VERTEXAI=true` plus
`GOOGLE_CLOUD_PROJECT` and `GOOGLE_CLOUD_LOCATION`. The included LangChain
example intentionally installs and accepts only the `openai:` provider prefix.
`AGENT_FRAMEWORK=fake` remains available for local checks and needs no provider
credential.

Start the Python server:

```bash
cd examples/agent-framework-webhooks/python
source .venv/bin/activate
agent-framework-webhooks
# equivalent: uvicorn agent_webhooks.app:create_app --factory --host 0.0.0.0 --port 8000
```

Start the TypeScript server after the install/build commands above:

```bash
cd examples/agent-framework-webhooks/typescript
npm run build
npm start
```

Expose port 8000 at a public HTTPS URL, then create an account-level webhook
subscription with a separate account-scoped key:

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

Copy the returned `whsec_...` value immediately into
`E2A_WEBHOOK_SECRET`, then send mail to the filtered agent address.

## Trust boundaries

`construct_event(...)` / `constructEvent(...)` verifies the webhook envelope's
HMAC signature and replay timestamp. That proves the payload was delivered by
the holder of the webhook signing secret. It is distinct from
`email.verified`, which reports whether the hydrated inbound message passed
DMARC alignment. A valid webhook signature does not make the sender, subject,
or content trustworthy; a DMARC pass authenticates a domain, not a person or
the truth of the message.

The prompt contains only normalized `from`, `subject`, plain-text body,
`verified`, and `flagged` values. All of those fields are treated as untrusted
model input. Raw MIME, the full `MessageView`, and attachments are deliberately
excluded.

The tutorial deduper and both ADK session stores are in memory. They lose state
on restart and are not shared across workers. Production deployments need a
durable event claim and, when conversation memory matters, a durable framework
session service. The expanded [ADK webhook example](../adk-cloud-webhook/README.md)
walks through conversation/session mapping in more detail.

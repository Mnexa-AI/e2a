---
description: Mental model and common workflows for e2a (email for AI agents) — send/receive email, HITL approval, agent + domain management. Read this before driving the e2a MCP tools or the `e2a` CLI. Pretraining assumes "human reading their inbox", but with e2a YOU are the agent and the inbox IS the agent. Covers send_email vs reply_to_message threading, the pending_approval HITL status, multi-agent disambiguation, custom-domain DNS flow, webhook SDK examples (Python + TypeScript), and other gotchas.
version: 10
---

# Using e2a

<!-- version: 10 -->

e2a is an authenticated email gateway for AI agents. It gives an agent a real email address (`agent@agents.e2a.dev` or `agent@your-domain.com`), verifies sender identity (SPF/DKIM), threads conversations, and optionally pauses outbound mail for human review.

This skill works two ways:
- **Via the Claude Code plugin (MCP).** Tools appear in the menu as `mcp__e2a__*` — 33 of them covering agents, messages, HITL, attachments, domains, events, and webhooks.
- **Via the `e2a` CLI / SDKs.** Same operations, different surface: `e2a` commands on the shell, or the TypeScript / Python SDK in your own webhook handler.

The mental model and gotchas are identical across both surfaces. Workflow steps below call out the MCP tool name and the CLI equivalent — pick whichever fits the current session.

## Preamble (run first when installed via curl)

If you got this skill by curling into `~/.claude/skills/e2a/SKILL.md` (the non-plugin path), run this once per day to check for updates. If you're using the Claude Code plugin, the plugin manager handles updates — skip this section.

```bash
_E2A_STATE="$HOME/.e2a/skill"
_E2A_CHECK="$_E2A_STATE/last-check"
_E2A_LOCAL=10
_E2A_NOW=$(date +%s)
_E2A_SKIP=0
if [ -f "$_E2A_CHECK" ]; then
  _E2A_LAST=$(cat "$_E2A_CHECK")
  [ $((_E2A_NOW - _E2A_LAST)) -lt 86400 ] && _E2A_SKIP=1
fi
if [ "$_E2A_SKIP" -eq 0 ]; then
  mkdir -p "$_E2A_STATE"
  echo "$_E2A_NOW" > "$_E2A_CHECK"
  _E2A_REMOTE=$(curl -sf https://raw.githubusercontent.com/Mnexa-AI/e2a/main/skills/using-e2a/SKILL.md | sed -n 's/.*version: \([0-9]*\).*/\1/p' | head -1)
  if [ -n "$_E2A_REMOTE" ] && [ "$_E2A_REMOTE" -gt "$_E2A_LOCAL" ] 2>/dev/null; then
    echo "E2A_SKILL_UPDATE_AVAILABLE $_E2A_LOCAL $_E2A_REMOTE"
  fi
fi
```

If output contains `E2A_SKILL_UPDATE_AVAILABLE`: tell the user a new version of the e2a skill is available and ask if they'd like to update. If yes, run:

```bash
curl -sf -o ~/.claude/skills/e2a/SKILL.md https://raw.githubusercontent.com/Mnexa-AI/e2a/main/skills/using-e2a/SKILL.md
```

Then re-read the updated file and follow its instructions instead.

## The mental model

Six load-bearing facts. Internalize these before you start calling tools or running CLI commands.

1. **An agent is an email address.** `support-bot@agents.e2a.dev` is an agent. When you send mail, the recipient sees a message FROM that address — not from "the user." When you list messages, you are reading the agent's own inbox, not the user's personal mail. You are not a secretary; you are the mailbox owner.

2. **Replies preserve threads; new sends do not.** A reply tool/command carries the `In-Reply-To` and `References` headers from the original message, so the response lands in the same email thread. A fresh send creates a new thread every time. If a user (or an inbound message) is asking you to respond to something specific, reply with the original `message_id` — even when you could synthesize an equivalent body as a new send. Thread fragmentation is the #1 visible symptom of getting this wrong.

3. **`pending_approval` is success, not failure.** When the agent has HITL enabled, outbound mail returns `{ status: "pending_approval", message_id: "msg_..." }`. The message was accepted by the server and is being held for a human to review. Do NOT retry. Do NOT report this as an error to the user. Tell them the draft was queued for approval, and (if asked) check on it via the pending tools / commands.

4. **Multi-agent accounts need `agent_email` per call.** If the account owns exactly one agent (the common case), tools/commands auto-resolve to that one. If the account owns more than one, you'll get "agentEmail required." The fix is to enumerate once (`list_agents` MCP, or `e2a agents list`), then pass `agent_email` explicitly to subsequent calls. Don't guess; don't pick at random; don't ask the user to pick if context already makes the choice obvious (e.g. they said "my support inbox").

5. **Custom domains are a two-step async dance.** Registering a domain returns DNS records (MX + TXT) to publish — it does NOT make the domain live. The user (or a DNS-provider MCP, if one is loaded) must add those records out-of-band, wait for DNS propagation (minutes to hours), then verify. Verification is idempotent and safe to retry. Until verification succeeds, the domain cannot send or receive mail. Don't promise the user their domain works the moment registration returns.

6. **HITL is not in the consent flow — toggle it explicitly.** Creating a new agent does not enable HITL. To turn on approval gates for an existing agent, update the agent with `hitl_enabled: true` (optionally with `hitl_ttl_seconds` and `hitl_expiration_action`). Same path applies to disabling it.

## Setup

### 1. Install the CLI (only if you're using it directly)

```bash
which e2a || npm install -g @e2a/cli
```

Skip this if you're driving everything through the MCP tools — the plugin needs no CLI.

### 2. Authenticate

Read `~/.e2a/config.json`. If it exists and has both `api_key` and `agent_email`, you're set — skip ahead.

Otherwise:
- **CLI path:** `e2a login` (opens a browser; saves `api_key` and `agent_email` to `~/.e2a/config.json`).
- **MCP path:** the plugin authenticates via OAuth on first tool call. If a tool returns an auth error mid-session, the refresh token has expired — re-auth via `/plugin` in Claude Code.

### 3. Register an agent if you don't have one

- **MCP:** call `create_agent` with a `slug` (e.g. `support-bot`). Registers `<slug>@<shared-domain>`. Defaults to `local` mode (poll-based — fine for MCP/CLI clients). Use `agent_mode: "cloud"` with `webhook_url` only if there's a real HTTPS endpoint to receive pushes.
- **CLI:** `e2a agents register <slug>`.

## Common workflows

### Triage the inbox

1. List unread messages (defaults are sensible).
   - MCP: `list_messages` (defaults to `status: unread`)
   - CLI: `e2a inbox`
2. Read one fully.
   - MCP: `get_message` with the `message_id`
   - CLI: `e2a read <message-id>`
3. Reply in-thread.
   - MCP: `reply_to_message` with that same `message_id`
   - CLI: `e2a reply <message-id> --body "..."`

For attachment bytes, use `get_attachment_data` (MCP) with a 0-based index. Indexes are stable within a message.

### Send a new email (with HITL awareness)

1. Send.
   - MCP: `send_email` with `to`, `subject`, `body`
   - CLI: `e2a send --to <recipient> --subject "..." --body "..."`
2. Check the response:
   - `status: sent` — done.
   - `status: pending_approval` — the agent has HITL on; the message is queued. Tell the user it's awaiting review. They can review in the dashboard, via the magic link in their notification email, or:
     - MCP: `list_pending_messages` / `get_pending_message` / `approve_pending_message` / `reject_pending_message`
     - CLI: `e2a pending list` / `e2a pending approve <id>` / `e2a pending reject <id>`

### Enable HITL on an agent

- MCP: `update_agent` with `hitl_enabled: true` (optionally `hitl_ttl_seconds`, `hitl_expiration_action`).
- CLI: `e2a agents update <slug> --hitl --hitl-ttl 3600 --hitl-expiration-action reject`.

### Add a custom domain (e.g. `mail.acme.com`)

1. Register.
   - MCP: `register_domain` with the FQDN — returns MX + TXT records and an unverified domain row.
   - CLI: `e2a domains register <domain>`.
2. Hand the records to the user (or to a DNS-provider MCP — Cloudflare, Route 53, etc. — if one is loaded; call its `create_dns_record`-style tool with the returned values).
3. Wait. DNS propagation is asynchronous — minutes typically, occasionally hours.
4. Verify.
   - MCP: `verify_domain` with the same FQDN.
   - CLI: `e2a domains verify <domain>`.
   If it returns `verified: true`, the domain is live. If still false, the response shows what DNS state was resolved so the user can debug. Retry as needed.
5. Once verified, agents can be created on (or moved to) that domain.

### Receive mail in your own backend (webhook integration)

If the user is building a cloud agent that handles inbound mail in their own service:

1. Register a cloud agent in the dashboard or via `create_agent` with `agent_mode: "cloud"` and `webhook_url: "https://..."`.
2. Create a signing secret on the dashboard's **Webhook secrets** page (shown once). Set it as `E2A_WEBHOOK_SECRET` in the webhook environment so the SDK can verify inbound payloads automatically.
3. Implement the endpoint with the e2a SDK. The SDK handles HMAC verification + decoding for you.

#### Python (FastAPI)

```bash
pip install e2a
```

```python
import os

from e2a.v1 import E2AClient, construct_event, E2AWebhookSignatureError
from fastapi import FastAPI, HTTPException, Request

app = FastAPI()
SECRET = os.environ["E2A_WEBHOOK_SECRET"]  # whsec_…

# The SDK is async-only and namespaced. There is no agent_email constructor arg.
client = E2AClient(api_key=os.environ["E2A_API_KEY"])  # or E2AClient() reads E2A_API_KEY

@app.post("/webhook")
async def webhook(request: Request):
    # construct_event = verify the X-E2A-Signature header + decode to a typed
    # event in one call. Pass the RAW body — re-serialized JSON won't match.
    try:
        event = construct_event(
            await request.body(), request.headers["X-E2A-Signature"], SECRET
        )
    except E2AWebhookSignatureError:
        raise HTTPException(400, "bad signature")

    if event.type == "email.received":
        msg = event.data  # the inbound message payload
        print(f"From: {msg.from_}")
        print(f"Subject: {msg.subject}")

        # Threaded reply — pass the agent address explicitly.
        await client.messages.reply(msg.recipient, msg.message_id, {"body": "Thanks for your email!"})
    return {"ok": True}
```

#### TypeScript (Express)

```bash
npm install @e2a/sdk
```

```typescript
import { E2AClient, constructEvent, E2AWebhookSignatureError } from "@e2a/sdk/v1";
import express from "express";

const app = express();

const client = new E2AClient({ apiKey: process.env.E2A_API_KEY! });
const SECRET = process.env.E2A_WEBHOOK_SECRET!; // whsec_…

// Use the raw body parser — re-stringified JSON won't match the signature.
app.post("/webhook", express.raw({ type: "application/json" }), async (req, res) => {
  let event;
  try {
    event = constructEvent(req.body, req.header("X-E2A-Signature"), SECRET);
  } catch (e) {
    if (e instanceof E2AWebhookSignatureError) return res.status(400).end();
    throw e;
  }
  if (event.type === "email.received") {
    const msg = event.data; // the inbound message payload
    console.log(`From: ${msg.from} — ${msg.subject}`);
    await client.messages.reply(msg.recipient, msg.messageId, { body: "Thanks for your email!" });
  }
  res.json({ ok: true });
});

app.listen(3000);
```

Inbound payload shape:

```json
{
  "message_id": "msg_abc123",
  "conversation_id": "conv_xyz",
  "from": "alice@example.com",
  "to": ["agent@agents.e2a.dev"],
  "cc": [],
  "recipient": "agent@agents.e2a.dev",
  "raw_message": "<base64-encoded RFC 2822 email>",
  "auth_headers": {
    "X-E2A-Auth-Verified": "true",
    "X-E2A-Auth-Sender": "alice@example.com",
    "X-E2A-Auth-Domain-Check": "spf=pass; dkim=pass"
  },
  "received_at": "2026-03-28T10:00:00Z"
}
```

`to` and `cc` are the parsed headers from the original message; `recipient` is this delivery's per-agent target. `construct_event` / `constructEvent` verifies the `X-E2A-Signature` header against your `whsec_…` secret and **throws** (`E2AWebhookSignatureError`) on a bad signature — so if it returns, the payload is authentic and you can read its fields directly. There is no separate "verify then read" gate and no unverified-email type; verification and decoding happen in the one call. During a secret rotation you can pass a list/array of secrets — accepted if any matches.

### Local agent with WebSocket bridge (optional)

If the user wants a local agent to receive emails in real time without writing a webhook, `e2a listen --forward` opens a WebSocket to e2a and POSTs each inbound email to a local HTTP endpoint:

```bash
e2a listen --forward http://localhost:3000/inbox
```

There's no MCP equivalent — this is a CLI-only pattern. Useful for local development or for proxying into a gateway (e.g. OpenClaw on `localhost:18789`) without exposing a public URL.

## Gotchas

- **Don't encode raw text as base64 yourself for attachments.** The `data` field expects base64 produced by another tool (a file reader, a doc generator, `get_attachment_data`). If you have plain text and want to attach it, write it to a file first and read it back, or generate the encoding via a Bash call — don't try to construct base64 from a Markdown string in your head.
- **Forwarding attachments is a verbatim copy.** Pass the `{filename, content_type, data}` tuple from `get_attachment_data` straight into the next send's `attachments[]`. No re-encoding, no re-naming necessary.
- **`get_message` deliberately omits raw MIME and attachment bytes.** Don't ask for the "full message" — you have what you need (decoded text/html bodies, headers, attachment metadata). Use `get_attachment_data` for actual bytes when you need them.
- **Destructive ops require `confirm: true`.** `delete_agent` and `delete_domain` (MCP), and their `--yes` CLI equivalents, refuse without explicit confirmation. This is a guard against hallucinated deletes; pass it only when the user has clearly asked for the destructive action.
- **`approve_pending_message` with `attachments: []` strips attachments.** An omitted `attachments` field keeps the original draft's attachments; an explicit empty array removes them. Same shape applies to other override fields — omit to keep, specify (including empty) to override.
- **HITL approval bodies are scrubbed after the terminal transition.** `get_pending_message` returns the full body only while status is `pending_approval`. Once approved or rejected, body columns are wiped server-side for compliance.
- **Token expiry on OAuth flows.** The hosted MCP runs over OAuth; if a tool starts erroring with auth failures across multiple calls, the refresh token has likely expired and the user needs to re-auth via `/plugin` in Claude Code.

## When NOT to use a tool

- Don't send a fresh message to respond to something in the inbox — reply (threading).
- Don't loop on the pending list waiting for an approval — there's no event in MCP; let the user drive when they want to check.
- Don't verify a custom domain immediately after registering it — DNS has not propagated. If the user wants a verification check, call it once and report the result; don't poll.
- Don't delete agents or domains from inferred intent. Require the user to say it.
- Don't enumerate agents on every turn. `whoami` (MCP) is cheaper for the common single-agent case; `list_agents` is only needed when `whoami` errors with the multi-agent diagnostic.

## Reference

- Config file (CLI): `~/.e2a/config.json` — JSON with `api_key`, `api_url`, `agent_email`, `shared_domain`.
- Env vars that override config: `E2A_API_KEY`, `E2A_URL`, `E2A_SHARED_DOMAIN` (CLI). `E2A_AGENT_EMAIL` is honored by `client.listen(...)` as the default agent address when you don't pass one — it is NOT a constructor argument. `E2A_WEBHOOK_SECRET` holds the `whsec_…` signing secret you pass to `construct_event` / `constructEvent` for inbound HMAC verification.
- Plugin homepage: https://e2a.dev
- 33 MCP tools spanning agents, messages, HITL, attachments, domains, events, and webhooks.
- Tool descriptions teach behavior; this skill teaches the mental model. When in doubt, read the tool's own `description` for the precise contract.
- CLI: https://www.npmjs.com/package/@e2a/cli
- TypeScript SDK: https://www.npmjs.com/package/@e2a/sdk
- Python SDK: https://pypi.org/project/e2a/
- Docs: https://e2a.dev/docs

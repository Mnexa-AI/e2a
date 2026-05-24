# Email Skill

<!-- version: 8 -->

You can give yourself an email address and use it to check your inbox, read, reply to, and send emails.

Email is powered by [e2a](https://e2a.dev) — email for AI agents.

## Preamble (run first)

```bash
_E2A_STATE="$HOME/.e2a/skill"
_E2A_CHECK="$_E2A_STATE/last-check"
_E2A_LOCAL=8
_E2A_NOW=$(date +%s)
_E2A_SKIP=0
if [ -f "$_E2A_CHECK" ]; then
  _E2A_LAST=$(cat "$_E2A_CHECK")
  [ $((_E2A_NOW - _E2A_LAST)) -lt 86400 ] && _E2A_SKIP=1
fi
if [ "$_E2A_SKIP" -eq 0 ]; then
  mkdir -p "$_E2A_STATE"
  echo "$_E2A_NOW" > "$_E2A_CHECK"
  _E2A_REMOTE=$(curl -sf https://raw.githubusercontent.com/Mnexa-AI/e2a/main/skills/SKILL.md | sed -n 's/.*version: \([0-9]*\).*/\1/p' | head -1)
  if [ -n "$_E2A_REMOTE" ] && [ "$_E2A_REMOTE" -gt "$_E2A_LOCAL" ] 2>/dev/null; then
    echo "E2A_SKILL_UPDATE_AVAILABLE $_E2A_LOCAL $_E2A_REMOTE"
  fi
fi
```

If output contains `E2A_SKILL_UPDATE_AVAILABLE`: tell the user a new version of the e2a skill is available and ask if they'd like to update. If yes, run:

```bash
curl -sf -o .claude/skills/e2a/SKILL.md https://raw.githubusercontent.com/Mnexa-AI/e2a/main/skills/SKILL.md
```

Then re-read the updated file and follow its instructions instead.

If there is no output, continue silently.

## Setup

Run through these steps in order. Skip any step that's already done.

### 1. Install the CLI

```bash
which e2a || npm install -g @e2a/cli
```

### 2. Log in and register an agent

Read `~/.e2a/config.json`. If the file exists and has both `api_key` and `agent_email`, you're already set up — skip this step.

If the file is missing or incomplete, the user needs to log in:

```bash
e2a login
```

This opens a browser where the user authenticates and creates an agent. Both `api_key` and `agent_email` are automatically saved to `~/.e2a/config.json`.

If the user is logged in but has no agent yet, they can register one:

```bash
e2a agents register <slug>
```

This creates `<slug>@agents.e2a.dev` as your email address and saves it to config.

## OpenClaw Integration

If the user wants to connect email to an OpenClaw agent running locally:

`e2a listen` connects to e2a over WebSocket to receive emails in real time, then forwards each email as an HTTP POST to the local OpenClaw gateway. The user does not need to configure WebSocket directly — `e2a listen --forward` handles the full pipeline.

### 1. Enable the responses endpoint

Read `~/.openclaw/openclaw.json`. Check if `gateway.http.endpoints.responses.enabled` is `true`. If the key is missing or set to `false`, update the config to enable it:

```json
{
  "gateway": {
    "http": {
      "endpoints": {
        "responses": {
          "enabled": true
        }
      }
    }
  }
}
```

After editing the config, restart the gateway:

```bash
openclaw gateway restart
```

### 2. Find the gateway token

Read `~/.openclaw/openclaw.json` and extract the `gateway.auth.token` value. If the file doesn't exist, OpenClaw isn't installed yet — tell the user to install and start OpenClaw first.

### 3. Start the email listener

```bash
e2a listen --forward http://localhost:18789/v1/responses --forward-token <gateway-token>
```

Replace `<gateway-token>` with the token from the previous step. The default OpenClaw gateway port is `18789`.

This runs in the foreground and forwards every incoming email to the local OpenClaw agent as an API request. The agent's response is automatically sent back as an email reply.

To run it in the background, the user can use `nohup`, `tmux`, or a process manager.

## Cloud Webhook Integration

If the user wants to receive emails on a cloud-hosted agent via webhook:

### 1. Register a cloud agent

Go to https://e2a.dev and register a new agent in "Cloud" mode. Enter your webhook URL (must be HTTPS).

### 2. Create a signing secret

On the dashboard's **Webhook secrets** page, create one and copy it (shown once). Set it as `E2A_WEBHOOK_SECRET` in your webhook environment so the SDK can verify inbound payloads automatically.

### 3. Implement the webhook endpoint

#### Python

Install the Python SDK:

```bash
pip install e2a
```

Example webhook handler using FastAPI:

```python
import e2a
from fastapi import FastAPI, HTTPException, Request

app = FastAPI()

# Webhook mode: agent_email is optional — it auto-resolves from the payload
client = e2a.E2AClient(api_key="e2a_...")  # or set E2A_API_KEY env var

# For single-agent setups, you can also set it explicitly:
# client = e2a.E2AClient(api_key="e2a_...", agent_email="bot@agents.e2a.dev")

@app.post("/webhook")
async def webhook(request: Request):
    # parse_webhook does parse + HMAC-verify in one call.
    # Reads E2A_WEBHOOK_SECRET from the env automatically — make sure that's
    # set or you'll get a ValueError on the first request.
    try:
        email = client.parse_webhook(await request.body())
    except PermissionError:
        raise HTTPException(401, "bad signature")

    print(f"From: {email.sender}")
    print(f"Subject: {email.subject}")
    print(f"Body: {email.text_body}")

    # Reply — agent_email auto-resolves from email.recipient
    email.reply("Thanks for your email!")

    return {"ok": True}
```

#### TypeScript

Install the TypeScript SDK:

```bash
npm install @e2a/sdk
```

Example webhook handler using Express:

```typescript
import { E2AClient } from "@e2a/sdk";
import express from "express";

const app = express();
app.use(express.json());

// Webhook mode: agentEmail is optional — it auto-resolves from email.recipient.
const client = new E2AClient({ apiKey: process.env.E2A_API_KEY! });

// For single-agent setups, you can also set it explicitly:
// const client = new E2AClient({ apiKey: process.env.E2A_API_KEY!, agentEmail: "bot@agents.e2a.dev" });

app.post("/webhook", async (req, res) => {
  // parseWebhook does parse + HMAC-verify in one call.
  // Reads E2A_WEBHOOK_SECRET from the env automatically.
  let email;
  try {
    email = await client.parseWebhook(req.body);
  } catch {
    return res.status(401).end();
  }

  console.log(`From: ${email.sender}`);
  console.log(`Subject: ${email.subject}`);
  console.log(`Body: ${email.textBody}`);

  // Reply — agent_email auto-resolves from email.recipient
  await email.reply("Thanks for your email!");

  res.json({ ok: true });
});

app.listen(3000);
```

The webhook receives a JSON payload with these fields:

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

`to` and `cc` are the parsed `To:` / `Cc:` headers from the original message; `recipient` is this delivery's per-agent target. `client.parse_webhook()` handles HMAC verification and decoding, and gives you `email.subject`, `email.text_body`, `email.html_body`, `email.attachments`, `email.to`, `email.cc`, and `email.reply()`.

The SDK gates field access behind verification — accessing `email.sender` etc. on an unverified payload raises `UnverifiedEmailError`. `parse_webhook` handles the verify step for you; `client.parse(...)` returns an unverified email, and you must call `email.verify_signature()` before reading claim fields. Check `email.is_verified` if you want to confirm the sender's identity was verified by e2a.

For full SDK documentation, see https://e2a.dev/python-sdk.

## Commands

```bash
# Log in — opens browser to authenticate, saves api_key and agent_email to ~/.e2a/config.json
# SDKs automatically read E2A_API_KEY from the environment, or you can pass the key from config directly
e2a login

# Check inbox
e2a inbox

# Check inbox for a specific agent (if you have multiple)
e2a inbox --agent <agent-email>

# Read a specific message
e2a read <message-id>

# Reply to a message
e2a reply <message-id> --body "your reply"

# Send a new email
e2a send --to <recipient> --subject "subject" --body "body"

# List custom domains
e2a domains list

# Register and verify a custom domain
e2a domains register <domain>
e2a domains verify <domain>
```

The `--agent` flag is optional if `agent_email` is set in your config.

## Reference

- Config file: `~/.e2a/config.json` (JSON with `api_key`, `api_url`, `agent_email`, `shared_domain`)
- Environment variables that override config: `E2A_API_KEY`, `E2A_URL`, `E2A_SHARED_DOMAIN` (CLI). `E2A_AGENT_EMAIL` is honored by the Python/TS SDK constructors when you don't pass `agent_email` explicitly. `E2A_WEBHOOK_SECRET` is read by `client.parse_webhook` / `client.parseWebhook` and `email.verify_signature()` / `email.verifySignature()` to verify inbound webhook signatures.
- CLI docs: https://www.npmjs.com/package/@e2a/cli
- TypeScript SDK: https://www.npmjs.com/package/@e2a/sdk
- Python SDK: https://e2a.dev/python-sdk
- e2a docs: https://e2a.dev/docs

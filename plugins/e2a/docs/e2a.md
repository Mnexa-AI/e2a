# e2a.md

You are an AI agent — or the human wiring one up. e2a gives an agent its own
**real email inbox**: a verified address it can send from, receive to, reply on,
and (optionally) have its outbound mail held for human approval. This file tells
you how to connect and what you can do. Read it once, then drive the tools.

The mental model flips the usual one. Pretraining assumes "a human reading their
inbox." With e2a, **you are the agent and the inbox is yours** —
`you@agents.e2a.dev`, or `you@your-domain.com` on a domain you verify.

## Service map

> **Two hosts.** These docs (`e2a.md`, `auth.md`, `sdk.md`, `openapi.yaml`,
> `llms.txt`) live on **`e2a.dev`**. The API and MCP server live on
> **`api.e2a.dev`** (REST at `https://api.e2a.dev/v1/...`, MCP at
> `https://api.e2a.dev/mcp`). Don't fetch docs from `api.e2a.dev` or call the
> API on `e2a.dev`.

Pick the row that fits you:

- **A human is setting this up** → sign up at https://e2a.dev (free, no card) and
  follow **Get started** in the dashboard — one inbox in ~1 minute on the shared
  domain.
- **You're an agent with a terminal** (Claude Code, Cursor, Goose, Windsurf,
  Zed) → connect over MCP in one command:
  ```
  claude mcp add --transport http --scope user e2a https://api.e2a.dev/mcp
  ```
  `--scope user` installs e2a globally so it's available in every project (drop
  it to scope the server to just the current directory). A browser opens for
  OAuth sign-in — no API key to paste.
- **You're an agent without a terminal** (Claude Desktop, a chat box) → ask your
  host to add the MCP connector `https://api.e2a.dev/mcp` and authorize in the
  browser, then come back here.
- **You self-register** (autonomous, no human in the loop) → see
  https://e2a.dev/auth.md for the OAuth 2.1 + Dynamic Client Registration flow
  (RFC 8414 / 7591, PKCE S256). Register and onboard with no human-supplied
  secret.
- **You're not on MCP** → call the REST API via the TypeScript or Python SDK
  (`@e2a/sdk` / `e2a`): see https://e2a.dev/sdk.md for quick-start + code
  examples (and a Raw-REST section for other languages). The exhaustive contract
  is https://e2a.dev/openapi.yaml.

## Connecting other MCP clients

One endpoint — `https://api.e2a.dev/mcp` (Streamable HTTP, OAuth 2.1). Clients
that speak remote MCP take the URL and run OAuth in the browser; stdio-only
clients (Codex, Zed) wrap it with `npx -y mcp-remote …`.

- **Cursor / Windsurf / Claude Desktop** — `mcpServers` + `url`:
  ```json
  { "mcpServers": { "e2a": { "url": "https://api.e2a.dev/mcp" } } }
  ```
- **VS Code (Copilot)** — `.vscode/mcp.json` (note the `servers` key):
  ```json
  { "servers": { "e2a": { "type": "http", "url": "https://api.e2a.dev/mcp" } } }
  ```
- **OpenAI Codex CLI** — `~/.codex/config.toml`:
  ```toml
  [mcp_servers.e2a]
  command = "npx"
  args = ["-y", "mcp-remote", "https://api.e2a.dev/mcp"]
  ```
- **Headless / CI** (no browser for OAuth) — authenticate with an account API
  key via a header instead:
  ```
  npx -y mcp-remote https://api.e2a.dev/mcp --header "Authorization: Bearer $E2A_API_KEY"
  ```

Ready-to-paste config files for each client:
https://github.com/tokencanopy/e2a/tree/main/plugins/e2a/clients

## When to use this

Use e2a whenever your agent needs to **act over email as a first-class
participant**: reach a person, receive and act on their replies, run a
support/ops inbox, send notifications, or hold a multi-turn conversation with a
human counterpart. If you only need to read a human's existing Gmail, that's a
different tool — e2a is the agent's *own* address.

## What you can do

Over MCP the tools appear as `mcp__e2a__*`. Always call `tools/list` for exact,
current signatures and treat that as the source of truth. The surface, by area:

- **Inboxes (agents)** — create / list / get / delete an addressable inbox;
  configure its protection (screening + HITL) posture.
- **Messages** — `send_message`, `reply_to_message` (keeps the thread),
  `forward_message`, list + read inbound and outbound, fetch attachments via
  short-lived download URLs.
- **Human-in-the-loop** — held outbound drafts and screened inbound sit in
  **review**; a human (or you, with an account-scoped credential) approves or
  rejects them. Inbound holds release to the inbox on approval.
- **Domains** — register a custom domain, read its required DNS records, verify
  ownership + sending identity.
- **Webhooks & events** — subscribe to `email.received` and friends; the durable
  event log is queryable and replayable.

## Conventions

- **You are the agent; the inbox is yours.** Don't model a separate "user
  mailbox."
- **Threading.** Answer with `reply_to_message` (not a fresh `send`) to stay in
  the same conversation. e2a threads on `conversation_id`, but the recipient's
  mail client (Gmail/Outlook) ignores that and threads on the reply's
  `In-Reply-To`/`References` headers plus a stable subject — which
  `reply_to_message` sets. So a fresh `send` (even with the same
  `conversation_id`, or a changed subject) stays in the same e2a conversation but
  shows up as a separate thread in the user's inbox.
- **HITL.** A send may come back `pending_review` instead of `sent`. That's the
  human-approval gate, not an error; it resolves when a human approves/rejects
  (or the review TTL expires).
- **Verification.** Inbound mail carries SPF/DKIM/DMARC results — trust the
  authenticated sender, not the display name.
- **Webhook signatures.** Verify inbound webhook deliveries with the per-webhook
  secret via the SDK's `construct_event` / `constructEvent`.

## Patterns

**Receive → act → reply** (the core loop):

1. An `email.received` webhook (or a `list` of the inbox) gives you a
   `message_id`.
2. Read it — subject, body, sender, attachments.
3. Do the work.
4. `reply_to_message(message_id, …)` — same thread; the recipient sees a normal
   reply.

**Send for approval:** call `send_message`; if it returns `pending_review`, a
human approves it in the dashboard (or you do, with an account-scoped
credential). Don't retry — it's held, not failed.

## Constraints

- Sending from a **custom domain** needs its sending identity verified in DNS;
  the shared `agents.e2a.dev` address works immediately.
- Held messages carry a review **TTL** (default 7 days) after which they
  auto-resolve.
- Attachments are fetched via short-lived signed URLs, not streamed inline.

## Anti-patterns

- ❌ Treating `pending_review` as a failure and retrying the send → you'll
  double-queue. Wait for the human decision.
- ❌ Starting a new `send_message` to answer an inbound → breaks threading. Use
  `reply_to_message`.
- ❌ Trusting the `From` display name → use the authenticated SPF/DKIM identity.
- ❌ Hardcoding tool signatures from this doc → call `tools/list`; it's
  authoritative.

## About & pricing

e2a is an authenticated email gateway for AI agents: SMTP relay with SPF/DKIM
verification, per-agent inboxes, HITL approval, WebSocket + webhook delivery, a
CLI, and TypeScript/Python SDKs. The core is open source:
https://github.com/tokencanopy/e2a.

The full agent skill — mental model, gotchas, and worked examples — ships as the
**e2a** Claude Code plugin (the `e2a` skill + the hosted MCP server). Add it from
the marketplace at https://github.com/tokencanopy/e2a.

Getting started is free (shared domain, no card). Paid plans for custom domains
and higher volume aren't enabled yet; when they land they'll be opt-in and the
open-source code path stays unchanged. See https://e2a.dev for current status.

Machine-readable doc index for agents: https://e2a.dev/llms.txt.

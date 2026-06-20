# e2a CLI

A thin developer convenience for [e2a](https://e2a.dev) — email for AI agents.

The CLI deliberately covers only what the other surfaces don't do ergonomically:
**browser login** and **real-time inbound streaming** (with a local forward proxy
for testing webhook handlers). For everything else, use the surface built for it:

| Task | Use |
|---|---|
| Drive an agent (read/send/reply/list/labels) | the **MCP tools** or the **SDK** (`@e2a/sdk`, `e2a`) |
| Manage domains, agents, webhooks, API keys, HITL | the **web dashboard** |
| React to inbound mail in production | **webhooks** (public URL) or `client.listen()` (SDK) |

## Install

```bash
npm install -g @e2a/cli
# or, without installing:
npx @e2a/cli login
```

## Commands

### `e2a login`

Open a browser login and save your API key + default agent to `~/.e2a/config.json`.

```bash
e2a login
```

### `e2a listen`

Stream inbound email for an agent over WebSocket in real time. The connection is
outbound, so it works from behind NAT — the simplest way for a **local** agent to
get push delivery without a public webhook URL.

```bash
e2a listen --agent bot@acme.com
# [10:30:15] From: alice@example.com | Subject: Meeting tomorrow

# --forward bridges each message to a local HTTP handler (the
# `stripe listen --forward-to` pattern) — ideal for developing a webhook
# handler locally without exposing a public URL:
e2a listen --agent bot@acme.com --forward http://localhost:3000/inbound
e2a listen --agent bot@acme.com --forward http://localhost:3000/inbound --forward-token <secret>

# Raw JSON notifications (one per line) for piping:
e2a listen --agent bot@acme.com --json
```

`--agent` falls back to the `agent_email` saved in config.

### `e2a config`

View or update the local config (`~/.e2a/config.json`).

```bash
e2a config list
e2a config get agent_email
e2a config set agent_email bot@acme.com
```

## Options

- `--help` — show help
- `--version` — show version

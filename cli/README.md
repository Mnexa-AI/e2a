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

Open a browser login and save your API key + default agent to `~/.e2a/config.json`
(also caches the deployment's shared mail domain, discovered from `GET /v1/info`).

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
# handler locally without exposing a public URL. Each message is POSTed as
# the full v1 MessageView JSON (SDK camelCase: messageId, createdAt, …):
e2a listen --agent bot@acme.com --forward http://localhost:3000/inbound

# --forward-token adds an `Authorization: Bearer <token>` header to the POST:
e2a listen --agent bot@acme.com --forward http://localhost:3000/inbound --forward-token <secret>

# Emit the full message as JSON (one object per line) for piping:
e2a listen --agent bot@acme.com --json
```

`--agent` falls back to the `agent_email` saved in config.

The server keeps **one WebSocket connection per agent**. If another listener
for the same agent connects (a second `e2a listen`, or an SDK
`client.listen()` elsewhere), this one is superseded: it prints a
`listener replaced` explanation and exits `5` instead of reconnecting —
auto-reconnecting would steal the socket back from the newer listener and
loop.

#### OpenAI Responses auto-reply

When the `--forward <url>` path ends in `/v1/responses`, `listen` switches to
**auto-reply mode**: it formats each inbound email as an OpenAI
[Responses API](https://platform.openai.com/docs/api-reference/responses) request,
POSTs it, and sends the model's output text back as a reply in the thread. Use
`--forward-token` for the model endpoint's bearer token.

```bash
e2a listen --agent bot@acme.com \
  --forward http://localhost:18789/v1/responses \
  --forward-token <token>
```

### `e2a config`

View or update the local config (`~/.e2a/config.json`).

```bash
e2a config list
e2a config get agent_email
e2a config set agent_email bot@acme.com
```

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `E2A_API_KEY` | — | API key. Skips `e2a login` — useful in CI and scripts |
| `E2A_URL` | `https://e2a.dev` | The e2a deployment root. Set for self-host |
| `E2A_AGENT_EMAIL` | — | Default sending/listening inbox (what `--agent` overrides) |
| `E2A_SHARED_DOMAIN` | auto-discovered | Force the shared domain instead of discovering it via `GET /v1/info` |

**Precedence:** command-line flags beat environment variables, which beat
`~/.e2a/config.json`, which beats the defaults above.

`E2A_URL` is the deployment root — the host that serves the `e2a login` browser
flow and `/get-started`, and proxies the `/v1` API. It is *not* the SDKs'
`E2A_API_URL`, which names the API host alone; pointing the CLI at an API host
breaks `e2a login`. The CLI does not read `E2A_API_URL` or the SDKs' older
`E2A_BASE_URL`.

Values that come from the environment are **not** written back to
`~/.e2a/config.json` — so with `E2A_URL` exported, `e2a config set api_url …`
has no effect until you unset it.

## Options

- `--help`, `-h` — show help
- `--version`, `-v` — show version

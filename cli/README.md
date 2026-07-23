# e2a CLI

A thin developer convenience for [e2a](https://e2a.dev) — email for AI agents.

The CLI is two things in one: a developer convenience (**browser login** and
**real-time inbound streaming**, with a local forward proxy for testing webhook
handlers) and the **scripting surface** — `whoami`/`send`/`reply`/`messages` are
stateless primitives for shell-based harnesses (skills, hooks, CI), with a
documented, frozen exit-code contract (see [Exit codes](#exit-codes) below) so
scripts can branch on the process exit status instead of parsing JSON. For
interactive, stateful agent work (an MCP client or a long-running process),
use the **MCP tools** or the **SDK** (`@e2a/sdk`, `e2a`) instead.

| Task | Use |
|---|---|
| Script a send/reply/read from a shell harness, with exit codes | the **CLI** (`send`, `reply`, `messages`, `whoami`) |
| Drive an agent interactively (MCP client, long-running process) | the **MCP tools** or the **SDK** (`@e2a/sdk`, `e2a`) |
| Manage domains, webhooks, HITL review queues | the **web dashboard**, MCP tools, or SDK |
| React to inbound mail in production | **webhooks** (public URL) or `client.listen()` (SDK) |

## Install

```bash
npm install -g @e2a/cli
# or, without installing:
npx @e2a/cli login
```

## Commands

### `e2a login`

Open a browser login and save an account-scoped API key to `~/.e2a/config.json`
(also caches the deployment's shared mail domain, discovered from `GET /v1/info`).

```bash
e2a login
```

On a headless machine, set `E2A_API_KEY` instead of running `login`. To persist
that key locally, use `e2a config set api_key <key>`; `e2a whoami` validates it.

Login does **not** set a default sending inbox. The key is account-scoped — it
spans every inbox on the account — so the CLI never guesses which one you meant.
Commands that send or read mail take `--agent <email>`; to avoid passing it every
time, set a default explicitly:

```bash
e2a agents list
e2a config set agent_email bot@acme.com   # or export E2A_AGENT_EMAIL
```

Without one, those commands exit `2` (usage) rather than picking an inbox for you.
A default you set this way survives re-login.

Need a least-privilege key bound to a single inbox? Mint one after logging in:

```bash
e2a keys create --agent bot@acme.com
```

### `e2a whoami`

Show the key identity: user, scope, bound agent, plan.

```bash
e2a whoami
e2a whoami --json
```

### `e2a agents`

Manage inboxes (requires an account-scoped key).

```bash
e2a agents list
e2a agents create bot@acme.com --name "Support bot"
e2a agents get bot@acme.com
```

`list`, `create`, and `get` all accept `--json` (print the raw JSON response).

### `e2a keys`

Mint, list, and revoke API keys (requires an account-scoped key).

```bash
e2a keys create --agent bot@acme.com --name "prod key"   # bound, least-privilege
                                                            # (plaintext printed once)
e2a keys list
e2a keys delete <key-id>
```

`create` and `list` accept `--json` (print the raw JSON response).

### `e2a protection`

Show or update an agent's protection (screening/review) config.

```bash
e2a protection get bot@acme.com
e2a protection set bot@acme.com --outbound-review off   # sends go out unheld
e2a protection set bot@acme.com --inbound-review off     # inbound delivered unheld
e2a protection set bot@acme.com --suppress-notifications on
```

`get` and `set` accept `--json` (print the raw JSON response).

### `e2a send` / `e2a reply`

Send an email as the agent, or reply in-thread. Together with `whoami` and
`messages`, these are the stateless scripting primitives — see
[Exit codes](#exit-codes).

```bash
e2a send --to alice@example.com --subject "Hi" --body "Plain-text body." \
  --agent bot@acme.com
e2a send --to alice@example.com --subject "Hi" --html-file body.html \
  --attach report.pdf --conversation-id conv_123 --idempotency-key <uuid>
e2a reply msg_abc123 --body "On it." --agent bot@acme.com
```

Common `send`/`reply` flags: `--to` (repeatable), `--subject`, `--body` /
`--body-file`, `--html-file` (text fallback derived if no `--body`), `--attach`
(repeatable; max 10 files, 10 MB each, 25 MB total), `--conversation-id`
(alias `--conversation`), `--reply-to`, `--idempotency-key`, `--agent`, `--json`
(print the full send result).

### `e2a messages`

List or fetch messages for an agent.

```bash
e2a messages list --agent bot@acme.com --direction inbound --read-status unread
e2a messages list --agent bot@acme.com --since 2026-07-01T00:00:00Z --json
e2a messages get msg_abc123 --agent bot@acme.com --text
e2a messages lifecycle msg_abc123 --agent bot@acme.com --json   # beta
```

`list` flags: `--direction` (`inbound`/`outbound`/`all`), `--since` (inclusive
ISO timestamp), `--conversation` (alias `--conversation-id`), `--read-status`
(`unread`/`read`/`all`, default `all`), `--limit`, `--agent`, `--json` (NDJSON
instead of TSV). `get` flags: `--text` (print parsed body text only),
`--agent`, `--json` (print the full message as JSON). `lifecycle` (beta) shows
a message's observed lifecycle transitions; flags: `--cursor` (continue from a
prior page), `--limit` (page size, 1–100), `--agent`, `--json` (print the
canonical lifecycle page as JSON).

### `e2a listen`

Stream inbound email for an agent over WebSocket in real time. The connection is
outbound, so it works from behind NAT — the simplest way for a **local** agent to
get push delivery without a public webhook URL.

```bash
e2a listen --agent bot@acme.com
# [10:30:15] Claimed From: alice@example.com | DMARC: pass (verified domain: example.com) | Subject: Meeting tomorrow

# --forward bridges each message to a local HTTP handler (the
# `stripe listen --forward-to` pattern) — ideal for developing a webhook
# handler locally without exposing a public URL. Each message is POSTed as
# the full v1 MessageView JSON (SDK camelCase: headerFrom, authentication, …):
e2a listen --agent bot@acme.com --forward http://localhost:3000/inbound

# --forward-token adds an `Authorization: Bearer <token>` header to the POST:
e2a listen --agent bot@acme.com --forward http://localhost:3000/inbound --forward-token <secret>

# Emit the full message as JSON (one object per line) for piping:
e2a listen --agent bot@acme.com --json

# Only messages in one conversation:
e2a listen --agent bot@acme.com --conversation conv_123

# Exit after the first (matching) message, or TIMEOUT (exit 6) if none arrives
# by the deadline — useful for a script waiting on one reply:
e2a listen --agent bot@acme.com --once --until 2026-07-18T13:00:00Z --text
```

`--agent` falls back to the `agent_email` saved in config.

Note: `listen --once --text` / `--json` fetches the message via the API GET,
which marks it as read (same side effect as `messages get`).

The server keeps **one WebSocket connection per agent**. If another listener
for the same agent connects (a second `e2a listen`, or an SDK
`client.listen()` elsewhere), this one is superseded: it prints a
`listener replaced` explanation and exits `5` instead of reconnecting —
auto-reconnecting would steal the socket back from the newer listener and
loop.

`listen` also participates in the exit-code contract below: a long-running
listen (no `--once`) exits `1` whenever the stream actually ends, such as
after a peer's normal WebSocket close (code 1000). Deploy drains use close code
1001 and reconnect with backoff, so they do not end the stream. A supervisor
(`systemd Restart=on-failure`, a retry loop) should treat exit `1` as
"restart me," not "stopped on purpose." Under `--once`, a forward that never
reaches the `--forward` endpoint also exits `1` even though the message itself
was printed to stdout — the message was consumed off the stream, so a silent
exit `0` would read as a successful hand-off to a harness when it wasn't.

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

Only `api_key` and `agent_email` are user-settable. Deployment URL, shared
domain, and cached key scope are managed by login or environment variables.

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

Environment variables take precedence over stored `api_key` and `agent_email`
values until they are unset. Deployment URL and shared-domain overrides are
environment-only (`E2A_URL` and `E2A_SHARED_DOMAIN`).

## Options

- `--help`, `-h` — show help
- `--version`, `-v` — show version

## Exit codes

`whoami`, `send`, `reply`, `messages`, and `listen` publish a stable, frozen
exit-code contract (`cli/src/exit.ts`) so shell harnesses can branch on the
process exit status instead of parsing JSON. Codes are never renumbered —
only added to.

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Transient error (network / 5xx / rate limit) — retry may help |
| `2` | Usage error (bad flags or arguments) |
| `3` | Send held for review (`pending_review`) — HTTP-successful but not delivered |
| `4` | Bad credentials or wrong key scope |
| `5` | Permanent request error (not found / invalid / conflict) — do not retry |
| `6` | Bounded wait (`listen --once --until`) expired with no matching message |
| `7` | A persisted send failed or returned an unrecognized outcome — do not retry; inspect the returned message id |

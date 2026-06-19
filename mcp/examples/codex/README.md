# OpenAI Codex CLI × e2a

[Codex CLI](https://github.com/openai/codex) is OpenAI's terminal coding agent (peer to Claude Code). It speaks MCP natively — you declare servers in `~/.codex/config.toml` and Codex connects to them at session start, then surfaces their tools to whatever model you're running.

Unlike the [LangChain](../langchain/), [Google ADK](../adk/), and [OpenAI Agents SDK](../openai-agents/) examples — which are Python scripts you run — Codex is itself the agent. The "example" here is the TOML you paste into your Codex config to add e2a's 33 MCP tools.

Two transport options, both first-class in Codex:

- **Local stdio** — Codex spawns `npx -y @e2a/mcp-server` as a child process. Simplest for laptop dev; needs Node 18+ on PATH.
- **Hosted Streamable HTTP** — Codex opens a Streamable HTTP session to `https://mcp.e2a.dev/mcp` with your API key in the `Authorization: Bearer …` header. Pick this when you don't want a Node toolchain on the host (CI runners, dev containers, remote app server, etc.).

## Prerequisites

- [Codex CLI](https://github.com/openai/codex) 0.133+ (`npm i -g @openai/codex` or `brew install --cask codex`)
- An [e2a API key](https://e2a.dev), exported as `E2A_API_KEY`
- For the **local stdio** variant only: Node 18+ (Codex shells out to `npx -y @e2a/mcp-server`)

## Install (the TOML way)

Open `~/.codex/config.toml` (Codex creates it on first run; `mkdir -p ~/.codex && touch ~/.codex/config.toml` if it doesn't exist yet) and paste **one** of the blocks from [`config.toml.example`](./config.toml.example):

```toml
# Local stdio
[mcp_servers.e2a]
command = "npx"
args    = ["-y", "@e2a/mcp-server"]

[mcp_servers.e2a.env]
E2A_API_KEY = "e2a_..."
```

```toml
# Hosted
[mcp_servers.e2a-hosted]
url                  = "https://mcp.e2a.dev/mcp"
bearer_token_env_var = "E2A_API_KEY"
```

For the hosted variant, export the key in your shell so Codex can read it at session start:

```bash
export E2A_API_KEY=e2a_...
```

## Install (the CLI way)

Codex ships first-class commands that write the same TOML for you:

```bash
# Local stdio
codex mcp add e2a --env E2A_API_KEY=e2a_... -- npx -y @e2a/mcp-server

# Hosted
codex mcp add e2a-hosted \
  --url https://mcp.e2a.dev/mcp \
  --bearer-token-env-var E2A_API_KEY
```

Verify with `codex mcp list` (add `--json` for machine-readable output) or `codex doctor`, which reports configured servers and surfaces missing env vars.

## Run

```bash
codex
```

Then in the session:

> What's in my inbox?
> Reply to the latest message politely.
> Send an email to alice@example.com — subject "test", body "hello from Codex".

If your e2a account has exactly one agent, the hosted endpoint auto-resolves it at session init — no `E2A_AGENT_EMAIL` needed. With multiple agents, ask Codex to pass `agent_email` per tool call, or set `E2A_AGENT_EMAIL` in `[mcp_servers.e2a.env]` for the stdio variant.

## How it works

Codex reads MCP server definitions from `~/.codex/config.toml` under `[mcp_servers.<id>]`. The transport is inferred from which fields you set:

- `command` (with optional `args` / `env` / `env_vars` / `cwd`) → stdio transport.
- `url` (with optional `bearer_token_env_var` / `http_headers` / `env_http_headers` / `oauth*`) → Streamable HTTP transport.

Setting both `command` and `url` in the same entry is rejected at load time, as is inline `bearer_token = "..."` — secrets must come from an env var via `bearer_token_env_var`. Codex also supports OAuth (`oauth.client_id`, `oauth_resource`, `scopes`) for the HTTP transport, but e2a's hosted server uses static API keys, so a bearer env var is the right choice.

## Pointing at a self-hosted e2a

- **Stdio**: set `E2A_URL` inside `[mcp_servers.e2a.env]` (or pass `--env E2A_URL=…` to `codex mcp add`). `@e2a/mcp-server` reads it. (`E2A_BASE_URL` is the legacy name and still accepted.)
- **Hosted**: change the `url` literal in the `[mcp_servers.e2a-hosted]` block to your deployment's MCP endpoint.

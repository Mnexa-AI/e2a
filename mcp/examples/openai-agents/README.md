# OpenAI Agents SDK × e2a

[OpenAI Agents SDK](https://openai.github.io/openai-agents-python/) `Agent` driving the e2a [MCP server](https://www.npmjs.com/package/@e2a/mcp-server).

Two transport options:

- **`agent.py`** — `MCPServerStdio` spawns the MCP server locally via `npx -y @e2a/mcp-server`. Simplest for laptop dev; needs a Node toolchain.
- **`agent_hosted.py`** — `MCPServerStreamableHttp` talks to the hosted endpoint at `https://mcp.e2a.dev/mcp`. Pick this when deploying to serverless runtimes (Cloud Run, Lambda) where spawning a stdio child process is awkward or impossible, or when you don't want a Node toolchain on the agent host.

## Prerequisites

- Python 3.10+
- An [e2a API key](https://e2a.dev)
- An [OpenAI API key](https://platform.openai.com/api-keys)
- For `agent.py` only: Node 18+ (the script shells out to `npx -y @e2a/mcp-server`)

## Run (local stdio)

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export E2A_AGENT_EMAIL=your-bot@your-domain.com   # optional default inbox
export OPENAI_API_KEY=sk-…

python agent.py "what's in my inbox?"
python agent.py "reply to the most recent message politely"
```

## Run (hosted)

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export OPENAI_API_KEY=sk-…

python agent_hosted.py "what's in my inbox?"
```

If your account has exactly one agent, the hosted endpoint auto-resolves it at session init — no `E2A_AGENT_EMAIL` needed. With multiple agents, pass `agent_email` per tool call.

## How it works

`MCPServerStdio` / `MCPServerStreamableHttp` connect to the e2a tool surface, the `Agent` picks them up as its toolset, and `Runner.run` drives the loop. The async-context-manager wrapper ensures the subprocess (stdio) or HTTP client (hosted) is cleaned up when the run finishes.

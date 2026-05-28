# CrewAI × e2a

Single-agent CrewAI crew that drives the e2a [MCP server](https://www.npmjs.com/package/@e2a/mcp-server) via [`crewai-tools`](https://github.com/crewAIInc/crewAI-tools)' `MCPServerAdapter`. Picks up the e2a tool surface and uses it to answer natural-language email tasks.

Two transport options:

- **`agent.py`** — runs the MCP server locally via `npx -y @e2a/mcp-server` (stdio). Simplest for laptop dev; needs a Node toolchain.
- **`agent_hosted.py`** — talks to the hosted endpoint at `https://mcp.e2a.dev/mcp` (Streamable HTTP). Pick this when deploying to serverless runtimes (Cloud Run, Lambda) where spawning a stdio child process is awkward or impossible, or when you don't want a Node toolchain on the agent host.

## Prerequisites

- Python 3.10+
- An [e2a API key](https://e2a.dev)
- An [Anthropic API key](https://console.anthropic.com/)
- For `agent.py` only: Node 18+ (the script shells out to `npx -y @e2a/mcp-server`)

## Run (local stdio)

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export E2A_AGENT_EMAIL=your-bot@your-domain.com   # optional default inbox
export ANTHROPIC_API_KEY=sk-ant-…

python agent.py "what's in my inbox?"
python agent.py "reply to the most recent message politely"
```

## Run (hosted)

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export ANTHROPIC_API_KEY=sk-ant-…

python agent_hosted.py "what's in my inbox?"
```

If your account has exactly one agent, the hosted endpoint auto-resolves it at session init — no `E2A_AGENT_EMAIL` needed. With multiple agents, pass `agent_email` per tool call.

## How it works

`MCPServerAdapter` connects to either a stdio child process (via `StdioServerParameters`) or a Streamable HTTP endpoint (via a dict with `transport: "streamable-http"` and a Bearer header). Inside the `with` block, the adapter yields one CrewAI tool per MCP tool — wired straight into the `Agent` so the crew can call them.

To swap models, change `"anthropic/claude-sonnet-4-6"` to any [LiteLLM-compatible model string](https://docs.litellm.ai/docs/providers) CrewAI accepts (CrewAI uses LiteLLM under the hood).

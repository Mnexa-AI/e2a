# CrewAI × e2a

Single-agent CrewAI crew that drives the hosted e2a [MCP server](https://api.e2a.dev/mcp) via [`crewai-tools`](https://github.com/crewAIInc/crewAI-tools)' `MCPServerAdapter`. Picks up the e2a tool surface and uses it to answer natural-language email tasks.

`agent.py` connects to the hosted endpoint at `https://api.e2a.dev/mcp` (Streamable HTTP) with your API key in the `Authorization` header.

## Prerequisites

- Python 3.10+
- An [e2a API key](https://e2a.dev)
- An [Anthropic API key](https://console.anthropic.com/)

## Run

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export ANTHROPIC_API_KEY=sk-ant-…

python agent.py "what's in my inbox?"
python agent.py "reply to the most recent message politely"
```

If your credential resolves a single agent, the hosted endpoint scopes tools to it server-side. With an account-scoped key and multiple agents, pass `agent_email` per tool call.

## How it works

`MCPServerAdapter` connects to the hosted Streamable HTTP endpoint (via a dict with `transport: "streamable-http"` and a Bearer header). Inside the `with` block, the adapter yields one CrewAI tool per MCP tool — wired straight into the `Agent` so the crew can call them.

To swap models, change `"anthropic/claude-sonnet-4-6"` to any [LiteLLM-compatible model string](https://docs.litellm.ai/docs/providers) CrewAI accepts (CrewAI uses LiteLLM under the hood).

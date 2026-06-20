# OpenAI Agents SDK × e2a

[OpenAI Agents SDK](https://openai.github.io/openai-agents-python/) `Agent` driving the hosted e2a [MCP server](https://api.e2a.dev/mcp).

`agent.py` wires `MCPServerStreamableHttp` to the hosted endpoint at `https://api.e2a.dev/mcp` with your API key in the `Authorization` header.

## Prerequisites

- Python 3.10+
- An [e2a API key](https://e2a.dev)
- An [OpenAI API key](https://platform.openai.com/api-keys)

## Run

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export OPENAI_API_KEY=sk-…

python agent.py "what's in my inbox?"
python agent.py "reply to the most recent message politely"
```

If your credential resolves a single agent, the hosted endpoint scopes tools to it server-side. With an account-scoped key and multiple agents, pass `agent_email` per tool call.

## How it works

`MCPServerStreamableHttp` connects to the hosted e2a tool surface, the `Agent` picks them up as its toolset, and `Runner.run` drives the loop. The async-context-manager wrapper ensures the HTTP client is cleaned up when the run finishes.

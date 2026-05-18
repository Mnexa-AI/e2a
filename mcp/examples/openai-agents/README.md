# OpenAI Agents SDK × e2a

[OpenAI Agents SDK](https://openai.github.io/openai-agents-python/) `Agent` driving [@e2a/mcp-server](https://www.npmjs.com/package/@e2a/mcp-server) over stdio.

## Prerequisites

- Node 18+ (`npx -y @e2a/mcp-server`)
- Python 3.10+
- An [e2a API key](https://e2a.dev)
- An [OpenAI API key](https://platform.openai.com/api-keys)

## Run

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export E2A_AGENT_EMAIL=your-bot@your-domain.com   # optional default inbox
export OPENAI_API_KEY=sk-…

python agent.py "what's in my inbox?"
python agent.py "reply to the most recent message politely"
```

## How it works

`MCPServerStdio` spawns the e2a MCP server, the `Agent` lists all 11 tools as its toolset, and `Runner.run` drives the loop. The async-context-manager wrapper ensures the subprocess is cleaned up when the run finishes.

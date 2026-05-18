# Google ADK × e2a

Google ADK [`Agent`](https://google.github.io/adk-docs/) powered by Gemini that uses [@e2a/mcp-server](https://www.npmjs.com/package/@e2a/mcp-server) for all 11 e2a tools over stdio.

## Prerequisites

- Node 18+ (`npx -y @e2a/mcp-server`)
- Python 3.10+
- An [e2a API key](https://e2a.dev)
- A [Google AI Studio key](https://aistudio.google.com/apikey) for Gemini

## Run

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export E2A_AGENT_EMAIL=your-bot@your-domain.com   # optional default inbox
export GOOGLE_API_KEY=…

adk web              # opens the ADK Web UI on http://localhost:8000
# or:
adk run agent.py     # interactive CLI
```

Then in the chat:

> What's in my inbox?
> Reply to the latest message politely.
> Send an email to alice@example.com — subject "test", body "hello from ADK".

## How it works

`McpToolset` spawns the e2a MCP server with `StdioConnectionParams`. ADK auto-discovers the tools and surfaces them to Gemini. Module-scope `root_agent` is the convention ADK's `run` / `web` look for.

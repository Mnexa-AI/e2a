# LangChain × e2a

LangGraph ReAct agent that drives [@e2a/mcp-server](https://www.npmjs.com/package/@e2a/mcp-server) over stdio via [`langchain-mcp-adapters`](https://github.com/langchain-ai/langchain-mcp-adapters). The agent picks up all 11 e2a tools and uses them to answer natural-language email tasks.

## Prerequisites

- Node 18+ (the agent shells out to `npx -y @e2a/mcp-server`)
- Python 3.10+
- An [e2a API key](https://e2a.dev)
- An [Anthropic API key](https://console.anthropic.com/)

## Run

```bash
pip install -r requirements.txt
export E2A_API_KEY=e2a_…
export E2A_AGENT_EMAIL=your-bot@your-domain.com   # optional default inbox
export ANTHROPIC_API_KEY=sk-ant-…

python agent.py "what's in my inbox?"
python agent.py "reply to the most recent message politely"
```

## How it works

`MultiServerMCPClient` spawns the e2a MCP server with the right env. `client.get_tools()` returns LangChain `BaseTool` objects, one per MCP tool. The ReAct agent treats them like any other LangChain tool.

To swap models, change `"anthropic:claude-sonnet-4-6"` to any provider:model string [`init_chat_model`](https://python.langchain.com/docs/how_to/chat_models_universal_init/) accepts.

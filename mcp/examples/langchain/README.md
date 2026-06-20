# LangChain × e2a

LangGraph ReAct agent that drives the hosted e2a [MCP server](https://api.e2a.dev/mcp) via [`langchain-mcp-adapters`](https://github.com/langchain-ai/langchain-mcp-adapters). Picks up the e2a tool surface and uses it to answer natural-language email tasks.

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

`MultiServerMCPClient` connects to the hosted Streamable HTTP endpoint with `transport: "streamable_http"` and a Bearer header. `client.get_tools()` returns LangChain `BaseTool` objects, one per MCP tool. The ReAct agent treats them like any other LangChain tool.

To swap models, change `"anthropic:claude-sonnet-4-6"` to any provider:model string [`init_chat_model`](https://python.langchain.com/docs/how_to/chat_models_universal_init/) accepts.

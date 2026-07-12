# Examples

End-to-end demos showing how to wire the e2a MCP surface into popular agent frameworks and CLIs.

| Framework | Path | LLM | Example |
| --- | --- | --- | --- |
| LangChain (LangGraph ReAct) | [langchain/](./langchain/) | Anthropic Claude | `agent.py` |
| CrewAI | [crewai/](./crewai/) | Anthropic Claude | `agent.py` |
| Google ADK | [adk/](./adk/) | Google Gemini | `agent.py` |
| OpenAI Agents SDK | [openai-agents/](./openai-agents/) | OpenAI GPT | `agent.py` |
| OpenAI Codex CLI | [codex/](./codex/) | Codex (OpenAI) | `[mcp_servers.e2a-hosted]` in `config.toml` |

## One hosted endpoint, same tool surface

Each example exercises the same e2a tool surface (37 tools; the visible set depends on your key's scope) against the hosted MCP server. The Python framework examples ship an `agent.py` script; the Codex example ships a TOML block (Codex is itself the agent, so you configure it instead of writing a script).

Every example connects to `https://api.e2a.dev/mcp` over Streamable HTTP with your API key in the `Authorization` header. No Node toolchain or local build is needed, and updates land without rebuilding the agent's image — so it works equally well on a laptop or on serverless runtimes (Cloud Run, Lambda, Vercel).

Bring your own [e2a API key](https://e2a.dev) and an LLM key for whichever framework you're trying.

## Things to try once a demo is running

- `what's in my inbox?` — exercises `list_messages` + `get_message`
- `reply to the most recent message politely` — exercises `reply_to_message` (preserves threading headers)
- `who am I?` — exercises `whoami`
- `what's waiting for my approval?` — exercises `list_reviews` (works once you've enabled HITL on your agent)

## Pointing at a self-hosted e2a

The examples are pinned to `https://api.e2a.dev/mcp` — edit the `url` literal in `agent.py` (or in the Codex `[mcp_servers.e2a-hosted]` block) if you run your own MCP server.

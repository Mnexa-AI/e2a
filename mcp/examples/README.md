# Examples

End-to-end demos showing how to wire `@e2a/mcp-server` into popular agent frameworks. Each is a self-contained script — clone the repo, install requirements, run.

| Framework | Path | LLM |
| --- | --- | --- |
| LangChain (LangGraph ReAct) | [langchain/](./langchain/) | Anthropic Claude |
| Google ADK | [adk/](./adk/) | Google Gemini |
| OpenAI Agents SDK | [openai-agents/](./openai-agents/) | OpenAI GPT |

All examples consume the published [@e2a/mcp-server](https://www.npmjs.com/package/@e2a/mcp-server) from npm via `npx -y` — no local build needed. Bring your own [e2a API key](https://e2a.dev) and an LLM key for whichever framework you're trying.

## Things to try once a demo is running

- `what's in my inbox?` — exercises `list_messages` + `get_message`
- `reply to the most recent message politely` — exercises `reply_to_message` (preserves threading headers)
- `who am I?` — exercises `whoami`
- `what's waiting for my approval?` — exercises `list_pending_messages` (works once you've enabled HITL on your agent)

## Pointing at a self-hosted e2a

Set `E2A_BASE_URL=https://your-deployment.example.com` in the environment and the examples forward it to the MCP server automatically.

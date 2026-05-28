# Examples

End-to-end demos showing how to wire the e2a MCP surface into popular agent frameworks and CLIs.

| Framework | Path | LLM | Stdio variant | Hosted variant |
| --- | --- | --- | --- | --- |
| LangChain (LangGraph ReAct) | [langchain/](./langchain/) | Anthropic Claude | `agent.py` | `agent_hosted.py` |
| CrewAI | [crewai/](./crewai/) | Anthropic Claude | `agent.py` | `agent_hosted.py` |
| Google ADK | [adk/](./adk/) | Google Gemini | `agent.py` | `agent_hosted.py` |
| OpenAI Agents SDK | [openai-agents/](./openai-agents/) | OpenAI GPT | `agent.py` | `agent_hosted.py` |
| OpenAI Codex CLI | [codex/](./codex/) | Codex (OpenAI) | `[mcp_servers.e2a]` in `config.toml` | `[mcp_servers.e2a-hosted]` in `config.toml` |

## Two transports, same tool surface

Each example exercises the same 18 e2a tools. The Python framework examples ship two scripts; the Codex example ships two TOML blocks (Codex is itself the agent, so you configure it instead of writing a script).

- **Stdio variant** consumes the published [@e2a/mcp-server](https://www.npmjs.com/package/@e2a/mcp-server) from npm via `npx -y` — no local build needed. Best for laptop / local dev.
- **Hosted variant** connects to `https://mcp.e2a.dev/mcp` over Streamable HTTP with your API key in the `Authorization` header. Best when:
  - Deploying to serverless runtimes (Cloud Run, Lambda, Vercel) where spawning a stdio child process is awkward or impossible.
  - You don't want a Node toolchain on the agent host.
  - You want updates to land without rebuilding the agent's image.

Bring your own [e2a API key](https://e2a.dev) and an LLM key for whichever framework you're trying.

## Things to try once a demo is running

- `what's in my inbox?` — exercises `list_messages` + `get_message`
- `reply to the most recent message politely` — exercises `reply_to_message` (preserves threading headers)
- `who am I?` — exercises `whoami`
- `what's waiting for my approval?` — exercises `list_pending_messages` (works once you've enabled HITL on your agent)

## Pointing at a self-hosted e2a

For the **stdio** variants: set `E2A_BASE_URL=https://your-deployment.example.com` in the environment and the examples forward it to the MCP server automatically. The hosted variants are pinned to `https://mcp.e2a.dev/mcp` — edit the `url` literal in `agent_hosted.py` (or in the Codex `[mcp_servers.e2a-hosted]` block) if you run your own MCP server.

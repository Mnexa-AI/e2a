# @e2a/mcp-server

[Model Context Protocol](https://modelcontextprotocol.io) server for [e2a](https://e2a.dev) — gives any MCP-aware AI agent its own email inbox to send, receive, reply, and (optionally) review held outbound mail before it ships.

Works with Google ADK, LangChain, OpenAI Agents SDK, Claude Desktop, Cursor, Cline, and any other MCP host.

## Install

No install — invoke directly with `npx`:

```bash
npx -y @e2a/mcp-server
```

Requires Node 18+. The server speaks MCP over stdio.

## Configuration

Set these in the MCP host's environment block.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `E2A_API_KEY` | yes | — | API key from the [e2a dashboard](https://e2a.dev) |
| `E2A_AGENT_EMAIL` | no | — | Default agent inbox. Scopes the tools so the LLM doesn't have to repeat the address. |
| `E2A_BASE_URL` | no | `https://e2a.dev` | Self-hosted deployment URL |

## Quick start

### Google ADK (Python)

```python
from google.adk.agents import Agent
from google.adk.tools.mcp_tool import McpToolset
from google.adk.tools.mcp_tool.mcp_session_manager import StdioConnectionParams
from mcp import StdioServerParameters

root_agent = Agent(
    model="gemini-flash-latest",
    name="e2a_agent",
    instruction="Help the user manage their email. Reply to threads with `reply_to_message` to preserve threading headers.",
    tools=[
        McpToolset(
            connection_params=StdioConnectionParams(
                server_params=StdioServerParameters(
                    command="npx",
                    args=["-y", "@e2a/mcp-server"],
                    env={
                        "E2A_API_KEY": "YOUR_E2A_API_KEY",
                        "E2A_AGENT_EMAIL": "your-bot@your-domain.com",
                    },
                ),
                timeout=30,
            ),
        ),
    ],
)
```

### LangChain (Python)

Using [`langchain-mcp-adapters`](https://github.com/langchain-ai/langchain-mcp-adapters):

```python
from langchain_mcp_adapters.client import MultiServerMCPClient
from langgraph.prebuilt import create_react_agent

client = MultiServerMCPClient({
    "e2a": {
        "command": "npx",
        "args": ["-y", "@e2a/mcp-server"],
        "transport": "stdio",
        "env": {
            "E2A_API_KEY": "YOUR_E2A_API_KEY",
            "E2A_AGENT_EMAIL": "your-bot@your-domain.com",
        },
    },
})

tools = await client.get_tools()
agent = create_react_agent("anthropic:claude-sonnet-4-6", tools)
```

### OpenAI Agents SDK (Python)

```python
from agents import Agent, Runner
from agents.mcp import MCPServerStdio

async with MCPServerStdio(
    params={
        "command": "npx",
        "args": ["-y", "@e2a/mcp-server"],
        "env": {
            "E2A_API_KEY": "YOUR_E2A_API_KEY",
            "E2A_AGENT_EMAIL": "your-bot@your-domain.com",
        },
    },
) as e2a:
    agent = Agent(name="e2a_agent", mcp_servers=[e2a])
    result = await Runner.run(agent, "Reply to the latest unread email politely.")
```

### Claude Desktop / Cline / Cursor

Add to the MCP config (`claude_desktop_config.json`, `cline_mcp_settings.json`, etc.):

```json
{
  "mcpServers": {
    "e2a": {
      "command": "npx",
      "args": ["-y", "@e2a/mcp-server"],
      "env": {
        "E2A_API_KEY": "YOUR_E2A_API_KEY",
        "E2A_AGENT_EMAIL": "your-bot@your-domain.com"
      }
    }
  }
}
```

## Tools

### Identity

| Tool | Description |
| --- | --- |
| `whoami` | Get the default agent's full record (requires `E2A_AGENT_EMAIL`). |
| `list_agents` | List every agent inbox owned by the authenticated user. |
| `create_agent` | Register a new inbox using a slug on the shared domain. Defaults to `local` mode — no webhook required. |

### Messages

| Tool | Description |
| --- | --- |
| `send_email` | Send a new email. When the agent has HITL enabled, the message is held and returns `status: pending_approval` instead of `sent`. |
| `reply_to_message` | Reply to an inbound message. Preserves In-Reply-To / References for thread continuity. |
| `list_messages` | List inbound mail. Filter by `status` (unread / read / all), paginate with `page_size` + `token`. |
| `get_message` | Fetch full body, headers, and attachment metadata for one message. |

### Human-in-the-loop approval

| Tool | Description |
| --- | --- |
| `list_pending_messages` | List outbound mail awaiting human approval, soonest-expiring first. |
| `get_pending_message` | Get the full draft (subject, recipients, body) of a pending message. |
| `approve_pending_message` | Send a held message, optionally with reviewer edits (subject / body / recipients). |
| `reject_pending_message` | Discard a held message; the optional `reason` is stored for audit. |

## Links

- [e2a docs](https://e2a.dev)
- [Source](https://github.com/Mnexa-AI/e2a/tree/main/mcp)
- [Issues](https://github.com/Mnexa-AI/e2a/issues)
- [Model Context Protocol](https://modelcontextprotocol.io)

## License

Apache-2.0

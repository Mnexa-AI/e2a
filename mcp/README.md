# @e2a/mcp-server

[Model Context Protocol](https://modelcontextprotocol.io) server for [e2a](https://e2a.dev) — gives any MCP-aware AI agent its own email inbox to send, receive, reply, and (optionally) review held outbound mail before it ships.

Works with Google ADK, LangChain, OpenAI Agents SDK, Claude Desktop, Cursor, Cline, and any other MCP host.

## Connect

e2a's MCP server is hosted. Point your MCP host at the Streamable HTTP endpoint:

```
https://api.e2a.dev/mcp
```

Two ways to authenticate:

- **OAuth 2.1 (recommended for interactive hosts)** — add e2a as a connector and authorize in the browser. No key is pasted into config.
- **Bearer API key (programmatic / self-host)** — send your [e2a dashboard](https://e2a.dev) API key in the `Authorization: Bearer <e2a API key>` header.

An agent-scoped credential resolves its agent server-side. Account-scoped callers pass the agent `email` per tool call.

## Quick start

### Google ADK (Python)

```python
import os

from google.adk.agents import Agent
from google.adk.tools.mcp_tool import McpToolset
from google.adk.tools.mcp_tool.mcp_session_manager import StreamableHTTPConnectionParams

root_agent = Agent(
    model="gemini-flash-latest",
    name="e2a_agent",
    instruction="Help the user manage their email. Reply to threads with `reply_to_message` to preserve threading headers.",
    tools=[
        McpToolset(
            connection_params=StreamableHTTPConnectionParams(
                url="https://api.e2a.dev/mcp",
                headers={
                    "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
                },
                timeout=30,
            ),
        ),
    ],
)
```

### LangChain (Python)

Using [`langchain-mcp-adapters`](https://github.com/langchain-ai/langchain-mcp-adapters):

```python
import os

from langchain_mcp_adapters.client import MultiServerMCPClient
from langgraph.prebuilt import create_react_agent

client = MultiServerMCPClient({
    "e2a": {
        "transport": "streamable_http",
        "url": "https://api.e2a.dev/mcp",
        "headers": {
            "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
        },
    },
})

tools = await client.get_tools()
agent = create_react_agent("anthropic:claude-sonnet-4-6", tools)
```

### OpenAI Agents SDK (Python)

```python
import os

from agents import Agent, Runner
from agents.mcp import MCPServerStreamableHttp

async with MCPServerStreamableHttp(
    name="e2a",
    params={
        "url": "https://api.e2a.dev/mcp",
        "headers": {
            "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
        },
    },
) as e2a:
    agent = Agent(name="e2a_agent", mcp_servers=[e2a])
    result = await Runner.run(agent, "Reply to the latest unread email politely.")
```

### Claude Desktop / Cline / Cursor

Add e2a as a remote MCP server in the host's config (`claude_desktop_config.json`, `cline_mcp_settings.json`, etc.):

```json
{
  "mcpServers": {
    "e2a": {
      "url": "https://api.e2a.dev/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_E2A_API_KEY"
      }
    }
  }
}
```

Hosts that support OAuth connectors can instead add `https://api.e2a.dev/mcp` as a connector and authorize in the browser — no key pasted.

## Tools

The server exposes up to **35** tools spanning agents, messages, human-in-the-loop
approval, attachments, domains, events, and webhooks. **The visible set depends on
your credential's scope (§6a):** an **agent**-scoped credential sees the 14
runtime/inbox tools (read, send, reply, and view its pending queue); an
**account**-scoped credential also sees the admin/setup tools (agent/domain/
webhook/event management — **and HITL approve/reject, which is an account-owner
action, never agent self-approval**) — all 35.
Every tool carries MCP annotations (`readOnlyHint`/`destructiveHint`/
`idempotentHint`) so hosts can auto-approve reads and flag destructive actions.
The tables below highlight the most commonly used ones — your MCP host's tool list
shows the set your scope allows, with per-tool descriptions.

### Identity

| Tool | Description |
| --- | --- |
| `whoami` | Get the authenticated account's identity — user, scope, plan/limits; for an agent-scoped credential, also the bound agent address. |
| `list_agents` | List every agent inbox owned by the authenticated user. |
| `create_agent` | Register a new agent by its full email address — on a verified domain you own, or the deployment's shared domain. No delivery "mode": inbound is always available via `list_messages` (poll) or a `create_webhook` subscription. (Admin/account-scoped.) |

> **Webhook deliveries are signed — verify them.** Push delivery is a top-level
> resource (`create_webhook`), not a per-agent mode. e2a HMAC-signs every webhook
> delivery against the webhook's signing secret (returned once from `create_webhook`
> / `rotate_webhook_secret`). Your handler must verify the signature on every
> request — the [e2a SDK](https://www.npmjs.com/package/@e2a/sdk) exposes
> `constructEvent(rawBody, signatureHeader, secret)` which verifies and returns a
> typed event in one call (throws on a bad signature). Or skip webhooks entirely
> and poll via `list_messages`.

### Messages

| Tool | Description |
| --- | --- |
| `send_email` | Send a new email. When the agent's outbound policy or content scan holds it for review, the message is held and returns `status: pending_review` instead of `sent`. |
| `reply_to_message` | Reply to an inbound message. Preserves In-Reply-To / References for thread continuity. |
| `list_messages` | List inbound mail. Filter by `read_status` (unread / read / all); cursor-paginated (`cursor` + `limit` in, `next_cursor` out). |
| `get_message` | Fetch full body, headers, and attachment metadata for one message. |
| `get_attachment` | Get one attachment's metadata + a short-lived `download_url` (fetch the bytes out of band); `inline: true` returns base64 `data` for small files (≤256 KB). |

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

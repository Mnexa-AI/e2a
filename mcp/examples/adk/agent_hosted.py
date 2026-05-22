"""End-to-end demo: Google ADK agent against the hosted e2a MCP server.

Same shape as `agent.py` but uses the hosted MCP endpoint
(https://mcp.e2a.dev/mcp) over Streamable HTTP instead of spawning
@e2a/mcp-server locally over stdio. Pick this variant when:
- Deploying the ADK agent to Cloud Run (which does not support stdio
  MCP servers).
- You don't want a Node toolchain on the agent host.
- You want updates to land without rebuilding the agent's image.

Requires:
  E2A_API_KEY      e2a API key (https://e2a.dev)
  GOOGLE_API_KEY   Google AI Studio key

Run interactively (recommended — opens ADK Web UI):
  pip install -r requirements.txt
  adk web

Or CLI:
  adk run agent_hosted.py
"""

import os

from google.adk.agents import Agent
from google.adk.tools.mcp_tool.mcp_toolset import McpToolset
from google.adk.tools.mcp_tool.mcp_session_manager import StreamableHTTPConnectionParams


root_agent = Agent(
    model="gemini-flash-latest",
    name="e2a_agent",
    instruction=(
        "You manage email through the e2a tools. Call whoami once to "
        "find your inbox address. Use list_messages and get_message to "
        "read; use reply_to_message (not send_email) when replying to "
        "an existing thread so In-Reply-To and References headers are "
        "preserved."
    ),
    tools=[
        McpToolset(
            connection_params=StreamableHTTPConnectionParams(
                url="https://mcp.e2a.dev/mcp",
                headers={
                    "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
                },
                timeout=30,
            ),
        ),
    ],
)

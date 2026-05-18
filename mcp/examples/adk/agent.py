"""End-to-end demo: Google ADK agent driving @e2a/mcp-server over stdio.

Wires the e2a MCP server into an ADK `Agent` so the LLM can send,
read, and reply to email through natural-language prompts.

Requires:
  E2A_API_KEY      e2a API key (https://e2a.dev)
  E2A_AGENT_EMAIL  (optional) default agent inbox
  E2A_BASE_URL     (optional) self-hosted e2a base URL
  GOOGLE_API_KEY   Google AI Studio key

Run interactively (recommended — opens ADK Web UI):
  pip install -r requirements.txt
  adk web

Or CLI:
  adk run agent.py
"""

import os

from google.adk.agents import Agent
from google.adk.tools.mcp_tool import McpToolset
from google.adk.tools.mcp_tool.mcp_session_manager import StdioConnectionParams
from mcp import StdioServerParameters


def _e2a_env() -> dict[str, str]:
    env = {"E2A_API_KEY": os.environ["E2A_API_KEY"]}
    for k in ("E2A_AGENT_EMAIL", "E2A_BASE_URL"):
        if k in os.environ:
            env[k] = os.environ[k]
    return env


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
            connection_params=StdioConnectionParams(
                server_params=StdioServerParameters(
                    command="npx",
                    args=["-y", "@e2a/mcp-server"],
                    env=_e2a_env(),
                ),
                timeout=30,
            ),
        ),
    ],
)

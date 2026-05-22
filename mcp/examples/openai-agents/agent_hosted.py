"""End-to-end demo: OpenAI Agents SDK against the hosted e2a MCP server.

Same shape as `agent.py` but uses the hosted MCP endpoint
(https://mcp.e2a.dev/mcp) over Streamable HTTP instead of spawning
@e2a/mcp-server locally over stdio. Pick this variant when:
- Deploying the agent to a serverless runtime (Cloud Run, Lambda) where
  launching a stdio child process per request is awkward or impossible.
- You don't want a Node toolchain on the agent host.
- You want updates to land without rebuilding the agent's image.

Requires:
  E2A_API_KEY      e2a API key (https://e2a.dev)
  OPENAI_API_KEY   OpenAI API key

Run:
  pip install -r requirements.txt
  python agent_hosted.py "what's in my inbox?"
"""

import asyncio
import os
import sys

from agents import Agent, Runner
from agents.mcp import MCPServerStreamableHttp


async def main(prompt: str) -> None:
    async with MCPServerStreamableHttp(
        name="e2a",
        params={
            "url": "https://mcp.e2a.dev/mcp",
            "headers": {
                "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
            },
        },
        # Default is 5s — too tight for the first request against a
        # cold serverless backend. Match the ADK and LangChain hosted
        # variants at 30s.
        client_session_timeout_seconds=30,
    ) as e2a:
        agent = Agent(
            name="e2a_agent",
            instructions=(
                "Manage email through the e2a tools. Use whoami once to "
                "find the inbox; list_messages + get_message to read; "
                "reply_to_message (not send_email) to reply in-thread."
            ),
            mcp_servers=[e2a],
        )
        result = await Runner.run(agent, prompt)
        print(result.final_output)


if __name__ == "__main__":
    prompt = " ".join(sys.argv[1:]) or "what's in my inbox?"
    asyncio.run(main(prompt))

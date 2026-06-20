"""End-to-end demo: OpenAI Agents SDK against the hosted e2a MCP server.

Connects to the hosted MCP endpoint (https://api.e2a.dev/mcp) over
Streamable HTTP with your API key in the Authorization header. Works
locally and on serverless runtimes (Cloud Run, Lambda, etc.).

Requires:
  E2A_API_KEY      e2a API key (https://e2a.dev)
  OPENAI_API_KEY   OpenAI API key

Run:
  pip install -r requirements.txt
  python agent.py "what's in my inbox?"
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
            "url": "https://api.e2a.dev/mcp",
            "headers": {
                "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
            },
        },
        # Default is 5s — too tight for the first request against a
        # cold serverless backend. Match the ADK and LangChain
        # examples at 30s.
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

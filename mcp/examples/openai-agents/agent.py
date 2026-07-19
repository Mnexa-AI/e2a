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

MCP_URL = os.getenv("E2A_MCP_URL", "https://api.e2a.dev/mcp")
INSTRUCTIONS = """Manage email through the e2a tools.
Use an agent-scoped e2a key so this runtime can act only as its own inbox.
Call whoami once to learn that inbox address. Use list_messages and then
get_message to read mail. Use reply_to_message for an existing thread so the
In-Reply-To and References headers are preserved; use send_message only for a
new thread. If send_message or reply_to_message returns pending_review, the
message was accepted for human review: report that status and Do not retry it.
Never approve or reject the agent's own held mail. Retry a failed tool call only
when its structured error says retryable=true, honoring retry_after_seconds.
"""


async def main(prompt: str) -> None:
    async with MCPServerStreamableHttp(
        name="e2a",
        params={
            "url": MCP_URL,
            "headers": {
                "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
            },
            "timeout": 30,
        },
        cache_tools_list=True,
        max_retry_attempts=3,
    ) as e2a:
        agent = Agent(
            name="e2a_agent",
            instructions=INSTRUCTIONS,
            mcp_servers=[e2a],
        )
        result = await Runner.run(agent, prompt)
        print(result.final_output)


if __name__ == "__main__":
    prompt = " ".join(sys.argv[1:]) or "what's in my inbox?"
    asyncio.run(main(prompt))

"""End-to-end demo: OpenAI Agents SDK using @e2a/mcp-server over stdio.

Wires the e2a MCP server into an `Agent` so the LLM can send, read,
and reply to email through natural-language prompts.

Requires:
  E2A_API_KEY      e2a API key (https://e2a.dev)
  E2A_AGENT_EMAIL  (optional) default agent inbox
  E2A_BASE_URL     (optional) self-hosted e2a base URL
  OPENAI_API_KEY   OpenAI API key

Run:
  pip install -r requirements.txt
  python agent.py "what's in my inbox?"
"""

import asyncio
import os
import sys

from agents import Agent, Runner
from agents.mcp import MCPServerStdio


def _e2a_env() -> dict[str, str]:
    env = {"E2A_API_KEY": os.environ["E2A_API_KEY"]}
    for k in ("E2A_AGENT_EMAIL", "E2A_BASE_URL"):
        if k in os.environ:
            env[k] = os.environ[k]
    return env


async def main(prompt: str) -> None:
    async with MCPServerStdio(
        name="e2a",
        params={
            "command": "npx",
            "args": ["-y", "@e2a/mcp-server"],
            "env": _e2a_env(),
        },
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

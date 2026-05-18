"""End-to-end demo: LangChain agent driving @e2a/mcp-server over stdio.

Wires the e2a MCP server into a LangGraph ReAct agent so the LLM can
send, read, and reply to email through natural-language prompts.

Requires:
  E2A_API_KEY        e2a API key (https://e2a.dev)
  E2A_AGENT_EMAIL    (optional) default agent inbox
  E2A_BASE_URL       (optional) self-hosted e2a base URL
  ANTHROPIC_API_KEY  Anthropic API key

Run:
  pip install -r requirements.txt
  python agent.py "what's in my inbox?"
"""

import asyncio
import os
import sys

from langchain_mcp_adapters.client import MultiServerMCPClient
from langgraph.prebuilt import create_react_agent

SYSTEM_PROMPT = (
    "You manage email through the e2a tools. Call whoami once to find "
    "your inbox address. Use list_messages and get_message to read; "
    "use reply_to_message (not send_email) when replying to an existing "
    "thread so In-Reply-To and References headers are preserved."
)


def _e2a_env() -> dict[str, str]:
    env = {"E2A_API_KEY": os.environ["E2A_API_KEY"]}
    for k in ("E2A_AGENT_EMAIL", "E2A_BASE_URL"):
        if k in os.environ:
            env[k] = os.environ[k]
    return env


async def main(prompt: str) -> None:
    client = MultiServerMCPClient(
        {
            "e2a": {
                "command": "npx",
                "args": ["-y", "@e2a/mcp-server"],
                "transport": "stdio",
                "env": _e2a_env(),
            },
        }
    )
    tools = await client.get_tools()
    print(f"Loaded {len(tools)} e2a tools: {', '.join(t.name for t in tools)}\n")

    agent = create_react_agent("anthropic:claude-sonnet-4-6", tools, prompt=SYSTEM_PROMPT)
    result = await agent.ainvoke({"messages": [{"role": "user", "content": prompt}]})

    final = result["messages"][-1]
    print(getattr(final, "content", final))


if __name__ == "__main__":
    prompt = " ".join(sys.argv[1:]) or "what's in my inbox?"
    asyncio.run(main(prompt))

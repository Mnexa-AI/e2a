"""End-to-end demo: LangChain agent against the hosted e2a MCP server (https://api.e2a.dev/mcp) over Streamable HTTP.

Connects to the hosted MCP endpoint (https://api.e2a.dev/mcp) over
Streamable HTTP with your API key in the Authorization header. Works
locally and on serverless runtimes (Cloud Run, Lambda, etc.).

Requires:
  E2A_API_KEY        e2a API key (https://e2a.dev)
  ANTHROPIC_API_KEY  Anthropic API key

Run:
  pip install -r requirements.txt
  python agent.py "what's in my inbox?"
"""

import asyncio
import os
import sys
from datetime import timedelta

from langchain_mcp_adapters.client import MultiServerMCPClient
from langgraph.prebuilt import create_react_agent

SYSTEM_PROMPT = (
    "You manage email through the e2a tools. Call whoami once to find "
    "your inbox address. Use list_messages and get_message to read; "
    "use reply_to_message (not send_email) when replying to an existing "
    "thread so In-Reply-To and References headers are preserved."
)


async def main(prompt: str) -> None:
    client = MultiServerMCPClient(
        {
            "e2a": {
                "transport": "streamable_http",
                "url": "https://api.e2a.dev/mcp",
                "headers": {
                    "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
                },
                "timeout": timedelta(seconds=30),
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

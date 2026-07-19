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

from langchain.agents import create_agent
from langchain_mcp_adapters.client import MultiServerMCPClient

MCP_URL = os.getenv("E2A_MCP_URL", "https://api.e2a.dev/mcp")
MODEL = os.getenv("LANGCHAIN_MODEL", "anthropic:claude-sonnet-4-6")
SYSTEM_PROMPT = """You manage email through the e2a tools.
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
    client = MultiServerMCPClient(
        {
            "e2a": {
                "transport": "http",
                "url": MCP_URL,
                "headers": {
                    "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
                },
            },
        },
        # MCP isError results become model-visible failed ToolMessages. Transport
        # failures still raise, which keeps broken auth/connectivity explicit.
        handle_tool_errors=True,
    )
    tools = await client.get_tools()
    print(f"Loaded {len(tools)} e2a tools: {', '.join(t.name for t in tools)}\n")

    agent = create_agent(MODEL, tools=tools, system_prompt=SYSTEM_PROMPT)
    result = await agent.ainvoke({"messages": [{"role": "user", "content": prompt}]})

    final = result["messages"][-1]
    print(getattr(final, "content", final))


if __name__ == "__main__":
    prompt = " ".join(sys.argv[1:]) or "what's in my inbox?"
    asyncio.run(main(prompt))

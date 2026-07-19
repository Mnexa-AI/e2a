"""End-to-end demo: CrewAI agent against the hosted e2a MCP server (https://api.e2a.dev/mcp) over Streamable HTTP.

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

import os
import sys

from crewai import Agent, Crew, Process, Task
from crewai.mcp import MCPServerHTTP

MCP_URL = os.getenv("E2A_MCP_URL", "https://api.e2a.dev/mcp")
MODEL = os.getenv("CREWAI_MODEL", "anthropic/claude-sonnet-4-6")
BACKSTORY = """You manage email through the e2a tools.
Use an agent-scoped e2a key so this runtime can act only as its own inbox.
Call whoami once to learn that inbox address. Use list_messages and then
get_message to read mail. Use reply_to_message for an existing thread so the
In-Reply-To and References headers are preserved; use send_message only for a
new thread. If send_message or reply_to_message returns pending_review, the
message was accepted for human review: report that status and Do not retry it.
Never approve or reject the agent's own held mail. Retry a failed tool call only
when its structured error says retryable=true, honoring retry_after_seconds.
"""


def main(prompt: str) -> None:
    e2a = MCPServerHTTP(
        url=MCP_URL,
        headers={
            "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
        },
        streamable=True,
        cache_tools_list=True,
    )
    agent = Agent(
        role="Email Manager",
        goal="Handle the operator's email request precisely and concisely.",
        backstory=BACKSTORY,
        mcps=[e2a],
        llm=MODEL,
        allow_delegation=False,
        verbose=True,
    )
    task = Task(
        description=prompt,
        expected_output="A clear, concise answer to the user's email-related request.",
        agent=agent,
    )
    crew = Crew(
        agents=[agent],
        tasks=[task],
        process=Process.sequential,
        verbose=False,
    )
    print(crew.kickoff())


if __name__ == "__main__":
    prompt = " ".join(sys.argv[1:]) or "what's in my inbox?"
    main(prompt)

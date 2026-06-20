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
from crewai_tools import MCPServerAdapter

BACKSTORY = (
    "You manage email through the e2a tools. Call whoami once to find "
    "your inbox address. Use list_messages and get_message to read; "
    "use reply_to_message (not send_email) when replying to an existing "
    "thread so In-Reply-To and References headers are preserved."
)


def main(prompt: str) -> None:
    server_params = {
        "url": "https://api.e2a.dev/mcp",
        "transport": "streamable-http",
        "headers": {
            "Authorization": f"Bearer {os.environ['E2A_API_KEY']}",
        },
    }

    with MCPServerAdapter(server_params) as e2a_tools:
        print(
            f"Loaded {len(e2a_tools)} e2a tools: "
            f"{', '.join(t.name for t in e2a_tools)}\n"
        )

        agent = Agent(
            role="Email Manager",
            goal="Handle the operator's email request precisely and concisely.",
            backstory=BACKSTORY,
            tools=e2a_tools,
            llm="anthropic/claude-sonnet-4-6",
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
        result = crew.kickoff()
        print(result)


if __name__ == "__main__":
    prompt = " ".join(sys.argv[1:]) or "what's in my inbox?"
    main(prompt)

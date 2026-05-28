"""End-to-end demo: CrewAI agent driving @e2a/mcp-server over stdio.

Wires the e2a MCP server into a single-agent CrewAI crew so the LLM can
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

import os
import sys

from crewai import Agent, Crew, Process, Task
from crewai_tools import MCPServerAdapter
from mcp import StdioServerParameters

BACKSTORY = (
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


def main(prompt: str) -> None:
    server_params = StdioServerParameters(
        command="npx",
        args=["-y", "@e2a/mcp-server"],
        env=_e2a_env(),
    )

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

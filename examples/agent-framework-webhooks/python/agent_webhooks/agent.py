"""OpenAI Agents SDK integration for the minimal webhook example."""

import os
from collections.abc import Awaitable, Callable
from typing import Any

from e2a import AsyncInboundEmail

from .prompt import REPLY_INSTRUCTIONS, email_prompt

OpenAIRun = Callable[[str], Awaitable[Any]]


class OpenAIReplyAgent:
    """Produce reply text from the safe, normalized inbound projection."""

    def __init__(self, run: OpenAIRun) -> None:
        self._run = run

    @classmethod
    def from_env(cls) -> "OpenAIReplyAgent":
        """Build the production agent with the official OpenAI Agents SDK."""

        from agents import Agent, Runner

        agent = Agent(
            name="Email assistant",
            instructions=REPLY_INSTRUCTIONS,
            model=os.getenv("OPENAI_MODEL", "gpt-5.6"),
        )

        async def run(prompt: str) -> Any:
            return await Runner.run(agent, prompt)

        return cls(run)

    async def reply(
        self, email: AsyncInboundEmail, conversation_id: str
    ) -> str:
        del conversation_id
        result = await self._run(email_prompt(email))
        final_output = result.final_output
        return "" if final_output is None else str(final_output)

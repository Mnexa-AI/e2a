"""OpenAI Agents SDK reply adapter."""

import os
from collections.abc import Awaitable, Callable
from typing import Any

from e2a import AsyncInboundEmail

from agent_webhooks.prompt import REPLY_INSTRUCTIONS, email_prompt

OpenAIRun = Callable[[str], Awaitable[Any]]


class OpenAIReplyAgent:
    def __init__(self, run: OpenAIRun) -> None:
        self._run = run

    @classmethod
    def from_env(cls) -> "OpenAIReplyAgent":
        from agents import Agent, Runner

        agent = Agent(
            name="Email assistant",
            instructions=REPLY_INSTRUCTIONS,
            model=os.getenv("OPENAI_MODEL", "gpt-5.6"),
        )
        return cls(lambda prompt: Runner.run(agent, prompt))

    async def reply(self, email: AsyncInboundEmail) -> str:
        result = await self._run(email_prompt(email))
        final_output = result.final_output
        return "" if final_output is None else str(final_output)

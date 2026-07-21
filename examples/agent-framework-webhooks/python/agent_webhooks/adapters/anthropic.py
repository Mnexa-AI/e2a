"""Anthropic Messages SDK reply adapter."""

import os
from collections.abc import Awaitable, Callable
from typing import Any

from e2a import AsyncInboundEmail

from agent_webhooks.prompt import email_prompt

from .openai import INSTRUCTIONS

AnthropicRun = Callable[[str], Awaitable[Any]]


class AnthropicReplyAgent:
    def __init__(self, run: AnthropicRun) -> None:
        self._run = run

    @classmethod
    def from_env(cls) -> "AnthropicReplyAgent":
        from anthropic import AsyncAnthropic

        client = AsyncAnthropic()
        return cls(
            lambda prompt: client.messages.create(
                model=os.getenv("ANTHROPIC_MODEL", "claude-opus-4-8"),
                max_tokens=1024,
                system=INSTRUCTIONS,
                messages=[{"role": "user", "content": prompt}],
            )
        )

    async def reply(self, email: AsyncInboundEmail) -> str:
        result = await self._run(email_prompt(email))
        return "\n".join(
            block.text
            for block in result.content
            if getattr(block, "type", None) == "text"
            and isinstance(getattr(block, "text", None), str)
        )

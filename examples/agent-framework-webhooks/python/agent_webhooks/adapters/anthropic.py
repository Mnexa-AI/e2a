"""Anthropic Messages SDK reply adapter."""

import os
from collections.abc import Awaitable, Callable
from typing import Any

from e2a import AsyncInboundEmail

from agent_webhooks.prompt import REPLY_INSTRUCTIONS, email_prompt

AnthropicRun = Callable[[str], Awaitable[Any]]
AnthropicClose = Callable[[], Awaitable[None]]


class AnthropicReplyAgent:
    def __init__(
        self, run: AnthropicRun, close: AnthropicClose | None = None
    ) -> None:
        self._run = run
        self._close = close

    @classmethod
    def from_env(cls) -> "AnthropicReplyAgent":
        from anthropic import AsyncAnthropic

        client = AsyncAnthropic()
        return cls(
            lambda prompt: client.messages.create(
                model=os.getenv("ANTHROPIC_MODEL", "claude-opus-4-8"),
                max_tokens=1024,
                system=REPLY_INSTRUCTIONS,
                messages=[{"role": "user", "content": prompt}],
            ),
            close=client.close,
        )

    async def aclose(self) -> None:
        if self._close is not None:
            await self._close()

    async def reply(
        self, email: AsyncInboundEmail, conversation_id: str
    ) -> str:
        del conversation_id
        result = await self._run(email_prompt(email))
        return "\n".join(
            block.text
            for block in result.content
            if getattr(block, "type", None) == "text"
            and isinstance(getattr(block, "text", None), str)
        )

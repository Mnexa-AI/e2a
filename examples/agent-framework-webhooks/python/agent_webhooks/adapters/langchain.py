"""LangChain 1.x reply adapter."""

import os
from collections.abc import Awaitable, Callable, Mapping, Sequence
from typing import Any

from e2a import AsyncInboundEmail

from agent_webhooks.prompt import REPLY_INSTRUCTIONS, email_prompt

LangChainRun = Callable[[str], Awaitable[Mapping[str, Any]]]


def _field(value: Any, name: str) -> Any:
    if isinstance(value, Mapping):
        return value.get(name)
    return getattr(value, name, None)


def _text_content(content: Any) -> str:
    if isinstance(content, str):
        return content
    if not isinstance(content, Sequence):
        return ""
    return "\n".join(
        text
        for block in content
        if _field(block, "type") == "text"
        and isinstance((text := _field(block, "text")), str)
    )


class LangChainReplyAgent:
    def __init__(self, run: LangChainRun) -> None:
        self._run = run

    @classmethod
    def from_env(cls) -> "LangChainReplyAgent":
        from langchain.agents import create_agent

        agent = create_agent(
            model=os.getenv("LANGCHAIN_MODEL", "openai:gpt-5.5"),
            tools=[],
            system_prompt=REPLY_INSTRUCTIONS,
        )
        return cls(
            lambda prompt: agent.ainvoke(
                {"messages": [{"role": "user", "content": prompt}]}
            )
        )

    async def reply(
        self, email: AsyncInboundEmail, conversation_id: str
    ) -> str:
        del conversation_id
        result = await self._run(email_prompt(email))
        messages = result.get("messages", [])
        for message in reversed(messages):
            if (
                _field(message, "type") == "ai"
                or _field(message, "role") == "assistant"
            ):
                return _text_content(_field(message, "content"))
        raise ValueError("LangChain result did not contain an assistant message")

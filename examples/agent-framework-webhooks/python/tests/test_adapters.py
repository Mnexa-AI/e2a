import asyncio
from collections.abc import AsyncIterator, Coroutine
from types import SimpleNamespace
from typing import Any, TypeVar

import pytest

from agent_webhooks.adapters import (
    ADKReplyAgent,
    AnthropicReplyAgent,
    FakeReplyAgent,
    LangChainReplyAgent,
    OpenAIReplyAgent,
)
from agent_webhooks.prompt import email_prompt

T = TypeVar("T")


def wait(coro: Coroutine[Any, Any, T]) -> T:
    return asyncio.run(coro)


@pytest.fixture
def email() -> SimpleNamespace:
    return SimpleNamespace(
        from_="sender@example.com",
        subject="A useful subject",
        text="Please send a concise answer.",
        verified=True,
        flagged=False,
        conversation_id="conversation-123",
        id="message-123",
    )


def test_openai_returns_final_output_and_passes_email_prompt(
    email: SimpleNamespace,
) -> None:
    prompts: list[str] = []

    async def run(prompt: str) -> Any:
        prompts.append(prompt)
        return SimpleNamespace(final_output="OpenAI")

    assert wait(OpenAIReplyAgent(run).reply(email)) == "OpenAI"
    assert prompts == [email_prompt(email)]


def test_openai_treats_none_final_output_as_empty(email: SimpleNamespace) -> None:
    async def run(_: str) -> Any:
        return SimpleNamespace(final_output=None)

    assert wait(OpenAIReplyAgent(run).reply(email)) == ""


def test_anthropic_joins_only_text_blocks_in_order(
    email: SimpleNamespace,
) -> None:
    prompts: list[str] = []

    async def run(prompt: str) -> Any:
        prompts.append(prompt)
        return SimpleNamespace(
            content=[
                SimpleNamespace(type="text", text="First"),
                SimpleNamespace(type="tool_use", text="ignored"),
                SimpleNamespace(type="text", text="Second"),
                SimpleNamespace(type="image", source={}),
            ]
        )

    assert wait(AnthropicReplyAgent(run).reply(email)) == "First\nSecond"
    assert prompts == [email_prompt(email)]


def test_langchain_returns_last_assistant_message(
    email: SimpleNamespace,
) -> None:
    prompts: list[str] = []

    async def run(prompt: str) -> Any:
        prompts.append(prompt)
        return {
            "messages": [
                SimpleNamespace(type="human", content="question"),
                SimpleNamespace(type="ai", content="Earlier"),
                SimpleNamespace(role="assistant", content="Last"),
            ]
        }

    assert wait(LangChainReplyAgent(run).reply(email)) == "Last"
    assert prompts == [email_prompt(email)]


def test_langchain_supports_text_content_blocks(email: SimpleNamespace) -> None:
    async def run(_: str) -> Any:
        return {
            "messages": [
                SimpleNamespace(
                    type="ai",
                    content=[
                        {"type": "text", "text": "One"},
                        {"type": "image", "url": "ignored"},
                        SimpleNamespace(type="text", text="Two"),
                    ],
                )
            ]
        }

    assert wait(LangChainReplyAgent(run).reply(email)) == "One\nTwo"


def test_langchain_rejects_missing_final_assistant(
    email: SimpleNamespace,
) -> None:
    async def run(_: str) -> Any:
        return {"messages": [SimpleNamespace(type="human", content="question")]}

    with pytest.raises(ValueError, match="assistant message"):
        wait(LangChainReplyAgent(run).reply(email))


class Event:
    def __init__(self, *, final: bool, text: str | None = None) -> None:
        self._final = final
        parts = [] if text is None else [SimpleNamespace(text=text)]
        self.content = SimpleNamespace(parts=parts)

    def is_final_response(self) -> bool:
        return self._final


async def events(*items: Event) -> AsyncIterator[Event]:
    for item in items:
        yield item


def test_adk_returns_last_final_text_and_ignores_nonfinal_events(
    email: SimpleNamespace,
) -> None:
    calls: list[tuple[Any, str]] = []

    def run(received_email: Any, prompt: str) -> AsyncIterator[Event]:
        calls.append((received_email, prompt))
        return events(
            Event(final=False, text="draft"),
            Event(final=True, text="Earlier final"),
            Event(final=False, text="tool"),
            Event(final=True, text="ADK"),
        )

    assert wait(ADKReplyAgent(run).reply(email)) == "ADK"
    assert calls == [(email, email_prompt(email))]


def test_adk_returns_empty_without_final_text(email: SimpleNamespace) -> None:
    def run(_: Any, __: str) -> AsyncIterator[Event]:
        return events(Event(final=False, text="draft"), Event(final=True))

    assert wait(ADKReplyAgent(run).reply(email)) == ""


def test_fake_is_deterministic_and_records_prompts(email: SimpleNamespace) -> None:
    agent = FakeReplyAgent("Fake")

    assert wait(agent.reply(email)) == "Fake"
    assert wait(agent.reply(email)) == "Fake"
    assert agent.prompts == [email_prompt(email), email_prompt(email)]
    assert agent.call_count == 2

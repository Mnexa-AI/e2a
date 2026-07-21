# mypy: disable-error-code="arg-type"

import asyncio
import hashlib
import sys
from collections.abc import AsyncIterator, Coroutine
from types import ModuleType, SimpleNamespace
from typing import Any, TypeVar
from unittest.mock import AsyncMock

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
        inbox="assistant@example.com",
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

    assert wait(OpenAIReplyAgent(run).reply(email, "conversation-123")) == "OpenAI"
    assert prompts == [email_prompt(email)]


def test_openai_treats_none_final_output_as_empty(email: SimpleNamespace) -> None:
    async def run(_: str) -> Any:
        return SimpleNamespace(final_output=None)

    assert wait(OpenAIReplyAgent(run).reply(email, "conversation-123")) == ""


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

    assert wait(AnthropicReplyAgent(run).reply(email, "conversation-123")) == "First\nSecond"
    assert prompts == [email_prompt(email)]


def test_anthropic_factory_closes_owned_client(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import anthropic

    class FakeMessages:
        async def create(self, **_: Any) -> Any:
            return SimpleNamespace(content=[])

    class FakeClient:
        instance: "FakeClient"

        def __init__(self) -> None:
            self.messages = FakeMessages()
            self.close = AsyncMock()
            FakeClient.instance = self

    monkeypatch.setattr(anthropic, "AsyncAnthropic", FakeClient)
    agent = AnthropicReplyAgent.from_env()

    wait(agent.aclose())

    FakeClient.instance.close.assert_awaited_once_with()


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

    assert wait(LangChainReplyAgent(run).reply(email, "conversation-123")) == "Last"
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

    assert wait(LangChainReplyAgent(run).reply(email, "conversation-123")) == "One\nTwo"


def test_langchain_rejects_missing_final_assistant(
    email: SimpleNamespace,
) -> None:
    async def run(_: str) -> Any:
        return {"messages": [SimpleNamespace(type="human", content="question")]}

    with pytest.raises(ValueError, match="assistant message"):
        wait(LangChainReplyAgent(run).reply(email, "conversation-123"))


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
    calls: list[tuple[Any, str, str]] = []

    def run(
        received_email: Any, prompt: str, conversation_id: str
    ) -> AsyncIterator[Event]:
        calls.append((received_email, prompt, conversation_id))
        return events(
            Event(final=False, text="draft"),
            Event(final=True, text="Earlier final"),
            Event(final=False, text="tool"),
            Event(final=True, text="ADK"),
        )

    assert wait(ADKReplyAgent(run).reply(email, "conversation-123")) == "ADK"
    assert calls == [(email, email_prompt(email), "conversation-123")]


def test_adk_returns_empty_without_final_text(email: SimpleNamespace) -> None:
    def run(_: Any, __: str, ___: str) -> AsyncIterator[Event]:
        return events(Event(final=False, text="draft"), Event(final=True))

    assert wait(ADKReplyAgent(run).reply(email, "conversation-123")) == ""


def test_adk_sender_formatting_maps_to_same_user_for_same_inbox() -> None:
    from agent_webhooks.adapters.adk import _user_id

    display = SimpleNamespace(
        from_="Ada <ADA@example.com>", inbox="Assistant@Example.com"
    )
    mailbox = SimpleNamespace(
        from_="ada@example.com", inbox="assistant@example.com"
    )

    assert _user_id(display) == _user_id(mailbox)


def test_adk_same_sender_maps_to_different_users_for_different_inboxes() -> None:
    from agent_webhooks.adapters.adk import _user_id

    first = SimpleNamespace(from_="ada@example.com", inbox="one@example.com")
    second = SimpleNamespace(from_="ada@example.com", inbox="two@example.com")

    assert _user_id(first) != _user_id(second)


def test_adk_factory_uses_isolated_identity_and_first_contact_session(
    email: SimpleNamespace, monkeypatch: pytest.MonkeyPatch
) -> None:
    class FakeSessions:
        instance: "FakeSessions"

        def __init__(self) -> None:
            self.get_calls: list[dict[str, str]] = []
            self.create_calls: list[dict[str, str]] = []
            FakeSessions.instance = self

        async def get_session(self, **kwargs: str) -> None:
            self.get_calls.append(kwargs)
            return None

        async def create_session(self, **kwargs: str) -> SimpleNamespace:
            self.create_calls.append(kwargs)
            return SimpleNamespace()

    class FakeRunner:
        instance: "FakeRunner"

        def __init__(self, **kwargs: Any) -> None:
            self.init_kwargs = kwargs
            self.run_calls: list[dict[str, Any]] = []
            FakeRunner.instance = self

        async def run_async(self, **kwargs: Any) -> AsyncIterator[Event]:
            self.run_calls.append(kwargs)
            yield Event(final=True, text="ADK factory")

    agents = ModuleType("google.adk.agents")
    setattr(agents, "LlmAgent", lambda **kwargs: SimpleNamespace(**kwargs))
    runners = ModuleType("google.adk.runners")
    setattr(runners, "Runner", FakeRunner)
    sessions = ModuleType("google.adk.sessions")
    setattr(sessions, "InMemorySessionService", FakeSessions)
    genai = ModuleType("google.genai")
    setattr(
        genai,
        "types",
        SimpleNamespace(
            Content=lambda **kwargs: SimpleNamespace(**kwargs),
            Part=lambda **kwargs: SimpleNamespace(**kwargs),
        ),
    )
    monkeypatch.setitem(sys.modules, "google.adk", ModuleType("google.adk"))
    monkeypatch.setitem(sys.modules, "google.adk.agents", agents)
    monkeypatch.setitem(sys.modules, "google.adk.runners", runners)
    monkeypatch.setitem(sys.modules, "google.adk.sessions", sessions)
    monkeypatch.setitem(sys.modules, "google.genai", genai)
    email.from_ = "Ada <ADA@example.com>"
    email.inbox = "Assistant@Example.com"
    email.conversation_id = ""

    assert wait(
        ADKReplyAgent.from_env().reply(email, "conv_first_contact_123")
    ) == "ADK factory"

    digest = hashlib.sha256(
        b"assistant@example.com\0ada@example.com"
    ).hexdigest()[:20]
    context = {
        "app_name": "e2a_email_assistant",
        "user_id": f"sender-{digest}",
        "session_id": "conv_first_contact_123",
    }
    assert FakeSessions.instance.get_calls == [context]
    assert FakeSessions.instance.create_calls == [context]
    assert FakeRunner.instance.init_kwargs["app_name"] == context["app_name"]
    run_call = FakeRunner.instance.run_calls[0]
    assert run_call["user_id"] == context["user_id"]
    assert run_call["session_id"] == context["session_id"]
    assert run_call["new_message"].parts[0].text == email_prompt(email)
    assert email.conversation_id == ""


def test_fake_is_deterministic_and_records_prompts(email: SimpleNamespace) -> None:
    agent = FakeReplyAgent("Fake")

    assert wait(agent.reply(email, "conversation-123")) == "Fake"
    assert wait(agent.reply(email, "conversation-123")) == "Fake"
    assert agent.prompts == [email_prompt(email), email_prompt(email)]
    assert agent.call_count == 2


def test_shared_reply_instructions_define_body_only_output() -> None:
    from agent_webhooks.prompt import REPLY_INSTRUCTIONS

    assert "1-3 short paragraphs" in REPLY_INSTRUCTIONS
    assert "body text only" in REPLY_INSTRUCTIONS
    assert "do not include a Subject line" in REPLY_INSTRUCTIONS

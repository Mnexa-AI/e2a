# mypy: disable-error-code="arg-type"

from types import SimpleNamespace
from typing import Any

import pytest

from agent_webhooks.agent import OpenAIReplyAgent
from agent_webhooks.prompt import email_prompt


@pytest.fixture
def email() -> SimpleNamespace:
    return SimpleNamespace(
        from_="sender@example.com",
        subject="A useful subject",
        text="Please send a concise answer.",
        verified=True,
        flagged=False,
        message=SimpleNamespace(raw_message="SECRET RAW MIME"),
    )


async def test_openai_agent_uses_only_the_safe_prompt(
    email: SimpleNamespace,
) -> None:
    prompts: list[str] = []

    async def run(prompt: str) -> Any:
        prompts.append(prompt)
        return SimpleNamespace(final_output="OpenAI reply")

    reply = await OpenAIReplyAgent(run).reply(email, "conv_evt_full")

    assert reply == "OpenAI reply"
    assert prompts == [email_prompt(email)]
    assert "SECRET RAW MIME" not in prompts[0]


async def test_openai_agent_treats_no_final_output_as_no_reply(
    email: SimpleNamespace,
) -> None:
    async def run(_: str) -> Any:
        return SimpleNamespace(final_output=None)

    assert await OpenAIReplyAgent(run).reply(email, "conv_evt_full") == ""


async def test_openai_agent_propagates_provider_errors(
    email: SimpleNamespace,
) -> None:
    async def run(_: str) -> Any:
        raise RuntimeError("provider failed")

    with pytest.raises(RuntimeError, match="provider failed"):
        await OpenAIReplyAgent(run).reply(email, "conv_evt_full")

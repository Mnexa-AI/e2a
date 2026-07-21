"""Deterministic no-key reply adapter for tests and dry runs."""

from e2a import AsyncInboundEmail

from agent_webhooks.prompt import email_prompt


class FakeReplyAgent:
    def __init__(self, response: str = "Fake") -> None:
        self.response = response
        self.prompts: list[str] = []

    @property
    def call_count(self) -> int:
        return len(self.prompts)

    async def reply(
        self, email: AsyncInboundEmail, conversation_id: str
    ) -> str:
        del conversation_id
        self.prompts.append(email_prompt(email))
        return self.response

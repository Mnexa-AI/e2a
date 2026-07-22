"""Small structural contracts used by the webhook delivery handler."""

from typing import Protocol

from e2a import AsyncInboundEmail
from e2a.v1.inbound import InboundEvent


class InboundResource(Protocol):
    """Convert verified webhook events into normalized inbound email facades."""

    async def from_event(self, event: InboundEvent) -> AsyncInboundEmail: ...


class ReplyAgent(Protocol):
    """An agent that produces a reply for an inbound email."""

    async def reply(
        self, email: AsyncInboundEmail, conversation_id: str
    ) -> str: ...

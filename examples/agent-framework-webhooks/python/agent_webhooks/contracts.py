"""Runtime-neutral contracts shared by agent framework adapters."""

from typing import Protocol

from e2a import AsyncInboundEmail


class ReplyAgent(Protocol):
    """An agent that produces a reply for an inbound email."""

    async def reply(self, email: AsyncInboundEmail) -> str: ...

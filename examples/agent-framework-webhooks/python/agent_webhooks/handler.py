"""Framework-neutral handling for verified e2a webhook deliveries."""

from __future__ import annotations

from e2a import construct_event

from .contracts import InboundResource, ReplyAgent
from .delivery_state import EventDeduper


class DeliveryInProgress(Exception):
    """Raised when another request is already processing the event."""

    def __init__(self, event_id: str) -> None:
        self.event_id = event_id
        super().__init__(f"delivery {event_id} is already in progress")


async def handle_delivery(
    body: bytes,
    signature: str,
    secret: str,
    inbound: InboundResource,
    agent: ReplyAgent,
    deduper: EventDeduper,
) -> dict[str, str]:
    """Verify, claim, and process one webhook delivery."""

    event = construct_event(body, signature, secret)
    if event.type != "email.received":
        return {"status": "ignored"}

    claim = await deduper.claim(event.id)
    if claim == "processed":
        return {"status": "duplicate"}
    if claim == "processing":
        raise DeliveryInProgress(event.id)

    try:
        email = await inbound.from_event(event)
        reply_text = (await agent.reply(email)).strip()
        if not reply_text:
            await deduper.complete(event.id)
            return {"status": "no_reply", "conversation_id": email.conversation_id}

        result = await email.reply(
            {"text": reply_text, "conversation_id": email.conversation_id},
            idempotency_key=event.id,
        )
        await deduper.complete(event.id)
        status = "replied" if result.status in {"accepted", "sent"} else result.status
        return {"status": status, "conversation_id": email.conversation_id}
    except BaseException:
        await deduper.release(event.id)
        raise

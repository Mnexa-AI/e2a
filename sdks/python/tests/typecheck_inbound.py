"""Static contract checks for async and blocking inbound facades."""

from typing import Awaitable

from e2a.v1 import AsyncE2AClient, AsyncInboundEmail, E2AClient, InboundEmail
from e2a.v1.generated.models import AttachmentView, SendResultView
from e2a.v1.webhook_signature import WebhookEvent


event = WebhookEvent(
    type="email.received",
    id="evt_1",
    schema_version="1",
    created_at="2026-07-01T10:30:00Z",
    data={"message_id": "msg_1", "delivered_to": "bot@example.com"},
)

async_client = AsyncE2AClient(api_key="e2a_test")
async_email_result: Awaitable[AsyncInboundEmail] = async_client.inbound.from_event(event)

sync_client = E2AClient(api_key="e2a_test")
sync_email: InboundEmail = sync_client.inbound.from_event(event)
sync_reply: SendResultView = sync_email.reply({"text": "ok"})
sync_attachment: AttachmentView = sync_email.attachments[0].get(inline=True)

_ = (async_email_result, sync_reply, sync_attachment)

"""Static contract checks for event constructors and narrowing guards."""

from typing import Any

from typing_extensions import assert_type

from e2a.v1 import WebhookEvent, WSEvent
from e2a.v1.generated.models import MessageLifecycleTransition
from e2a.v1.webhook_signature import (
    EmailSentData,
    is_domain_suppression_added,
    is_email_bounced,
    is_email_complained,
    is_email_delivered,
    is_email_failed,
    is_email_received,
    is_email_sent,
)


webhook_event: WebhookEvent[Any] = WebhookEvent(
    type="email.received",
    id="evt_1",
    schema_version="1",
    created_at="2026-07-01T10:30:00Z",
    data={},
)
ws_event: WSEvent[Any] = WSEvent(
    type="email.received",
    id="evt_1",
    schema_version="1",
    created_at="2026-07-01T10:30:00Z",
    data={},
)

loopback_sent_data: EmailSentData = {
    "message_id": "msg_local",
    "agent_email": "bot@example.com",
    "direction": "outbound",
    "method": "loopback",
    "from": "bot@example.com",
    "to": ["bot@example.com"],
    "subject": "Note to self",
    "message_type": "send",
}

# These ignores are intentional assertions: warn_unused_ignores makes mypy
# fail if the core fields ever become optional again.
WebhookEvent(type="email.received", data={})  # type: ignore[call-arg]
WSEvent(type="email.received", data={})  # type: ignore[call-arg]


def probe_webhook_narrowing(event: WebhookEvent[Any]) -> None:
    if is_email_received(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_sent(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_failed(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_delivered(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_bounced(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_complained(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_domain_suppression_added(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)


def probe_websocket_narrowing(event: WSEvent[Any]) -> None:
    if is_email_received(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_sent(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_failed(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_delivered(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_bounced(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_email_complained(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)
    if is_domain_suppression_added(event):
        assert_type(event.data["lifecycle_transitions"][0], MessageLifecycleTransition)

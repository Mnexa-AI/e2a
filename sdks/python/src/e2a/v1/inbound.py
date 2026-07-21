"""High-level inbound email facade over verified event envelopes."""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Any, Coroutine, List, Mapping, Optional, Protocol, TypeVar

from .errors import E2AValidationError
from .generated.models import (
    AttachmentMetaView,
    AttachmentView,
    Authentication,
    MessageView,
    SendResultView,
)

__all__ = [
    "InboundEvent",
    "AsyncInboundResource",
    "AsyncInboundEmail",
    "AsyncInboundAttachment",
    "InboundResource",
    "InboundEmail",
    "InboundAttachment",
]


class InboundEvent(Protocol):
    """Structural webhook/WebSocket envelope accepted by the facade."""

    @property
    def id(self) -> str: ...

    @property
    def type(self) -> str: ...

    @property
    def schema_version(self) -> str: ...

    @property
    def created_at(self) -> str: ...

    @property
    def data(self) -> Any: ...


class InboundMessageOperations(Protocol):
    async def get(self, email: str, message_id: str) -> MessageView: ...

    async def get_attachment(
        self, email: str, message_id: str, index: int, *, inline: bool = False
    ) -> AttachmentView: ...

    async def reply(
        self,
        email: str,
        message_id: str,
        body: Any,
        *,
        idempotency_key: Optional[str] = None,
    ) -> SendResultView: ...

    async def forward(
        self,
        email: str,
        message_id: str,
        body: Any,
        *,
        idempotency_key: Optional[str] = None,
    ) -> SendResultView: ...


def _invalid_event(message: str) -> E2AValidationError:
    return E2AValidationError(
        code="invalid_email_received_event",
        message=message,
        status=0,
        retryable=False,
    )


def _validated_data(event: InboundEvent) -> Mapping[str, Any]:
    if event.schema_version != "1" or event.type != "email.received":
        raise _invalid_event("expected a schema-v1 email.received event")
    if not isinstance(event.data, Mapping):
        raise _invalid_event("email.received data must be a mapping")
    message_id = event.data.get("message_id")
    delivered_to = event.data.get("delivered_to")
    if not isinstance(message_id, str) or not message_id:
        raise _invalid_event("email.received requires a non-empty message_id fetch key")
    if not isinstance(delivered_to, str) or not delivered_to:
        raise _invalid_event("email.received requires a non-empty delivered_to fetch key")
    return event.data


def _iso_milliseconds(value: datetime) -> str:
    normalized = value
    if normalized.tzinfo is None:
        normalized = normalized.replace(tzinfo=timezone.utc)
    normalized = normalized.astimezone(timezone.utc)
    return normalized.isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _attachment_dict(meta: AttachmentMetaView) -> dict[str, Any]:
    result: dict[str, Any] = {
        "index": meta.index,
        "size_bytes": meta.size_bytes,
    }
    if meta.content_id is not None:
        result["content_id"] = meta.content_id
    if meta.content_type is not None:
        result["content_type"] = meta.content_type
    if meta.filename is not None:
        result["filename"] = meta.filename
    return result


class AsyncInboundAttachment:
    def __init__(
        self,
        meta: AttachmentMetaView,
        inbox: str,
        message_id: str,
        messages: InboundMessageOperations,
    ) -> None:
        self.content_id = meta.content_id
        self.content_type = meta.content_type
        self.filename = meta.filename
        self.index = meta.index
        self.size_bytes = meta.size_bytes
        self._inbox = inbox
        self._message_id = message_id
        self._messages = messages

    async def get(self, *, inline: bool = False) -> AttachmentView:
        return await self._messages.get_attachment(
            self._inbox, self._message_id, self.index, inline=inline
        )

    def to_dict(self) -> dict[str, Any]:
        return _attachment_dict(self)  # type: ignore[arg-type]

    def __repr__(self) -> str:
        return f"AsyncInboundAttachment({self.to_dict()!r})"


class AsyncInboundEmail:
    def __init__(
        self,
        event: InboundEvent,
        message: MessageView,
        messages: InboundMessageOperations,
    ) -> None:
        data = _validated_data(event)
        self.event = event
        self.message = message
        self.id = message.id
        self.inbox = message.delivered_to
        self.conversation_id = message.conversation_id
        self.from_ = message.header_from
        self.envelope_from = message.envelope_from
        self.authentication: Optional[Authentication] = message.authentication
        self.verified = bool(
            message.authentication and message.authentication.dmarc.status == "pass"
        )
        self.to = list(message.to)
        self.cc = list(message.cc)
        self.reply_to = list(message.reply_to)
        self.reply_targets = (
            list(message.reply_to)
            if message.reply_to
            else ([message.header_from] if message.header_from is not None else [])
        )
        self.subject = message.subject
        self.text = message.parsed.text if message.parsed is not None else ""
        self.html = message.parsed.html if message.parsed is not None else None
        self.text_truncated = message.parsed.truncated if message.parsed is not None else False
        self.received_at = message.created_at
        self.flagged = bool(message.flagged)
        self.flag_reason = message.flag_reason
        self.attachments = [
            AsyncInboundAttachment(meta, self.inbox, self.id, messages)
            for meta in message.attachments
        ]
        self._messages = messages

    async def reply(
        self, body: Any, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        return await self._messages.reply(
            self.inbox, self.id, body, idempotency_key=idempotency_key
        )

    async def forward(
        self, body: Any, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        return await self._messages.forward(
            self.inbox, self.id, body, idempotency_key=idempotency_key
        )

    def to_dict(self) -> dict[str, Any]:
        result: dict[str, Any] = {
            "id": self.id,
            "inbox": self.inbox,
            "conversation_id": self.conversation_id,
            "from": self.from_,
            "envelope_from": self.envelope_from,
            "verified": self.verified,
            "to": list(self.to),
            "cc": list(self.cc),
            "reply_to": list(self.reply_to),
            "reply_targets": list(self.reply_targets),
            "subject": self.subject,
            "text": self.text,
            "text_truncated": self.text_truncated,
            "received_at": _iso_milliseconds(self.received_at),
            "flagged": self.flagged,
            "attachments": [attachment.to_dict() for attachment in self.attachments],
        }
        if self.html is not None:
            result["html"] = self.html
        if self.flag_reason is not None:
            result["flag_reason"] = self.flag_reason
        return result

    def __repr__(self) -> str:
        return f"AsyncInboundEmail({self.to_dict()!r})"


class AsyncInboundResource:
    def __init__(self, messages: InboundMessageOperations) -> None:
        self._messages = messages

    async def from_event(self, event: InboundEvent) -> AsyncInboundEmail:
        data = _validated_data(event)
        message = await self._messages.get(data["delivered_to"], data["message_id"])
        return AsyncInboundEmail(event, message, self._messages)


T = TypeVar("T")


class _Bridge(Protocol):
    def submit(self, coro: Coroutine[Any, Any, T]) -> T: ...


class InboundAttachment:
    """Blocking adapter over :class:`AsyncInboundAttachment`."""

    def __init__(self, target: AsyncInboundAttachment, bridge: _Bridge) -> None:
        self._target = target
        self._bridge = bridge
        self.content_id = target.content_id
        self.content_type = target.content_type
        self.filename = target.filename
        self.index = target.index
        self.size_bytes = target.size_bytes

    def get(self, *, inline: bool = False) -> AttachmentView:
        return self._bridge.submit(self._target.get(inline=inline))

    def to_dict(self) -> dict[str, Any]:
        return self._target.to_dict()

    def __repr__(self) -> str:
        return f"InboundAttachment({self.to_dict()!r})"


class InboundEmail:
    """Blocking adapter over :class:`AsyncInboundEmail`."""

    def __init__(self, target: AsyncInboundEmail, bridge: _Bridge) -> None:
        self._target = target
        self._bridge = bridge
        self.event = target.event
        self.message = target.message
        self.id = target.id
        self.inbox = target.inbox
        self.conversation_id = target.conversation_id
        self.from_ = target.from_
        self.envelope_from = target.envelope_from
        self.authentication = target.authentication
        self.verified = target.verified
        self.to = list(target.to)
        self.cc = list(target.cc)
        self.reply_to = list(target.reply_to)
        self.reply_targets = list(target.reply_targets)
        self.subject = target.subject
        self.text = target.text
        self.html = target.html
        self.text_truncated = target.text_truncated
        self.received_at = target.received_at
        self.flagged = target.flagged
        self.flag_reason = target.flag_reason
        self.attachments = [InboundAttachment(item, bridge) for item in target.attachments]

    def reply(
        self, body: Any, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        return self._bridge.submit(
            self._target.reply(body, idempotency_key=idempotency_key)
        )

    def forward(
        self, body: Any, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        return self._bridge.submit(
            self._target.forward(body, idempotency_key=idempotency_key)
        )

    def to_dict(self) -> dict[str, Any]:
        return self._target.to_dict()

    def __repr__(self) -> str:
        return f"InboundEmail({self.to_dict()!r})"


class InboundResource:
    """Blocking adapter over :class:`AsyncInboundResource`."""

    def __init__(self, target: AsyncInboundResource, bridge: _Bridge) -> None:
        self._target = target
        self._bridge = bridge

    def from_event(self, event: InboundEvent) -> InboundEmail:
        target = self._bridge.submit(self._target.from_event(event))
        return InboundEmail(target, self._bridge)

"""Webhook signature verification for the /v1 webhooks resource.

e2a signs each subscriber delivery with HMAC-SHA256 keyed by the webhook's
per-webhook ``signing_secret`` (``whsec_...``), Stripe-style::

    X-E2A-Signature: t=<unix>,v1=<hex>[,v1=<hex>]

During the 24h rotation grace each delivery carries two ``v1=`` pairs (one per
active secret); accept if ANY matches. The timestamp guards replay — reject
anything older than the tolerance (default 5 min). The signed payload is
``f"{t}.{raw_body}"`` — pass the RAW request body bytes; re-stringifying parsed
JSON will not match (whitespace / key-order differ).
"""

from __future__ import annotations

import hashlib
import hmac
import json
import math
import time
from dataclasses import dataclass, field
from typing import Any, List, Mapping, Optional, Sequence, Union

from typing_extensions import NotRequired, TypedDict, TypeGuard

from .errors import E2AWebhookSignatureError

__all__ = [
    "verify_webhook_signature",
    "construct_event",
    "WebhookEvent",
    # Typed per-event payloads (stable events).
    "AttachmentMeta",
    "EmailReceivedData",
    "EmailSentData",
    "EmailFailedData",
    "EmailDeliveredData",
    "EmailBouncedData",
    "EmailComplainedData",
    "DomainSendingVerifiedData",
    "DomainSendingFailedData",
    "DomainSuppressionAddedData",
    # Narrowing guards.
    "is_email_received",
    "is_email_sent",
    "is_email_failed",
    "is_email_delivered",
    "is_email_bounced",
    "is_email_complained",
    "is_domain_sending_verified",
    "is_domain_sending_failed",
    "is_domain_suppression_added",
]

Secret = Union[str, Sequence[str]]


def verify_webhook_signature(
    raw_body: Union[str, bytes],
    header: str,
    secret: Secret,
    *,
    tolerance_seconds: int = 300,
    now: Optional[float] = None,
) -> bool:
    """Verify an ``X-E2A-Signature`` header.

    Returns ``True`` on success and ``False`` on any failure (bad format,
    missing pair, signature mismatch, replay). Never raises.

    ``secret`` may be a single ``whsec_...`` string or a sequence of them (to
    verify one handler against several endpoints' secrets / a rotation grace).
    """
    # Guard a missing/non-string header so a missing X-E2A-Signature is a clean
    # False, never a raw AttributeError (WH-SIG-1).
    if not header or not isinstance(header, str):
        return False
    t = ""
    v1s: list[str] = []
    for part in header.split(","):
        trimmed = part.strip()
        if trimmed.startswith("t="):
            t = trimmed[2:]
        elif trimmed.startswith("v1="):
            v1s.append(trimmed[3:])
    if not t or not v1s:
        return False

    try:
        ts = float(t)
    except ValueError:
        return False
    # Reject non-finite timestamps: abs(now - nan) > tol is False, which would
    # silently disable the replay guard for a t=nan delivery.
    if not math.isfinite(ts):
        return False
    if now is None:
        now = time.time()
    if abs(now - ts) > tolerance_seconds:
        return False

    body_bytes = raw_body.encode("utf-8") if isinstance(raw_body, str) else raw_body
    signed_payload = t.encode("ascii") + b"." + body_bytes

    secrets = [secret] if isinstance(secret, str) else list(secret)
    for sec in secrets:
        expected = hmac.new(sec.encode("utf-8"), signed_payload, hashlib.sha256).hexdigest()
        for candidate in v1s:
            if len(candidate) != len(expected):
                continue
            if hmac.compare_digest(candidate, expected):
                return True
    return False


# ── Typed per-event `data` payloads (STABLE events) ─────────────────────────
#
# TypedDicts mirroring the server's canonical structs (internal/eventpayload)
# and the OpenAPI component schemas (EmailReceivedData, …), locked by the
# shared golden fixtures under internal/eventpayload/testdata. They are typing
# aids only — at runtime ``event.data`` stays the parsed dict, so unknown/beta
# event types (email.flagged, email.blocked, email.review_*) keep parsing as
# generic dicts. Narrow with the ``is_email_*`` / ``is_domain_*`` guards.


class AttachmentMeta(TypedDict):
    """Metadata for one attachment (never the bytes). ``index`` is the stable
    0-based fetch key for ``GET …/messages/{id}/attachments/{index}``."""

    filename: NotRequired[str]
    content_type: NotRequired[str]
    size_bytes: int
    index: int


# email.received / email.sent / email.failed carry the sender under the wire
# key "from" — a Python keyword — so these three use the functional TypedDict
# syntax. Access it as ``data["from"]``.

EmailReceivedData = TypedDict(
    "EmailReceivedData",
    {
        "message_id": str,
        "agent_email": str,
        "direction": str,  # always "inbound"
        "conversation_id": NotRequired[str],
        # Display/reply sender (prefers Reply-To); for the verified identity
        # use authenticated_from.
        "from": str,
        "authenticated_from": str,
        "to": List[str],
        "cc": NotRequired[List[str]],
        "reply_to": NotRequired[List[str]],
        # The one agent address this per-agent copy was delivered to — the
        # fetch key for the message.
        "delivered_to": str,
        "subject": str,
        # Signed X-E2A-Auth-* attestation (may be empty on the WS drain path).
        "auth_headers": Mapping[str, str],
        "received_at": str,
        "attachments": NotRequired[List[AttachmentMeta]],
    },
)
EmailReceivedData.__doc__ = (
    "``data`` of an ``email.received`` event — a metadata-only notification. "
    "``message_id`` + ``delivered_to`` are the fetch keys; retrieve the full "
    "message with ``client.webhooks.fetch_message(event)``. "
    "``authenticated_from`` — not ``from`` — is the SPF/DKIM/DMARC-verified identity."
)

EmailSentData = TypedDict(
    "EmailSentData",
    {
        "message_id": str,
        "agent_email": str,
        "direction": str,  # always "outbound"
        "conversation_id": NotRequired[str],
        # Provider-assigned (SES) id — the correlation key for the async
        # delivered/bounced/complained feedback events.
        "provider_message_id": str,
        "method": str,  # open set; known values: smtp
        "from": str,
        "to": List[str],
        "cc": NotRequired[List[str]],
        "bcc": NotRequired[List[str]],
        "subject": str,
        "message_type": str,  # open set; known values: send, reply, forward
    },
)
EmailSentData.__doc__ = (
    "``data`` of an ``email.sent`` event — the provider accepted an outbound "
    "send. Identical from the sync and async send paths."
)

EmailFailedData = TypedDict(
    "EmailFailedData",
    {
        "message_id": str,
        "agent_email": str,
        "direction": str,  # always "outbound"
        "conversation_id": NotRequired[str],
        "method": str,
        "from": str,
        "to": List[str],
        "cc": NotRequired[List[str]],
        "bcc": NotRequired[List[str]],
        "subject": str,
        "message_type": str,
        "reason": str,
        "reason_code": NotRequired[str],
        # Present only when the send path genuinely knows; absent != False.
        "retryable": NotRequired[bool],
    },
)
EmailFailedData.__doc__ = (
    "``data`` of an ``email.failed`` event — an outbound send terminally "
    "failed (retries exhausted / permanent reject)."
)


class EmailDeliveredData(TypedDict):
    """``data`` of an ``email.delivered`` event — per-recipient acceptance.
    The event TYPE is the outcome; there is no ``status`` field."""

    message_id: str
    agent_email: str
    direction: str  # always "outbound"
    delivered_to: str  # the one recipient this outcome is about
    subject: NotRequired[str]
    smtp_detail: NotRequired[str]


class EmailBouncedData(TypedDict):
    """``data`` of an ``email.bounced`` event — EmailDeliveredData's fields
    plus the SES bounce classification."""

    message_id: str
    agent_email: str
    direction: str  # always "outbound"
    delivered_to: str
    subject: NotRequired[str]
    smtp_detail: NotRequired[str]
    bounce_type: str  # "permanent" | "transient" | "undetermined"
    bounce_sub_type: NotRequired[str]


class EmailComplainedData(TypedDict):
    """``data`` of an ``email.complained`` event — a recipient marked an
    outbound message as spam."""

    message_id: str
    agent_email: str
    direction: str  # always "outbound"
    delivered_to: str
    subject: NotRequired[str]
    smtp_detail: NotRequired[str]


class DomainSendingVerifiedData(TypedDict):
    """``data`` of a ``domain.sending_verified`` event."""

    domain: str
    sending_status: str  # open set; known values: verified


class DomainSendingFailedData(TypedDict):
    """``data`` of a ``domain.sending_failed`` event."""

    domain: str
    sending_status: str  # open set; known values: failed
    reason: NotRequired[str]


class DomainSuppressionAddedData(TypedDict):
    """``data`` of a ``domain.suppression_added`` event — an address was
    auto-suppressed after a hard bounce/complaint. Account-scoped despite the
    ``domain.`` prefix."""

    address: str
    source: str  # "bounce" | "complaint"
    reason: NotRequired[str]
    message_id: NotRequired[str]


@dataclass(frozen=True)
class WebhookEvent:
    """A verified event envelope — the shape of a webhook delivery body, a
    ``GET /v1/events/{id}`` object, and a WebSocket frame. ``data`` is the
    per-event payload dict; it stays generic at the envelope level (unknown/
    beta event types must keep parsing) — narrow on ``type`` with the
    ``is_email_*`` / ``is_domain_*`` guards for the typed stable payloads."""

    type: str
    data: Any = None
    id: Optional[str] = None
    created_at: Optional[str] = None
    #: Envelope schema version (currently "1"). Branch on it before parsing
    #: ``data`` if you need forward-compatibility across envelope revisions.
    schema_version: Optional[str] = None
    #: The full parsed envelope (all fields, for forward-compatibility).
    raw: Mapping[str, Any] = field(default_factory=dict)


def construct_event(
    raw_body: Union[str, bytes],
    header: str,
    secret: Secret,
    *,
    tolerance_seconds: int = 300,
    now: Optional[float] = None,
) -> WebhookEvent:
    """Verify a delivery and parse it into a :class:`WebhookEvent` in one call
    (Stripe's ``construct_event`` shape). Raises
    :class:`~e2a.v1.errors.E2AWebhookSignatureError` on a bad signature, a replay
    outside tolerance, or an unparseable body. Recommended path — call it from
    your webhook handler with the RAW request body.
    """
    if not verify_webhook_signature(
        raw_body, header, secret, tolerance_seconds=tolerance_seconds, now=now
    ):
        raise _sig_error("webhook_signature_invalid", "webhook signature verification failed")

    text = raw_body.decode("utf-8") if isinstance(raw_body, bytes) else raw_body
    try:
        parsed = json.loads(text)
    except (ValueError, TypeError):
        raise _sig_error("webhook_body_invalid", "webhook body is not valid JSON")
    if not isinstance(parsed, dict) or not isinstance(parsed.get("type"), str):
        raise _sig_error("webhook_body_invalid", "webhook event is missing a string `type`")

    return WebhookEvent(
        type=parsed["type"],
        data=parsed.get("data"),
        id=parsed.get("id"),
        created_at=parsed.get("created_at"),
        schema_version=parsed.get("schema_version"),
        raw=parsed,
    )


def _sig_error(code: str, message: str) -> E2AWebhookSignatureError:
    return E2AWebhookSignatureError(code=code, message=message, status=0, retryable=False)


# ── Discriminated narrowing guards ──────────────────────────────────────────
#
# ``event.data`` narrows to the typed payload for type-checkers::
#
#     event = construct_event(raw_body, header, secret)
#     if is_email_received(event):
#         msg = await client.webhooks.fetch_message(event)
#     elif is_email_bounced(event):
#         print(event.data["bounce_type"], event.data["delivered_to"])
#     # unknown/beta types: handle event.data as a generic dict.


def is_email_received(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``email.received`` (``e.data``: :class:`EmailReceivedData`)."""
    return e.type == "email.received"


def is_email_sent(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``email.sent`` (``e.data``: :class:`EmailSentData`)."""
    return e.type == "email.sent"


def is_email_failed(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``email.failed`` (``e.data``: :class:`EmailFailedData`)."""
    return e.type == "email.failed"


def is_email_delivered(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``email.delivered`` (``e.data``: :class:`EmailDeliveredData`)."""
    return e.type == "email.delivered"


def is_email_bounced(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``email.bounced`` (``e.data``: :class:`EmailBouncedData`)."""
    return e.type == "email.bounced"


def is_email_complained(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``email.complained`` (``e.data``: :class:`EmailComplainedData`)."""
    return e.type == "email.complained"


def is_domain_sending_verified(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``domain.sending_verified`` (``e.data``: :class:`DomainSendingVerifiedData`)."""
    return e.type == "domain.sending_verified"


def is_domain_sending_failed(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``domain.sending_failed`` (``e.data``: :class:`DomainSendingFailedData`)."""
    return e.type == "domain.sending_failed"


def is_domain_suppression_added(e: WebhookEvent) -> TypeGuard[WebhookEvent]:
    """True iff the event is ``domain.suppression_added`` (``e.data``: :class:`DomainSuppressionAddedData`)."""
    return e.type == "domain.suppression_added"

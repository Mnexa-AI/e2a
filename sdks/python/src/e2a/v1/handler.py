"""High-level parsed email types and MIME parsing for e2a v1.

Contains :class:`InboundEmail`, value types (``Attachment``, ``AuthHeaders``,
``SendResult``, ``MessageSummary``, ``MessageList``), and helpers for
decoding raw RFC 2822 messages.
"""

from __future__ import annotations

import base64
import email as email_lib
import hashlib
import hmac
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from email.policy import default as default_policy
from typing import TYPE_CHECKING, Any, Optional


# Replay window matches the Go server's headers.DefaultMaxAge.
_REPLAY_WINDOW = timedelta(minutes=5)
# Tolerance for clock skew where the timestamp appears slightly in the
# future relative to local time. Matches the server's negative-skew
# allowance.
_FUTURE_SKEW = timedelta(seconds=30)

if TYPE_CHECKING:
    from e2a.v1.async_client import AsyncE2AClient
    from e2a.v1.client import E2AClient


@dataclass
class AuthHeaders:
    """Parsed e2a authentication headers from a webhook payload.

    Matches the signed auth surface defined in the server contract:
    Verified, Sender, EntityType, DomainCheck, Delegation, Signature,
    Timestamp, MessageID, BodyHash.

    .. warning::
       The :attr:`verified` field is the raw ``X-E2A-Auth-Verified``
       header value — i.e. the *server's claim*. It is **not** a
       cryptographic verification. To confirm the headers were actually
       signed by your e2a instance and bound to this exact message,
       call :meth:`InboundEmail.verify_signature(secret)` and trust the
       result, not ``email.is_verified``.
    """

    verified: bool
    sender: str
    entity_type: str  # "human" or "agent"
    domain_check: str
    delegation: str
    signature: str
    timestamp: str
    message_id: str
    body_hash: str

    @classmethod
    def from_headers(cls, headers: dict[str, str]) -> AuthHeaders:
        return cls(
            verified=headers.get("X-E2A-Auth-Verified", "").lower() == "true",
            sender=headers.get("X-E2A-Auth-Sender", ""),
            entity_type=headers.get("X-E2A-Auth-Entity-Type", ""),
            domain_check=headers.get("X-E2A-Auth-Domain-Check", ""),
            delegation=headers.get("X-E2A-Auth-Delegation", ""),
            signature=headers.get("X-E2A-Auth-Signature", ""),
            timestamp=headers.get("X-E2A-Auth-Timestamp", ""),
            message_id=headers.get("X-E2A-Auth-Message-Id", ""),
            body_hash=headers.get("X-E2A-Auth-Body-Hash", ""),
        )


def _canonical_string(h: AuthHeaders) -> str:
    """Reconstruct the byte string fed to HMAC. Field order must match
    the Go server's headers.canonicalString — changing it is a wire
    contract change.
    """
    verified = "true" if h.verified else "false"
    return "\n".join([
        verified,
        h.sender,
        h.entity_type,
        h.domain_check,
        h.delegation,
        h.timestamp,
        h.message_id,
        h.body_hash,
    ])


def _verify_auth_headers(
    h: AuthHeaders,
    raw_message: bytes,
    secret: str,
    *,
    max_age: timedelta = _REPLAY_WINDOW,
) -> bool:
    """Verify HMAC, body hash, and timestamp window.

    Returns True only if all three checks pass:
    1. Body bytes hash to the signed body_hash
    2. Signature is HMAC-SHA256 of canonical string under secret
    3. Timestamp is within the replay window (and not skewed too far
       into the future)
    """
    if not h.signature:
        return False

    # Bind to the actual body bytes the recipient received.
    if not hmac.compare_digest(
        h.body_hash, hashlib.sha256(raw_message).hexdigest()
    ):
        return False

    # Reject obvious tampering or unset timestamp.
    try:
        ts = datetime.fromisoformat(h.timestamp.replace("Z", "+00:00"))
    except ValueError:
        return False
    if ts.tzinfo is None:
        ts = ts.replace(tzinfo=timezone.utc)
    age = datetime.now(timezone.utc) - ts
    if age < -_FUTURE_SKEW or age > max_age:
        return False

    expected = hmac.new(
        secret.encode(), _canonical_string(h).encode(), hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(h.signature, expected)


@dataclass
class Attachment:
    """An email attachment."""

    filename: str
    content_type: str
    data: bytes
    size: int


@dataclass
class SendResult:
    """Result from sending an email or reply."""

    status: str
    message_id: str
    method: str  # "smtp" or "webhook"


@dataclass
class MessageSummary:
    """A message summary from the list endpoint (no body content)."""

    message_id: str
    conversation_id: str | None
    sender: str
    recipient: str
    subject: str
    status: str  # "unread" or "read"
    created_at: str


@dataclass
class MessageList:
    """Response from get_messages()."""

    messages: list[MessageSummary]
    next_token: str | None


class InboundEmail:
    """A parsed inbound email with convenience methods.

    Attributes:
        message_id: Unique e2a message identifier.
        conversation_id: Opaque thread/conversation ID, or ``None``.
        sender: Sender email address.
        recipient: Recipient email address (your agent).
        subject: Email subject line.
        cc: List of CC'd email addresses.
        text_body: Plain-text email body.
        html_body: HTML email body, or ``None``.
        attachments: Parsed file attachments.
        auth: Parsed authentication headers.
        received_at: Timestamp when the message was received, or ``None``.
        raw_message: Raw RFC 2822 email bytes.
    """

    def __init__(
        self,
        *,
        message_id: str,
        conversation_id: Optional[str],
        sender: str,
        recipient: str,
        subject: str,
        cc: list[str],
        text_body: str,
        html_body: Optional[str],
        attachments: list[Attachment],
        auth: AuthHeaders,
        received_at: Optional[str],
        raw_message: bytes,
        client: E2AClient,
    ) -> None:
        self.message_id = message_id
        self.conversation_id = conversation_id
        self.sender = sender
        self.recipient = recipient
        self.subject = subject
        self.cc = cc
        self.text_body = text_body
        self.html_body = html_body
        self.attachments = attachments
        self.auth = auth
        self.received_at = received_at
        self.raw_message = raw_message
        self._client = client

    @property
    def is_verified(self) -> bool:
        """The server's *claim* that the sender's domain passed SPF/DKIM.

        .. warning::
           This reflects the value of the ``X-E2A-Auth-Verified`` header
           and does **not** verify the HMAC signature. Anyone who can
           POST to your webhook URL can set this to ``True``. Call
           :meth:`verify_signature` and trust its return value before
           making security decisions.
        """
        return self.auth.verified

    def verify_signature(self, secret: str) -> bool:
        """Cryptographically verify the auth headers were issued by an
        e2a instance holding ``secret`` and are bound to this exact
        message.

        Checks (in order):

        1. The SHA-256 of :attr:`raw_message` matches
           ``X-E2A-Auth-Body-Hash``
        2. The HMAC-SHA256 over the canonical string under ``secret``
           matches ``X-E2A-Auth-Signature``
        3. ``X-E2A-Auth-Timestamp`` is within the 5-minute replay window

        Returns ``True`` if all three pass. Returns ``False`` for any
        tampering, expired timestamp, missing field, or wrong secret —
        callers should treat ``False`` as untrusted regardless of the
        :attr:`is_verified` claim.
        """
        return _verify_auth_headers(self.auth, self.raw_message, secret)

    def reply(
        self,
        body: str,
        html_body: Optional[str] = None,
        reply_all: Optional[bool] = None,
        cc: Optional[list[str]] = None,
        bcc: Optional[list[str]] = None,
        conversation_id: Optional[str] = None,
        attachments: Optional[list[Attachment]] = None,
    ) -> SendResult:
        """Reply to this email.

        Uses ``self.recipient`` as the agent email automatically.
        """
        return self._client.reply(
            self.message_id,
            body,
            html_body=html_body,
            reply_all=reply_all,
            cc=cc,
            bcc=bcc,
            conversation_id=conversation_id,
            attachments=attachments,
            agent_email=self.recipient,
        )

    def __repr__(self) -> str:
        return (
            f"InboundEmail(message_id={self.message_id!r}, sender={self.sender!r}, "
            f"subject={self.subject!r}, conversation_id={self.conversation_id!r})"
        )


# ── Parsing helpers ───────────────────────────────────────────────


def _parse_address_list(header_value: str | None) -> list[str]:
    """Parse an email header value into a list of addresses."""
    if not header_value:
        return []
    from email.utils import getaddresses
    return [addr for _, addr in getaddresses([header_value]) if addr]


def parse_raw_email(raw: bytes) -> tuple[str, str, Optional[str], list[Attachment], list[str]]:
    """Extract subject, text body, HTML body, attachments, and CC from raw RFC 2822."""
    try:
        msg = email_lib.message_from_bytes(raw, policy=default_policy)
    except Exception:
        return "", "", None, [], []

    subject = str(msg.get("Subject", ""))
    cc = _parse_address_list(msg.get("Cc"))

    text_body = ""
    html_body = None
    attachments: list[Attachment] = []

    if msg.is_multipart():
        for part in msg.walk():
            ct = part.get_content_type()
            disposition = part.get_content_disposition()

            if ct.startswith("multipart/"):
                continue

            if disposition in ("attachment", "inline"):
                try:
                    data = part.get_payload(decode=True) or b""
                except Exception:
                    continue
                attachments.append(Attachment(
                    filename=part.get_filename() or "unnamed",
                    content_type=ct,
                    data=data,
                    size=len(data),
                ))
                continue

            if ct == "text/plain" and not text_body:
                try:
                    text_body = part.get_content()
                except Exception:
                    pass
            elif ct == "text/html" and html_body is None:
                try:
                    html_body = part.get_content()
                except Exception:
                    pass
            elif not ct.startswith("text/"):
                try:
                    data = part.get_payload(decode=True) or b""
                except Exception:
                    continue
                attachments.append(Attachment(
                    filename=part.get_filename() or "unnamed",
                    content_type=ct,
                    data=data,
                    size=len(data),
                ))
    else:
        try:
            content = msg.get_content()
        except Exception:
            content = ""
        if msg.get_content_type() == "text/html":
            html_body = content
        else:
            text_body = content

    return subject, text_body, html_body, attachments, cc


def _decode_raw_message(data: dict[str, Any] | str | None) -> bytes:
    """Decode a base64 raw_message field to bytes."""
    if not data:
        return b""
    raw = data if isinstance(data, str) else ""
    if isinstance(data, dict):
        raw = data.get("raw_message", "") or ""
    if not raw:
        return b""
    try:
        return base64.b64decode(raw)
    except Exception:
        return raw.encode() if isinstance(raw, str) else b""


def _parse_payload(data: dict[str, Any]) -> dict[str, Any]:
    """Shared parsing logic for both sync and async builders."""
    raw_message = _decode_raw_message(data)
    subject, text_body, html_body, attachments, cc = parse_raw_email(raw_message)
    auth_headers = data.get("auth_headers", {}) or {}
    received_at = data.get("created_at") or data.get("received_at") or None

    return {
        "message_id": data.get("message_id", ""),
        "conversation_id": data.get("conversation_id"),
        "sender": data.get("from", ""),
        "recipient": data.get("to", ""),
        "subject": subject or data.get("subject", ""),
        "cc": cc,
        "text_body": text_body,
        "html_body": html_body,
        "attachments": attachments,
        "auth": AuthHeaders.from_headers(auth_headers),
        "received_at": received_at,
        "raw_message": raw_message,
    }


def build_inbound_email(
    data: dict[str, Any],
    client: E2AClient,
) -> InboundEmail:
    """Build an InboundEmail from a webhook/API JSON payload."""
    fields = _parse_payload(data)
    return InboundEmail(**fields, client=client)


class AsyncInboundEmail:
    """Async version of :class:`InboundEmail`.

    Identical fields, but ``.reply()`` is an async method.
    """

    def __init__(
        self,
        *,
        message_id: str,
        conversation_id: Optional[str],
        sender: str,
        recipient: str,
        subject: str,
        cc: list[str],
        text_body: str,
        html_body: Optional[str],
        attachments: list[Attachment],
        auth: AuthHeaders,
        received_at: Optional[str],
        raw_message: bytes,
        client: AsyncE2AClient,
    ) -> None:
        self.message_id = message_id
        self.conversation_id = conversation_id
        self.sender = sender
        self.recipient = recipient
        self.subject = subject
        self.cc = cc
        self.text_body = text_body
        self.html_body = html_body
        self.attachments = attachments
        self.auth = auth
        self.received_at = received_at
        self.raw_message = raw_message
        self._client = client

    @property
    def is_verified(self) -> bool:
        """The server's *claim* that the sender's domain passed SPF/DKIM.

        .. warning::
           See :attr:`InboundEmail.is_verified` — this is **not** a
           cryptographic verification. Use :meth:`verify_signature`.
        """
        return self.auth.verified

    def verify_signature(self, secret: str) -> bool:
        """Cryptographically verify the auth headers under ``secret``.

        See :meth:`InboundEmail.verify_signature` — identical contract.
        """
        return _verify_auth_headers(self.auth, self.raw_message, secret)

    async def reply(
        self,
        body: str,
        html_body: Optional[str] = None,
        reply_all: Optional[bool] = None,
        cc: Optional[list[str]] = None,
        bcc: Optional[list[str]] = None,
        conversation_id: Optional[str] = None,
        attachments: Optional[list[Attachment]] = None,
    ) -> SendResult:
        """Reply to this email (async).

        Uses ``self.recipient`` as the agent email automatically.
        """
        return await self._client.reply(
            self.message_id,
            body,
            html_body=html_body,
            reply_all=reply_all,
            cc=cc,
            bcc=bcc,
            conversation_id=conversation_id,
            attachments=attachments,
            agent_email=self.recipient,
        )

    def __repr__(self) -> str:
        return (
            f"AsyncInboundEmail(message_id={self.message_id!r}, sender={self.sender!r}, "
            f"subject={self.subject!r}, conversation_id={self.conversation_id!r})"
        )


def build_inbound_email_async(
    data: dict[str, Any],
    client: AsyncE2AClient,
) -> AsyncInboundEmail:
    """Build an AsyncInboundEmail from a webhook/API JSON payload."""
    fields = _parse_payload(data)
    return AsyncInboundEmail(**fields, client=client)

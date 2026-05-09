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
import os
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


_warned_legacy_env = False


def _resolve_webhook_secret() -> str:
    """Read the webhook signing secret from env, preferring the new name.

    Order: ``E2A_WEBHOOK_SECRET`` (canonical) → ``E2A_HMAC_SECRET`` (legacy
    alias, kept for backward compatibility with SDK 2.0). Emits a one-time
    DeprecationWarning when only the legacy name is set so users notice
    before the alias is removed in a future major release.
    """
    val = os.environ.get("E2A_WEBHOOK_SECRET", "")
    if val:
        return val
    legacy = os.environ.get("E2A_HMAC_SECRET", "")
    if legacy:
        global _warned_legacy_env
        if not _warned_legacy_env:
            import warnings
            warnings.warn(
                "E2A_HMAC_SECRET is deprecated; rename it to E2A_WEBHOOK_SECRET. "
                "The legacy name will be removed in a future major release.",
                DeprecationWarning,
                stacklevel=3,
            )
            _warned_legacy_env = True
        return legacy
    return ""


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
    to: list[str]
    cc: list[str]
    subject: str
    status: str  # "unread" or "read"
    created_at: str


@dataclass
class MessageList:
    """Response from get_messages()."""

    messages: list[MessageSummary]
    next_token: str | None


class UnverifiedEmailError(RuntimeError):
    """Raised when accessing claim fields on an InboundEmail before
    :meth:`InboundEmail.verify_signature` has succeeded.

    This is a security feature: the SDK refuses to expose
    attacker-controllable fields (sender, recipient, body, subject, …)
    until you've cryptographically verified the payload. Catch this
    exception only to handle a known unverified path; treat its presence
    in production logs as a bug to fix by calling :meth:`verify_signature`
    or using :meth:`E2AClient.parse_webhook` (which verifies for you).

    If you genuinely need to inspect the raw payload without verifying
    (e.g. for forensics on a malformed delivery), use
    :attr:`InboundEmail.unverified_payload` — explicit, named, and
    documented as attacker-controllable.
    """


class InboundEmail:
    """A parsed inbound email with convenience methods.

    Field access is gated behind :meth:`verify_signature`: properties
    like :attr:`sender`, :attr:`recipient`, :attr:`text_body` raise
    :class:`UnverifiedEmailError` until verify succeeds. This makes it
    impossible to accidentally trust attacker-supplied data.

    Recommended entry point for webhook handlers is
    :meth:`E2AClient.parse_webhook`, which combines parse + verify in
    one call and returns an already-verified :class:`InboundEmail`.

    Always-available (un-gated) members:
        - :attr:`auth` — needed by :meth:`verify_signature` itself
        - :attr:`raw_message` — same
        - :attr:`is_verified` — server's *claim*, not a check
        - :attr:`unverified_payload` — explicit escape hatch
        - :meth:`verify_signature` — the gate
        - :attr:`verified` — has verify_signature succeeded yet?

    Gated (require verify_signature first):
        message_id, conversation_id, sender, recipient, to, cc, subject,
        text_body, html_body, attachments, received_at, reply().
    """

    def __init__(
        self,
        *,
        message_id: str,
        conversation_id: Optional[str],
        sender: str,
        recipient: str,
        to: list[str],
        cc: list[str],
        subject: str,
        text_body: str,
        html_body: Optional[str],
        attachments: list[Attachment],
        auth: AuthHeaders,
        received_at: Optional[str],
        raw_message: bytes,
        client: E2AClient,
    ) -> None:
        # Stored as private fields. Public access flows through @property
        # gates that check self._verified. The constructor takes the
        # parsed values as-is — verification happens explicitly later.
        self._message_id = message_id
        self._conversation_id = conversation_id
        self._sender = sender
        self._recipient = recipient
        self._to = to
        self._cc = cc
        self._subject = subject
        self._text_body = text_body
        self._html_body = html_body
        self._attachments = attachments
        self._auth = auth
        self._received_at = received_at
        self._raw_message = raw_message
        self._client = client
        self._verified = False

    # --- Always-available (verification inputs + meta) ---

    @property
    def auth(self) -> AuthHeaders:
        """Parsed authentication headers (input to :meth:`verify_signature`)."""
        return self._auth

    @property
    def raw_message(self) -> bytes:
        """Raw RFC 2822 bytes (input to :meth:`verify_signature`)."""
        return self._raw_message

    @property
    def verified(self) -> bool:
        """True if :meth:`verify_signature` has succeeded on this instance."""
        return self._verified

    @property
    def is_verified(self) -> bool:
        """The server's *claim* that the sender's domain passed SPF/DKIM.

        .. warning::
           This reflects the value of the ``X-E2A-Auth-Verified`` header
           and does **not** verify the HMAC signature. Anyone who can
           POST to your webhook URL can set this to ``True``. Call
           :meth:`verify_signature` and check :attr:`verified` instead
           for security decisions.
        """
        return self._auth.verified

    @property
    def unverified_payload(self) -> dict:
        """Inspect the parsed payload **without** HMAC verification.

        Returns a dict of the parsed fields (sender, recipient, subject,
        body text, etc.). The returned values are attacker-controllable
        until verify_signature succeeds — never feed them into security
        or identity decisions. Useful only for debugging delivery issues.
        """
        return {
            "message_id": self._message_id,
            "conversation_id": self._conversation_id,
            "sender": self._sender,
            "recipient": self._recipient,
            "to": list(self._to),
            "cc": list(self._cc),
            "subject": self._subject,
            "text_body": self._text_body,
            "html_body": self._html_body,
            "received_at": self._received_at,
            "attachments_count": len(self._attachments),
        }

    def verify_signature(self, secret: Optional[str] = None) -> bool:
        """Cryptographically verify the auth headers and unlock field access.

        On success, transitions this instance to the "verified" state
        so subsequent property reads (sender, subject, body, …) work.

        ``secret`` defaults to the ``E2A_WEBHOOK_SECRET`` environment
        variable when omitted (with ``E2A_HMAC_SECRET`` accepted as a
        deprecated alias).

        Most webhook handlers should use :meth:`E2AClient.parse_webhook`
        instead — it calls ``verify_signature`` internally and raises
        ``PermissionError`` on failure, so the handler reads as one
        concise call. Use ``verify_signature`` directly only when you
        need to inspect ``unverified_payload`` first or have some other
        reason to keep the unverified object around.

        Checks (in order):

        1. ``SHA-256(raw_message)`` matches ``X-E2A-Auth-Body-Hash``
        2. HMAC-SHA256 of the canonical string under ``secret`` matches
           ``X-E2A-Auth-Signature``
        3. ``X-E2A-Auth-Timestamp`` is within the 5-minute replay window

        Returns ``True`` if all three pass. Returns ``False`` for any
        tampering / expired / wrong-secret — instance stays in the
        unverified state and field access keeps raising.

        Raises ``ValueError`` if no secret is available (neither passed
        nor in the environment) — better than silently treating "" as
        a verify attempt that always fails.
        """
        if secret is None:
            secret = _resolve_webhook_secret()
        if not secret:
            raise ValueError(
                "verify_signature requires a secret. Pass it explicitly "
                "or set E2A_WEBHOOK_SECRET in the environment."
            )
        ok = _verify_auth_headers(self._auth, self._raw_message, secret)
        if ok:
            self._verified = True
        return ok

    # --- Gated claim fields (require verify_signature first) ---

    def _require_verified(self) -> None:
        if not self._verified:
            raise UnverifiedEmailError(
                "Call verify_signature(secret) before accessing this field. "
                "For inspection without verification, use .unverified_payload."
            )

    @property
    def message_id(self) -> str:
        self._require_verified()
        return self._message_id

    @property
    def conversation_id(self) -> Optional[str]:
        self._require_verified()
        return self._conversation_id

    @property
    def sender(self) -> str:
        self._require_verified()
        return self._sender

    @property
    def recipient(self) -> str:
        self._require_verified()
        return self._recipient

    @property
    def to(self) -> list[str]:
        self._require_verified()
        return self._to

    @property
    def cc(self) -> list[str]:
        self._require_verified()
        return self._cc

    @property
    def subject(self) -> str:
        self._require_verified()
        return self._subject

    @property
    def text_body(self) -> str:
        self._require_verified()
        return self._text_body

    @property
    def html_body(self) -> Optional[str]:
        self._require_verified()
        return self._html_body

    @property
    def attachments(self) -> list[Attachment]:
        self._require_verified()
        return self._attachments

    @property
    def received_at(self) -> Optional[str]:
        self._require_verified()
        return self._received_at

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
        # repr is for debugging — pulls from the private fields directly
        # so it works on both verified and unverified instances. The
        # rendered values are still attacker-controllable on an
        # unverified email; treat repr output as untrusted for security.
        state = "verified" if self._verified else "UNVERIFIED"
        return (
            f"InboundEmail<{state}>(message_id={self._message_id!r}, "
            f"sender={self._sender!r}, subject={self._subject!r}, "
            f"conversation_id={self._conversation_id!r})"
        )


# ── Parsing helpers ───────────────────────────────────────────────


def parse_raw_email(raw: bytes) -> tuple[str, str, Optional[str], list[Attachment]]:
    """Extract subject, text body, HTML body, and attachments from raw RFC 2822.

    The To/Cc address lists are not parsed here: the server emits them as
    structured fields on every payload, so re-parsing the raw bytes would
    duplicate work and risk drifting from the server's interpretation.
    """
    try:
        msg = email_lib.message_from_bytes(raw, policy=default_policy)
    except Exception:
        return "", "", None, []

    subject = str(msg.get("Subject", ""))

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

    return subject, text_body, html_body, attachments


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
    """Shared parsing logic for both sync and async builders.

    The To/Cc address lists and the per-delivery target come straight from
    the server's structured fields. The raw message is still parsed for the
    body and attachments, which the server doesn't break out into JSON.
    """
    raw_message = _decode_raw_message(data)
    subject, text_body, html_body, attachments = parse_raw_email(raw_message)
    auth_headers = data.get("auth_headers", {}) or {}
    received_at = data.get("created_at") or data.get("received_at") or None

    return {
        "message_id": data.get("message_id", ""),
        "conversation_id": data.get("conversation_id"),
        "sender": data.get("from", ""),
        "recipient": data.get("recipient", ""),
        "to": list(data.get("to") or []),
        "cc": list(data.get("cc") or []),
        "subject": subject or data.get("subject", ""),
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
    *,
    trusted: bool = False,
) -> InboundEmail:
    """Build an InboundEmail from a webhook/API JSON payload.

    ``trusted=True`` marks the result as already-verified — used by the
    REST polling path (``client.get_message``), which fetched the data
    over the authenticated API channel. The webhook path leaves
    ``trusted=False`` (default) so the user must explicitly call
    ``verify_signature`` before reading claim fields.
    """
    fields = _parse_payload(data)
    email = InboundEmail(**fields, client=client)
    if trusted:
        email._verified = True
    return email


class AsyncInboundEmail:
    """Async mirror of :class:`InboundEmail`.

    Identical gating semantics: claim fields raise
    :class:`UnverifiedEmailError` until :meth:`verify_signature` succeeds.
    Only ``.reply()`` differs — async method instead of sync.
    """

    def __init__(
        self,
        *,
        message_id: str,
        conversation_id: Optional[str],
        sender: str,
        recipient: str,
        to: list[str],
        cc: list[str],
        subject: str,
        text_body: str,
        html_body: Optional[str],
        attachments: list[Attachment],
        auth: AuthHeaders,
        received_at: Optional[str],
        raw_message: bytes,
        client: AsyncE2AClient,
    ) -> None:
        self._message_id = message_id
        self._conversation_id = conversation_id
        self._sender = sender
        self._recipient = recipient
        self._to = to
        self._cc = cc
        self._subject = subject
        self._text_body = text_body
        self._html_body = html_body
        self._attachments = attachments
        self._auth = auth
        self._received_at = received_at
        self._raw_message = raw_message
        self._client = client
        self._verified = False

    # --- Always-available ---

    @property
    def auth(self) -> AuthHeaders:
        return self._auth

    @property
    def raw_message(self) -> bytes:
        return self._raw_message

    @property
    def verified(self) -> bool:
        return self._verified

    @property
    def is_verified(self) -> bool:
        """Server's *claim*. See :attr:`InboundEmail.is_verified` — not a check."""
        return self._auth.verified

    @property
    def unverified_payload(self) -> dict:
        """See :attr:`InboundEmail.unverified_payload`."""
        return {
            "message_id": self._message_id,
            "conversation_id": self._conversation_id,
            "sender": self._sender,
            "recipient": self._recipient,
            "to": list(self._to),
            "cc": list(self._cc),
            "subject": self._subject,
            "text_body": self._text_body,
            "html_body": self._html_body,
            "received_at": self._received_at,
            "attachments_count": len(self._attachments),
        }

    def verify_signature(self, secret: Optional[str] = None) -> bool:
        """See :meth:`InboundEmail.verify_signature` — identical contract."""
        if secret is None:
            secret = _resolve_webhook_secret()
        if not secret:
            raise ValueError(
                "verify_signature requires a secret. Pass it explicitly "
                "or set E2A_WEBHOOK_SECRET in the environment."
            )
        ok = _verify_auth_headers(self._auth, self._raw_message, secret)
        if ok:
            self._verified = True
        return ok

    # --- Gated claim fields ---

    def _require_verified(self) -> None:
        if not self._verified:
            raise UnverifiedEmailError(
                "Call verify_signature(secret) before accessing this field. "
                "For inspection without verification, use .unverified_payload."
            )

    @property
    def message_id(self) -> str:
        self._require_verified()
        return self._message_id

    @property
    def conversation_id(self) -> Optional[str]:
        self._require_verified()
        return self._conversation_id

    @property
    def sender(self) -> str:
        self._require_verified()
        return self._sender

    @property
    def recipient(self) -> str:
        self._require_verified()
        return self._recipient

    @property
    def to(self) -> list[str]:
        self._require_verified()
        return self._to

    @property
    def cc(self) -> list[str]:
        self._require_verified()
        return self._cc

    @property
    def subject(self) -> str:
        self._require_verified()
        return self._subject

    @property
    def text_body(self) -> str:
        self._require_verified()
        return self._text_body

    @property
    def html_body(self) -> Optional[str]:
        self._require_verified()
        return self._html_body

    @property
    def attachments(self) -> list[Attachment]:
        self._require_verified()
        return self._attachments

    @property
    def received_at(self) -> Optional[str]:
        self._require_verified()
        return self._received_at

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
        state = "verified" if self._verified else "UNVERIFIED"
        return (
            f"AsyncInboundEmail<{state}>(message_id={self._message_id!r}, "
            f"sender={self._sender!r}, subject={self._subject!r}, "
            f"conversation_id={self._conversation_id!r})"
        )


def build_inbound_email_async(
    data: dict[str, Any],
    client: AsyncE2AClient,
    *,
    trusted: bool = False,
) -> AsyncInboundEmail:
    """Build an AsyncInboundEmail. See :func:`build_inbound_email` for
    the meaning of ``trusted``.
    """
    fields = _parse_payload(data)
    email = AsyncInboundEmail(**fields, client=client)
    if trusted:
        email._verified = True
    return email

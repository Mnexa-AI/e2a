"""High-level sync client for the e2a v1 API.

Wraps :class:`E2AApi` with ergonomic methods that return parsed
:class:`InboundEmail` objects and handle agent email resolution.
"""

from __future__ import annotations

import base64
import json
import os
import warnings
from typing import Any, Optional

from e2a.v1.api import E2AApi
from e2a.v1.generated import (
    ApprovePendingMessageRequest,
    MessageDetail,
    RegisterAgentRequest,
    RegisterDomainRequest,
    ReplyToMessageRequest,
    SendEmailRequest,
    UpdateAgentRequest,
)
from e2a.v1.generated import internal_agent
from e2a.v1.handler import (
    Attachment,
    InboundEmail,
    MessageList,
    MessageSummary,
    SendResult,
    build_inbound_email,
)


def _serialize_attachments(
    attachments: list[Attachment] | None,
) -> list[internal_agent.Attachment] | None:
    """Convert high-level Attachment objects to generated wire format."""
    if not attachments:
        return None
    return [
        internal_agent.Attachment(
            filename=att.filename,
            content_type=att.content_type,
            data=base64.b64encode(att.data).decode(),
        )
        for att in attachments
    ]


class E2AClient:
    """High-level sync client for the e2a v1 API.

    Wraps the raw :class:`E2AApi` with convenience methods that return
    parsed :class:`InboundEmail` objects.

    Args:
        api_key: Your API key.
            Falls back to the ``E2A_API_KEY`` environment variable.
        agent_email: Default agent email address.
            Falls back to ``E2A_AGENT_EMAIL``.
        base_url: e2a API base URL. Defaults to ``https://e2a.dev``.
        timeout: Request timeout in seconds. Defaults to 30.
    """

    def __init__(
        self,
        api_key: Optional[str] = None,
        agent_email: Optional[str] = None,
        base_url: str = "https://e2a.dev",
        timeout: float = 30,
    ) -> None:
        resolved_key = api_key or os.environ.get("E2A_API_KEY", "")
        self.agent_email = agent_email or os.environ.get("E2A_AGENT_EMAIL", "")
        self.api = E2AApi(api_key=resolved_key, base_url=base_url, timeout=timeout)

    def _require_agent_email(self, agent_email: Optional[str] = None) -> str:
        email = agent_email or self.agent_email
        if not email:
            raise ValueError(
                "agent_email is required. Pass it to E2AClient(), set E2A_AGENT_EMAIL, "
                "or use InboundEmail.reply() which auto-resolves it from the payload."
            )
        return email

    # ── Parsing ───────────────────────────────────────────────────────

    def parse(
        self,
        body: bytes | str | dict[str, Any] | MessageDetail,
        headers: dict[str, str] | None = None,
    ) -> InboundEmail:
        """Parse a webhook payload or MessageDetail into an InboundEmail.

        .. deprecated:: 2.2
           Use :meth:`parse_webhook` for webhook handlers (parse + verify
           in one call) or :attr:`InboundEmail.unverified_payload` for
           inspection without verification. ``parse`` will be removed in
           3.0.

        Accepts bytes, JSON string, dict, or a generated MessageDetail.

        The returned InboundEmail starts in the *unverified* state —
        property accesses (sender, subject, body, …) raise
        :class:`UnverifiedEmailError` until :meth:`InboundEmail.verify_signature`
        succeeds. The combination of "looks usable" + "blows up on first
        field access" is precisely the trap that motivated the deprecation;
        ``parse_webhook`` raises immediately on bad signatures and returns
        a ready-to-use object on success.
        """
        warnings.warn(
            "E2AClient.parse() is deprecated and will be removed in 3.0. "
            "For webhook handlers, use client.parse_webhook(body) — it "
            "parses and HMAC-verifies in one call. For inspection without "
            "verification, use email.unverified_payload after parse_webhook.",
            DeprecationWarning,
            stacklevel=2,
        )
        return self._parse_unverified(body)

    def _parse_unverified(
        self,
        body: bytes | str | dict[str, Any] | MessageDetail,
    ) -> InboundEmail:
        """Internal parse without the deprecation warning. ``parse_webhook``
        delegates here so the recommended path doesn't emit the warning
        meant for direct ``parse`` callers."""
        if isinstance(body, MessageDetail):
            data = body.model_dump(by_alias=True)
        elif isinstance(body, dict):
            data = body
        elif isinstance(body, (bytes, str)):
            data = json.loads(body)
        else:
            raise TypeError(f"Unsupported body type: {type(body)}")

        return build_inbound_email(data, self)

    def parse_webhook(
        self,
        body: bytes | str | dict[str, Any] | MessageDetail,
        secret: Optional[str] = None,
    ) -> InboundEmail:
        """Parse + HMAC-verify a webhook payload in one call.

        Recommended entry point for webhook handlers. Returns an
        already-verified :class:`InboundEmail` — field access just
        works. Raises :class:`PermissionError` on signature failure
        (so a webhook handler can let the exception bubble to a 401).

        ``secret`` defaults to the ``E2A_WEBHOOK_SECRET`` environment
        variable (with ``E2A_HMAC_SECRET`` accepted as a deprecated alias).
        """
        email = self._parse_unverified(body)
        if not email.verify_signature(secret):
            raise PermissionError("HMAC signature verification failed")
        return email

    # ── Messages ──────────────────────────────────────────────────────

    def get_message(
        self,
        message_id: str,
        agent_email: Optional[str] = None,
    ) -> InboundEmail:
        """Fetch a single message and return a parsed InboundEmail.

        Marks the message as read.

        The returned email is **pre-verified** (``trusted=True``) — the
        REST API channel is authenticated by the bearer token, so an
        additional HMAC verify on the response would be redundant. This
        differs from :meth:`parse` (webhook entry), which leaves the
        email unverified until you explicitly verify it.
        """
        email = self._require_agent_email(agent_email)
        detail = self.api.get_message(email, message_id)
        data = detail.model_dump(by_alias=True)
        return build_inbound_email(data, self, trusted=True)

    def get_messages(
        self,
        status: str = "unread",
        page_size: int = 50,
        token: Optional[str] = None,
        agent_email: Optional[str] = None,
    ) -> MessageList:
        """Fetch message summaries with ergonomic field names."""
        email = self._require_agent_email(agent_email)
        resp = self.api.list_messages(email, status=status, page_size=page_size, token=token)
        messages = [
            MessageSummary(
                message_id=m.message_id or "",
                conversation_id=m.conversation_id,
                sender=m.from_ or "",
                recipient=m.recipient or "",
                to=list(m.to or []),
                cc=list(m.cc or []),
                subject=m.subject or "",
                status=m.status or "",
                created_at=m.created_at or "",
            )
            for m in (resp.messages or [])
        ]
        return MessageList(messages=messages, next_token=resp.next_token)

    # ── Reply / Send ──────────────────────────────────────────────────

    def reply(
        self,
        message_id: str,
        body: str,
        html_body: Optional[str] = None,
        reply_all: Optional[bool] = None,
        cc: Optional[list[str]] = None,
        bcc: Optional[list[str]] = None,
        conversation_id: Optional[str] = None,
        attachments: Optional[list[Attachment]] = None,
        agent_email: Optional[str] = None,
    ) -> SendResult:
        """Reply to an inbound email."""
        email = self._require_agent_email(agent_email)
        req = ReplyToMessageRequest(
            body=body,
            html_body=html_body,
            reply_all=reply_all,
            cc=cc,
            bcc=bcc,
            conversation_id=conversation_id,
            attachments=_serialize_attachments(attachments),
        )
        resp = self.api.reply_to_message(email, message_id, req)
        return SendResult(
            status=resp.status or "",
            message_id=resp.message_id or "",
            method=resp.method or "",
        )

    def send(
        self,
        to: list[str],
        subject: str,
        body: str,
        html_body: Optional[str] = None,
        cc: Optional[list[str]] = None,
        bcc: Optional[list[str]] = None,
        conversation_id: Optional[str] = None,
        attachments: Optional[list[Attachment]] = None,
        agent_email: Optional[str] = None,
    ) -> SendResult:
        """Send a new email."""
        email = self._require_agent_email(agent_email)
        req = SendEmailRequest(
            to=to,
            subject=subject,
            body=body,
            html_body=html_body,
            from_=email,
            cc=cc,
            bcc=bcc,
            conversation_id=conversation_id,
            attachments=_serialize_attachments(attachments),
        )
        resp = self.api.send_email(req)
        return SendResult(
            status=resp.status or "",
            message_id=resp.message_id or "",
            method=resp.method or "",
        )

    # ── Agent CRUD ────────────────────────────────────────────────────

    def list_agents(self):
        return self.api.list_agents()

    def register_agent(
        self,
        slug: Optional[str] = None,
        *,
        email: Optional[str] = None,
        name: Optional[str] = None,
        webhook_url: Optional[str] = None,
        agent_mode: Optional[str] = None,
    ):
        """Register a new agent.

        For shared-domain agents, pass ``slug`` (just the local part, e.g. ``"my-bot"``).
        The server appends its configured shared domain automatically — do not
        pass a full email. Slug registration only works on deployments where
        the operator has enabled it; otherwise the request is rejected with 400.

        For custom-domain agents, pass ``email`` with the full address
        (e.g. ``"support@mycompany.com"``). The domain must be registered
        and DNS-verified first.
        """
        return self.api.register_agent(
            RegisterAgentRequest(
                slug=slug, email=email, name=name, webhook_url=webhook_url, agent_mode=agent_mode,
            )
        )

    def get_agent(self, email: str):
        return self.api.get_agent(email)

    def delete_agent(self, email: str):
        return self.api.delete_agent(email)

    def update_agent(
        self,
        email: str,
        *,
        webhook_url: Optional[str] = None,
        agent_mode: Optional[str] = None,
        hitl_enabled: Optional[bool] = None,
        hitl_ttl_seconds: Optional[int] = None,
        hitl_expiration_action: Optional[str] = None,
    ):
        """Update an agent's configuration.

        Only fields you pass are applied; missing fields keep their
        current server-side value. Useful for toggling HITL on/off or
        adjusting the approval window without re-specifying the rest.
        """
        body = UpdateAgentRequest(
            webhook_url=webhook_url,
            agent_mode=agent_mode,
            hitl_enabled=hitl_enabled,
            hitl_ttl_seconds=hitl_ttl_seconds,
            hitl_expiration_action=hitl_expiration_action,
        )
        return self.api.update_agent(email, body)

    # ── HITL (human-in-the-loop approval) ─────────────────────────────

    def list_pending_messages(self):
        """List pending-approval messages across every owned agent,
        sorted by soonest-expiring first."""
        return self.api.list_pending_messages()

    def get_pending_message(self, message_id: str):
        """Fetch the full detail of a held outbound message."""
        return self.api.get_pending_message(message_id)

    def approve_message(
        self,
        message_id: str,
        *,
        subject: Optional[str] = None,
        body_text: Optional[str] = None,
        body_html: Optional[str] = None,
        to: Optional[list[str]] = None,
        cc: Optional[list[str]] = None,
        bcc: Optional[list[str]] = None,
    ):
        """Approve a held outbound message.

        Pass any subset of overrides to approve with edits; pass none
        to approve as-is.
        """
        any_override = any(
            v is not None for v in (subject, body_text, body_html, to, cc, bcc)
        )
        overrides = (
            ApprovePendingMessageRequest(
                subject=subject,
                body_text=body_text,
                body_html=body_html,
                to=to,
                cc=cc,
                bcc=bcc,
            )
            if any_override
            else None
        )
        return self.api.approve_message(message_id, overrides)

    def reject_message(self, message_id: str, reason: str = ""):
        """Reject a held outbound message. The optional reason is
        stored for audit."""
        return self.api.reject_message(message_id, reason)

    # ── Domain CRUD ───────────────────────────────────────────────────

    def list_domains(self):
        return self.api.list_domains()

    def register_domain(self, domain: str):
        return self.api.register_domain(RegisterDomainRequest(domain=domain))

    def verify_domain(self, domain: str):
        return self.api.verify_domain(domain)

    def delete_domain(self, domain: str):
        return self.api.delete_domain(domain)

    # ── Lifecycle ─────────────────────────────────────────────────────

    def close(self) -> None:
        """Close the underlying HTTP client."""
        self.api.close()

    def __enter__(self) -> E2AClient:
        return self

    def __exit__(self, *args: object) -> None:
        self.close()

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
    ConversationDetail,
    CreateWebhookRequest,
    ForwardMessageRequest,
    ListConversationsResponse,
    MessageDetail,
    RegisterAgentRequest,
    RegisterDomainRequest,
    ReplyToMessageRequest,
    SendEmailRequest,
    TestWebhookRequest,
    UpdateAgentRequest,
    UpdateMessageRequest,
    UpdateMessageResponse,
    UpdateWebhookRequest,
    WebhookFilters,
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
        sort: Optional[str] = None,
        from_: Optional[str] = None,
        subject_contains: Optional[str] = None,
        conversation_id: Optional[str] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
        labels: Optional[list[str]] = None,
    ) -> MessageList:
        """Fetch message summaries with ergonomic field names.

        ``sort`` defaults server-side to ``"desc"`` (newest first). Pass
        ``"asc"`` to drain the inbox in arrival order — FIFO polling.

        ``from_`` / ``subject_contains`` are case-insensitive substring
        filters (capped at 200 chars server-side). ``conversation_id``
        exact-matches a thread. ``since`` / ``until`` are RFC3339
        timestamps bounding ``created_at``.

        ``labels``: AND-match filter. A row is returned only if EVERY
        label in the list is present. Each entry must match
        ``[a-z0-9:_-]+`` (≤64 chars). Reading by ``e2a:*`` system
        labels is allowed; setting them is server-only.
        """
        email = self._require_agent_email(agent_email)
        resp = self.api.list_messages(
            email,
            status=status,
            page_size=page_size,
            token=token,
            sort=sort,
            from_=from_,
            subject_contains=subject_contains,
            conversation_id=conversation_id,
            since=since,
            until=until,
            labels=labels,
        )
        messages = [
            MessageSummary(
                message_id=m.message_id or "",
                conversation_id=m.conversation_id,
                sender=m.from_ or "",
                recipient=m.recipient or "",
                to=list(m.to or []),
                cc=list(m.cc or []),
                reply_to=list(m.reply_to or []),
                subject=m.subject or "",
                status=m.status or "",
                created_at=m.created_at or "",
                labels=list(m.labels or []),
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
        idempotency_key: Optional[str] = None,
    ) -> SendResult:
        """Reply to an inbound email.

        ``idempotency_key`` is sent as the ``Idempotency-Key`` header.
        Supply a stable key derived from the triggering event (e.g. the
        inbound message id) to make this reply safe to retry; omit to
        let the SDK generate a fresh UUIDv4 per call (network-layer
        retry safety only).
        """
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
        resp = self.api.reply_to_message(email, message_id, req, idempotency_key=idempotency_key)
        return SendResult(
            status=resp.status or "",
            message_id=resp.message_id or "",
            method=resp.method or "",
        )

    def forward(
        self,
        message_id: str,
        to: list[str],
        body: Optional[str] = None,
        html_body: Optional[str] = None,
        cc: Optional[list[str]] = None,
        bcc: Optional[list[str]] = None,
        conversation_id: Optional[str] = None,
        attachments: Optional[list[Attachment]] = None,
        agent_email: Optional[str] = None,
        idempotency_key: Optional[str] = None,
    ) -> SendResult:
        """Forward an inbound email to new recipients.

        The server prepends ``body`` / ``html_body`` (your optional
        comment), then a Gmail-style "Forwarded message" block with
        the original headers and body. A forward is a new thread —
        no ``In-Reply-To`` / ``References`` headers are emitted. Pass
        ``conversation_id`` to bind to an existing thread.

        ``idempotency_key`` is sent as the ``Idempotency-Key`` header.
        Supply a stable key derived from the triggering event (e.g.
        the inbound ``message_id`` plus targets) to make this forward
        safe to retry.
        """
        email = self._require_agent_email(agent_email)
        req = ForwardMessageRequest(
            to=to,
            cc=cc,
            bcc=bcc,
            body=body,
            html_body=html_body,
            conversation_id=conversation_id,
            attachments=_serialize_attachments(attachments),
        )
        resp = self.api.forward_message(email, message_id, req, idempotency_key=idempotency_key)
        return SendResult(
            status=resp.status or "",
            message_id=resp.message_id or "",
            method=resp.method or "",
        )

    def update_message_labels(
        self,
        message_id: str,
        add_labels: Optional[list[str]] = None,
        remove_labels: Optional[list[str]] = None,
        agent_email: Optional[str] = None,
    ) -> list[str]:
        """Apply a labels delta to a message; return the post-update set.

        ``add_labels`` / ``remove_labels`` are each capped at 50 entries
        per call. Labels are lowercased server-side and must match
        ``[a-z0-9:_-]+`` up to 64 chars. The ``e2a:`` prefix is
        reserved for server-applied system labels — caller writes that
        try to set them return 400. A label that appears in both
        lists is removed (remove wins).

        Empty / None for both arguments is a no-op that simply echoes
        the current label set — useful as a cheap "fetch labels only"
        call without pulling the full message body.
        """
        email = self._require_agent_email(agent_email)
        req = UpdateMessageRequest(add_labels=add_labels, remove_labels=remove_labels)
        resp = self.api.update_message_labels(email, message_id, req)
        return list(resp.labels or [])

    # ── Conversations ───────────────────────────────────────────────

    def list_conversations(
        self,
        page_size: Optional[int] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
        agent_email: Optional[str] = None,
    ) -> ListConversationsResponse:
        """List conversations for the configured agent.

        One row per non-empty ``conversation_id``, sorted by most
        recent activity. The server caps the response at 100 entries
        — pagination is intentionally deferred for slice 1. Pass
        ``since`` / ``until`` (RFC3339) to bracket ``last_message_at``.
        """
        email = self._require_agent_email(agent_email)
        return self.api.list_conversations(
            email, page_size=page_size, since=since, until=until,
        )

    def get_conversation(
        self,
        conversation_id: str,
        agent_email: Optional[str] = None,
    ) -> ConversationDetail:
        """Fetch a single conversation with member messages, computed
        participants union, and computed labels union."""
        email = self._require_agent_email(agent_email)
        return self.api.get_conversation(email, conversation_id)

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
        idempotency_key: Optional[str] = None,
    ) -> SendResult:
        """Send a new email.

        ``idempotency_key`` is sent as the ``Idempotency-Key`` header.
        Supply a stable key derived from the triggering event (e.g. a
        job id) to make this send safe to retry; omit to let the SDK
        generate a fresh UUIDv4 per call (network-layer retry safety
        only — does not help across an explicit retry loop).
        """
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
        resp = self.api.send_email(req, idempotency_key=idempotency_key)
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
        agent_email: str,
        message_id: str,
        *,
        subject: Optional[str] = None,
        body_text: Optional[str] = None,
        body_html: Optional[str] = None,
        to: Optional[list[str]] = None,
        cc: Optional[list[str]] = None,
        bcc: Optional[list[str]] = None,
        idempotency_key: Optional[str] = None,
    ):
        """Approve a held outbound message.

        ``agent_email`` is the message's owning agent — taken from the
        pending-message listing (``agent_id``) or the webhook payload.
        Returns 404 if it doesn't match the message's owner.

        Pass any subset of overrides to approve with edits; pass none
        to approve as-is.

        ``idempotency_key`` makes retries safe across the SES double-
        send window. Supply a stable key derived from the review event
        (e.g. the dashboard click id or the pending ``message_id``) to
        make retries dedupe. When omitted the SDK mints a fresh UUIDv4
        per call — that gives network-layer retry safety only; the
        per-call default does not survive an explicit retry loop.
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
        return self.api.approve_message(agent_email, message_id, overrides, idempotency_key=idempotency_key)

    def reject_message(self, agent_email: str, message_id: str, reason: str = ""):
        """Reject a held outbound message. The optional reason is
        stored for audit. ``agent_email`` requirements match
        :meth:`approve_message`."""
        return self.api.reject_message(agent_email, message_id, reason)

    # ── Domain CRUD ───────────────────────────────────────────────────

    def list_domains(self):
        return self.api.list_domains()

    def register_domain(self, domain: str):
        return self.api.register_domain(RegisterDomainRequest(domain=domain))

    def verify_domain(self, domain: str):
        return self.api.verify_domain(domain)

    def delete_domain(self, domain: str):
        return self.api.delete_domain(domain)

    # ── Webhooks (top-level resource) ─────────────────────────────────

    def list_webhooks(self):
        return self.api.list_webhooks()

    def create_webhook(
        self,
        url: str,
        events: list[str],
        *,
        description: str = "",
        filters: Optional[WebhookFilters] = None,
    ):
        return self.api.create_webhook(
            CreateWebhookRequest(
                url=url,
                events=events,
                description=description,
                filters=filters,
            )
        )

    def get_webhook(self, webhook_id: str):
        return self.api.get_webhook(webhook_id)

    def update_webhook(
        self,
        webhook_id: str,
        *,
        url: Optional[str] = None,
        events: Optional[list[str]] = None,
        filters: Optional[WebhookFilters] = None,
        description: Optional[str] = None,
        enabled: Optional[bool] = None,
    ):
        return self.api.update_webhook(
            webhook_id,
            UpdateWebhookRequest(
                url=url,
                events=events,
                filters=filters,
                description=description,
                enabled=enabled,
            ),
        )

    def delete_webhook(self, webhook_id: str):
        return self.api.delete_webhook(webhook_id)

    def rotate_webhook_secret(self, webhook_id: str):
        return self.api.rotate_webhook_secret(webhook_id)

    def test_webhook(
        self,
        webhook_id: str,
        *,
        event: str = "",
        data: Optional[dict] = None,
    ):
        return self.api.test_webhook(
            webhook_id, TestWebhookRequest(event=event, data=data)
        )

    def list_webhook_deliveries(
        self,
        webhook_id: str,
        *,
        limit: Optional[int] = None,
        status: Optional[str] = None,
    ):
        return self.api.list_webhook_deliveries(
            webhook_id, limit=limit, status=status
        )

    # ── Lifecycle ─────────────────────────────────────────────────────

    def close(self) -> None:
        """Close the underlying HTTP client."""
        self.api.close()

    def __enter__(self) -> E2AClient:
        return self

    def __exit__(self, *args: object) -> None:
        self.close()

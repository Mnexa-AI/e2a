"""Async high-level client for the e2a v1 API.

Drop-in async replacement for :class:`E2AClient`. Contains both
:class:`AsyncE2AApi` (raw typed async HTTP) and :class:`AsyncE2AClient`
(high-level with ``AsyncInboundEmail``).
"""

from __future__ import annotations

import base64
import json
import os
import warnings
from typing import TYPE_CHECKING, Any, AsyncIterator, Optional
from urllib.parse import quote

import httpx

if TYPE_CHECKING:
    from e2a.v1.websocket import WSNotification

from e2a.v1.api import E2AApiError, _check_response, _idempotency_header
from e2a.v1.generated import (
    Agent,
    ApprovePendingMessageRequest,
    ApprovePendingMessageResponse,
    ConversationDetail,
    CreateWebhookRequest,
    DeploymentInfo,
    Domain,
    ForwardMessageRequest,
    ListAgentsResponse,
    ListConversationsResponse,
    ListDomainsResponse,
    ListMessagesResponse,
    ListPendingMessagesResponse,
    ListWebhookDeliveriesResponse,
    ListWebhooksResponse,
    MessageDetail,
    PendingMessageDetail,
    RegisterAgentRequest,
    RegisterAgentResponse,
    RegisterDomainRequest,
    RejectPendingMessageRequest,
    RejectPendingMessageResponse,
    ReplyToMessageRequest,
    RotateWebhookSecretResponse,
    SendEmailRequest,
    SendEmailResponse,
    TestWebhookRequest,
    TestWebhookResponse,
    UpdateAgentRequest,
    UpdateMessageRequest,
    UpdateMessageResponse,
    UpdateWebhookRequest,
    VerifyDomainResponse,
    WebhookResponse,
)
from e2a.v1.generated import internal_agent
from e2a.v1.handler import (
    Attachment,
    AsyncInboundEmail,
    MessageList,
    MessageSummary,
    SendResult,
    build_inbound_email_async,
)


def _encode_email(email: str) -> str:
    return quote(email, safe="")


def _serialize_attachments(
    attachments: list[Attachment] | None,
) -> list[internal_agent.Attachment] | None:
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


# ── Raw async API ────────────────────────────────────────────────


class AsyncE2AApi:
    """Raw typed async client for the e2a v1 REST API.

    Async mirror of :class:`e2a.v1.api.E2AApi`.
    """

    def __init__(
        self,
        api_key: Optional[str] = None,
        base_url: str = "https://e2a.dev",
        timeout: float = 30,
    ) -> None:
        self.api_key = api_key or os.environ.get("E2A_API_KEY", "")
        if not self.api_key:
            raise ValueError(
                "api_key is required. Pass it to AsyncE2AApi() or set E2A_API_KEY in the environment."
            )
        self.base_url = base_url.rstrip("/")
        self._client = httpx.AsyncClient(
            base_url=self.base_url,
            headers={"Authorization": f"Bearer {self.api_key}"},
            timeout=timeout,
        )

    # ── Agents ────────────────────────────────────────────────────

    async def list_agents(self) -> ListAgentsResponse:
        resp = await self._client.get("/api/v1/agents")
        _check_response(resp)
        return ListAgentsResponse.model_validate(resp.json())

    async def register_agent(self, body: RegisterAgentRequest) -> RegisterAgentResponse:
        resp = await self._client.post(
            "/api/v1/agents",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return RegisterAgentResponse.model_validate(resp.json())

    async def get_agent(self, email: str) -> Agent:
        resp = await self._client.get(f"/api/v1/agents/{_encode_email(email)}")
        _check_response(resp)
        return Agent.model_validate(resp.json())

    async def delete_agent(self, email: str) -> None:
        resp = await self._client.delete(f"/api/v1/agents/{_encode_email(email)}")
        _check_response(resp)

    async def update_agent(self, email: str, body: UpdateAgentRequest) -> Agent:
        resp = await self._client.put(
            f"/api/v1/agents/{_encode_email(email)}",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return Agent.model_validate(resp.json())

    # ── Domains ───────────────────────────────────────────────────

    async def list_domains(self) -> ListDomainsResponse:
        resp = await self._client.get("/api/v1/domains")
        _check_response(resp)
        return ListDomainsResponse.model_validate(resp.json())

    async def register_domain(self, body: RegisterDomainRequest) -> Domain:
        resp = await self._client.post(
            "/api/v1/domains",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return Domain.model_validate(resp.json())

    async def verify_domain(self, domain: str) -> VerifyDomainResponse:
        resp = await self._client.post(f"/api/v1/domains/{quote(domain, safe='')}/verify")
        _check_response(resp)
        return VerifyDomainResponse.model_validate(resp.json())

    async def delete_domain(self, domain: str) -> None:
        resp = await self._client.delete(f"/api/v1/domains/{quote(domain, safe='')}")
        _check_response(resp)

    # ── Webhooks (top-level resource) ────────────────────────────

    async def list_webhooks(self) -> ListWebhooksResponse:
        resp = await self._client.get("/api/v1/webhooks")
        _check_response(resp)
        return ListWebhooksResponse.model_validate(resp.json())

    async def create_webhook(
        self, body: CreateWebhookRequest
    ) -> WebhookResponse:
        resp = await self._client.post(
            "/api/v1/webhooks",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return WebhookResponse.model_validate(resp.json())

    async def get_webhook(self, webhook_id: str) -> WebhookResponse:
        resp = await self._client.get(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}"
        )
        _check_response(resp)
        return WebhookResponse.model_validate(resp.json())

    async def update_webhook(
        self, webhook_id: str, body: UpdateWebhookRequest
    ) -> WebhookResponse:
        resp = await self._client.patch(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return WebhookResponse.model_validate(resp.json())

    async def delete_webhook(self, webhook_id: str) -> None:
        resp = await self._client.delete(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}"
        )
        _check_response(resp)

    async def rotate_webhook_secret(
        self, webhook_id: str
    ) -> RotateWebhookSecretResponse:
        resp = await self._client.post(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}/rotate-secret"
        )
        _check_response(resp)
        return RotateWebhookSecretResponse.model_validate(resp.json())

    async def test_webhook(
        self,
        webhook_id: str,
        body: Optional[TestWebhookRequest] = None,
    ) -> TestWebhookResponse:
        payload = (
            body.model_dump(by_alias=True, exclude_none=True)
            if body is not None
            else {}
        )
        resp = await self._client.post(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}/test",
            json=payload,
        )
        _check_response(resp)
        return TestWebhookResponse.model_validate(resp.json())

    async def list_webhook_deliveries(
        self,
        webhook_id: str,
        limit: Optional[int] = None,
        status: Optional[str] = None,
    ) -> ListWebhookDeliveriesResponse:
        params = {}
        if limit is not None:
            params["limit"] = limit
        if status is not None:
            params["status"] = status
        resp = await self._client.get(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}/deliveries",
            params=params,
        )
        _check_response(resp)
        return ListWebhookDeliveriesResponse.model_validate(resp.json())

    # ── Messages ──────────────────────────────────────────────────

    async def list_messages(
        self,
        agent_email: str,
        status: str = "unread",
        page_size: int = 50,
        token: Optional[str] = None,
        sort: Optional[str] = None,
        from_: Optional[str] = None,
        subject_contains: Optional[str] = None,
        conversation_id: Optional[str] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
        labels: Optional[list[str]] = None,
    ) -> ListMessagesResponse:
        """Async variant of :meth:`E2AApi.list_messages`. See that
        method for the full filter / sort docs."""
        # Build query string by hand so repeated `labels=` params work
        # — see the sync sibling for the rationale.
        params: list[tuple[str, str]] = [
            ("status", status),
            ("page_size", str(page_size)),
        ]
        if sort:
            params.append(("sort", sort))
        if from_:
            params.append(("from", from_))
        if subject_contains:
            params.append(("subject_contains", subject_contains))
        if conversation_id:
            params.append(("conversation_id", conversation_id))
        if since:
            params.append(("since", since))
        if until:
            params.append(("until", until))
        if labels:
            for label in labels:
                params.append(("labels", label))
        if token:
            params.append(("token", token))
        resp = await self._client.get(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages",
            params=params,
        )
        _check_response(resp)
        return ListMessagesResponse.model_validate(resp.json())

    async def get_message(self, agent_email: str, message_id: str) -> MessageDetail:
        resp = await self._client.get(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages/{message_id}",
        )
        _check_response(resp)
        return MessageDetail.model_validate(resp.json())

    async def reply_to_message(
        self,
        agent_email: str,
        message_id: str,
        body: ReplyToMessageRequest,
        idempotency_key: Optional[str] = None,
    ) -> SendEmailResponse:
        resp = await self._client.post(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages/{message_id}/reply",
            json=body.model_dump(by_alias=True, exclude_none=True),
            headers=_idempotency_header(idempotency_key),
        )
        _check_response(resp)
        return SendEmailResponse.model_validate(resp.json())

    async def forward_message(
        self,
        agent_email: str,
        message_id: str,
        body: ForwardMessageRequest,
        idempotency_key: Optional[str] = None,
    ) -> SendEmailResponse:
        """Async variant of :meth:`E2AApi.forward_message`."""
        resp = await self._client.post(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages/{message_id}/forward",
            json=body.model_dump(by_alias=True, exclude_none=True),
            headers=_idempotency_header(idempotency_key),
        )
        _check_response(resp)
        return SendEmailResponse.model_validate(resp.json())

    async def update_message_labels(
        self,
        agent_email: str,
        message_id: str,
        body: UpdateMessageRequest,
    ) -> UpdateMessageResponse:
        """Async variant of :meth:`E2AApi.update_message_labels`."""
        resp = await self._client.patch(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages/{message_id}",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return UpdateMessageResponse.model_validate(resp.json())

    async def send_email(
        self,
        body: SendEmailRequest,
        idempotency_key: Optional[str] = None,
    ) -> SendEmailResponse:
        resp = await self._client.post(
            "/api/v1/send",
            json=body.model_dump(by_alias=True, exclude_none=True),
            headers=_idempotency_header(idempotency_key),
        )
        _check_response(resp)
        return SendEmailResponse.model_validate(resp.json())

    # ── Conversations ─────────────────────────────────────────────

    async def list_conversations(
        self,
        agent_email: str,
        page_size: Optional[int] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
    ) -> ListConversationsResponse:
        """Async variant of :meth:`E2AApi.list_conversations`."""
        params: list[tuple[str, str]] = []
        if page_size is not None:
            params.append(("page_size", str(page_size)))
        if since:
            params.append(("since", since))
        if until:
            params.append(("until", until))
        resp = await self._client.get(
            f"/api/v1/agents/{_encode_email(agent_email)}/conversations",
            params=params,
        )
        _check_response(resp)
        return ListConversationsResponse.model_validate(resp.json())

    async def get_conversation(
        self,
        agent_email: str,
        conversation_id: str,
    ) -> ConversationDetail:
        """Async variant of :meth:`E2AApi.get_conversation`."""
        resp = await self._client.get(
            f"/api/v1/agents/{_encode_email(agent_email)}/conversations/{_encode_email(conversation_id)}",
        )
        _check_response(resp)
        return ConversationDetail.model_validate(resp.json())

    # ── HITL (human-in-the-loop approval) ─────────────────────────

    async def list_pending_messages(self) -> ListPendingMessagesResponse:
        resp = await self._client.get(
            "/api/v1/pending",
        )
        _check_response(resp)
        return ListPendingMessagesResponse.model_validate(resp.json())

    async def get_pending_message(self, message_id: str) -> PendingMessageDetail:
        resp = await self._client.get(
            f"/api/v1/messages/{quote(message_id, safe='')}",
        )
        _check_response(resp)
        return PendingMessageDetail.model_validate(resp.json())

    async def approve_message(
        self,
        agent_email: str,
        message_id: str,
        overrides: Optional[ApprovePendingMessageRequest] = None,
        idempotency_key: Optional[str] = None,
    ) -> ApprovePendingMessageResponse:
        """Async variant of :meth:`E2AApi.approve_message`. ``idempotency_key``
        closes the SES double-send window — see that method for details."""
        payload = overrides.model_dump(by_alias=True, exclude_none=True) if overrides else {}
        resp = await self._client.post(
            f"/api/v1/agents/{quote(agent_email, safe='')}/messages/{quote(message_id, safe='')}/approve",
            json=payload,
            headers=_idempotency_header(idempotency_key),
        )
        _check_response(resp)
        return ApprovePendingMessageResponse.model_validate(resp.json())

    async def reject_message(
        self,
        agent_email: str,
        message_id: str,
        reason: str = "",
    ) -> RejectPendingMessageResponse:
        body = RejectPendingMessageRequest(reason=reason)
        resp = await self._client.post(
            f"/api/v1/agents/{quote(agent_email, safe='')}/messages/{quote(message_id, safe='')}/reject",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return RejectPendingMessageResponse.model_validate(resp.json())

    # ── Discovery ─────────────────────────────────────────────────

    async def get_info(self) -> DeploymentInfo:
        """Fetch deployment-specific configuration (shared domain, public URL).

        Async mirror of :meth:`e2a.v1.api.E2AApi.get_info`. Unauthenticated.
        """
        resp = await self._client.get("/api/v1/info")
        _check_response(resp)
        return DeploymentInfo.model_validate(resp.json())

    # ── Lifecycle ─────────────────────────────────────────────────

    async def close(self) -> None:
        await self._client.aclose()

    async def __aenter__(self) -> AsyncE2AApi:
        return self

    async def __aexit__(self, *args: object) -> None:
        await self.close()


# ── High-level async client ──────────────────────────────────────


class AsyncE2AClient:
    """High-level async client for the e2a v1 API.

    Drop-in async replacement for :class:`e2a.v1.client.E2AClient`.
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
        self.api = AsyncE2AApi(api_key=resolved_key, base_url=base_url, timeout=timeout)

    def _require_agent_email(self, agent_email: Optional[str] = None) -> str:
        email = agent_email or self.agent_email
        if not email:
            raise ValueError(
                "agent_email is required. Pass it to AsyncE2AClient(), set E2A_AGENT_EMAIL, "
                "or use AsyncInboundEmail.reply() which auto-resolves it from the payload."
            )
        return email

    # ── Parsing ───────────────────────────────────────────────────

    def parse(
        self,
        body: bytes | str | dict[str, Any] | MessageDetail,
        headers: dict[str, str] | None = None,
    ) -> AsyncInboundEmail:
        """Parse a webhook payload into an AsyncInboundEmail.

        .. deprecated:: 2.2
           Use :meth:`parse_webhook` for webhook handlers (parse + verify
           in one call) or :attr:`AsyncInboundEmail.unverified_payload`
           for inspection without verification. ``parse`` will be removed
           in 3.0.

        Synchronous (no I/O). The returned email's ``.reply()`` is async.

        Returns an *unverified* AsyncInboundEmail — claim fields raise
        :class:`UnverifiedEmailError` until you call
        :meth:`AsyncInboundEmail.verify_signature`.
        """
        warnings.warn(
            "AsyncE2AClient.parse() is deprecated and will be removed in 3.0. "
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
    ) -> AsyncInboundEmail:
        """Internal parse without the deprecation warning."""
        if isinstance(body, MessageDetail):
            data = body.model_dump(by_alias=True)
        elif isinstance(body, dict):
            data = body
        elif isinstance(body, (bytes, str)):
            data = json.loads(body)
        else:
            raise TypeError(f"Unsupported body type: {type(body)}")

        return build_inbound_email_async(data, self)

    def parse_webhook(
        self,
        body: bytes | str | dict[str, Any] | MessageDetail,
        secret: Optional[str] = None,
    ) -> AsyncInboundEmail:
        """Parse + HMAC-verify a webhook payload in one call (async client).

        See :meth:`E2AClient.parse_webhook` — identical contract.
        Synchronous despite living on the async client (no I/O).
        """
        email = self._parse_unverified(body)
        if not email.verify_signature(secret):
            raise PermissionError("HMAC signature verification failed")
        return email

    # ── Messages ──────────────────────────────────────────────────

    async def get_message(
        self,
        message_id: str,
        agent_email: Optional[str] = None,
    ) -> AsyncInboundEmail:
        """Fetch a single message and return a parsed AsyncInboundEmail.

        Returned email is pre-verified — see :meth:`E2AClient.get_message`.
        """
        email = self._require_agent_email(agent_email)
        detail = await self.api.get_message(email, message_id)
        data = detail.model_dump(by_alias=True)
        return build_inbound_email_async(data, self, trusted=True)

    async def get_messages(
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

        Search filters (``from_``, ``subject_contains``, ``conversation_id``,
        ``since``, ``until``, ``labels``) match the sync client — see
        :meth:`E2AApi.list_messages` for the full reference.
        """
        email = self._require_agent_email(agent_email)
        resp = await self.api.list_messages(
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

    # ── Reply / Send ──────────────────────────────────────────────

    async def reply(
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
        let the SDK generate a fresh UUIDv4 per call.
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
        resp = await self.api.reply_to_message(email, message_id, req, idempotency_key=idempotency_key)
        return SendResult(
            status=resp.status or "",
            message_id=resp.message_id or "",
            method=resp.method or "",
        )

    async def forward(
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
        """Async variant of :meth:`E2AClient.forward`."""
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
        resp = await self.api.forward_message(email, message_id, req, idempotency_key=idempotency_key)
        return SendResult(
            status=resp.status or "",
            message_id=resp.message_id or "",
            method=resp.method or "",
        )

    async def update_message_labels(
        self,
        message_id: str,
        add_labels: Optional[list[str]] = None,
        remove_labels: Optional[list[str]] = None,
        agent_email: Optional[str] = None,
    ) -> list[str]:
        """Async variant of :meth:`E2AClient.update_message_labels`.

        Returns the post-update label set so the caller can echo state
        without a separate fetch. ``add_labels`` / ``remove_labels`` are
        each capped at 50 entries per call; the per-message cap is 100.
        Labels matching ``e2a:*`` are server-applied only and rejected
        on user writes. Empty / None for both arguments is a no-op
        that returns the current labels.
        """
        email = self._require_agent_email(agent_email)
        req = UpdateMessageRequest(add_labels=add_labels, remove_labels=remove_labels)
        resp = await self.api.update_message_labels(email, message_id, req)
        return list(resp.labels or [])

    # ── Conversations ───────────────────────────────────────────────

    async def list_conversations(
        self,
        page_size: Optional[int] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
        agent_email: Optional[str] = None,
    ) -> ListConversationsResponse:
        """Async variant of :meth:`E2AClient.list_conversations`."""
        email = self._require_agent_email(agent_email)
        return await self.api.list_conversations(
            email, page_size=page_size, since=since, until=until,
        )

    async def get_conversation(
        self,
        conversation_id: str,
        agent_email: Optional[str] = None,
    ) -> ConversationDetail:
        """Async variant of :meth:`E2AClient.get_conversation`."""
        email = self._require_agent_email(agent_email)
        return await self.api.get_conversation(email, conversation_id)

    async def send(
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
        generate a fresh UUIDv4 per call.
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
        resp = await self.api.send_email(req, idempotency_key=idempotency_key)
        return SendResult(
            status=resp.status or "",
            message_id=resp.message_id or "",
            method=resp.method or "",
        )

    # ── Agent CRUD ────────────────────────────────────────────────

    async def list_agents(self):
        return await self.api.list_agents()

    async def register_agent(
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
        return await self.api.register_agent(
            RegisterAgentRequest(
                slug=slug, email=email, name=name, webhook_url=webhook_url, agent_mode=agent_mode,
            )
        )

    async def get_agent(self, email: str):
        return await self.api.get_agent(email)

    async def delete_agent(self, email: str):
        return await self.api.delete_agent(email)

    async def update_agent(
        self,
        email: str,
        *,
        webhook_url: Optional[str] = None,
        agent_mode: Optional[str] = None,
        hitl_enabled: Optional[bool] = None,
        hitl_ttl_seconds: Optional[int] = None,
        hitl_expiration_action: Optional[str] = None,
    ):
        """Update an agent's configuration. Only fields you pass are
        applied; missing fields keep their current server-side value."""
        body = UpdateAgentRequest(
            webhook_url=webhook_url,
            agent_mode=agent_mode,
            hitl_enabled=hitl_enabled,
            hitl_ttl_seconds=hitl_ttl_seconds,
            hitl_expiration_action=hitl_expiration_action,
        )
        return await self.api.update_agent(email, body)

    # ── HITL (human-in-the-loop approval) ─────────────────────────

    async def list_pending_messages(self):
        return await self.api.list_pending_messages()

    async def get_pending_message(self, message_id: str):
        return await self.api.get_pending_message(message_id)

    async def approve_message(
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
        return await self.api.approve_message(agent_email, message_id, overrides, idempotency_key=idempotency_key)

    async def reject_message(self, agent_email: str, message_id: str, reason: str = ""):
        return await self.api.reject_message(agent_email, message_id, reason)

    # ── Domain CRUD ───────────────────────────────────────────────

    async def list_domains(self):
        return await self.api.list_domains()

    async def register_domain(self, domain: str):
        return await self.api.register_domain(RegisterDomainRequest(domain=domain))

    async def verify_domain(self, domain: str):
        return await self.api.verify_domain(domain)

    async def delete_domain(self, domain: str):
        return await self.api.delete_domain(domain)

    # ── Webhooks (top-level resource) ──────────────────────────────

    async def list_webhooks(self):
        return await self.api.list_webhooks()

    async def create_webhook(
        self,
        url: str,
        events: list[str],
        *,
        description: str = "",
        filters: Optional[Any] = None,
    ):
        return await self.api.create_webhook(
            CreateWebhookRequest(
                url=url,
                events=events,
                description=description,
                filters=filters,
            )
        )

    async def get_webhook(self, webhook_id: str):
        return await self.api.get_webhook(webhook_id)

    async def update_webhook(
        self,
        webhook_id: str,
        *,
        url: Optional[str] = None,
        events: Optional[list[str]] = None,
        filters: Optional[Any] = None,
        description: Optional[str] = None,
        enabled: Optional[bool] = None,
    ):
        return await self.api.update_webhook(
            webhook_id,
            UpdateWebhookRequest(
                url=url,
                events=events,
                filters=filters,
                description=description,
                enabled=enabled,
            ),
        )

    async def delete_webhook(self, webhook_id: str):
        return await self.api.delete_webhook(webhook_id)

    async def rotate_webhook_secret(self, webhook_id: str):
        return await self.api.rotate_webhook_secret(webhook_id)

    async def test_webhook(
        self,
        webhook_id: str,
        *,
        event: str = "",
        data: Optional[dict] = None,
    ):
        return await self.api.test_webhook(
            webhook_id, TestWebhookRequest(event=event, data=data)
        )

    async def list_webhook_deliveries(
        self,
        webhook_id: str,
        *,
        limit: Optional[int] = None,
        status: Optional[str] = None,
    ):
        return await self.api.list_webhook_deliveries(
            webhook_id, limit=limit, status=status
        )

    # ── WebSocket ─────────────────────────────────────────────────

    def listen(
        self,
        agent_email: Optional[str] = None,
        reconnect: bool = True,
        max_backoff: float = 30.0,
    ) -> "AsyncIterator[WSNotification]":
        """Listen for inbound mail via WebSocket. Yields lightweight notifications.

        Each yielded :class:`WSNotification` carries metadata (message_id,
        sender, subject, etc.) — no body. Call ``await self.get_message(
        notif.message_id)`` to fetch the full email when you actually need
        it. This matches the server's design (small WS frames, explicit REST
        fetch) and lets callers skip messages without a network round-trip.

        Reconnects with exponential backoff (1s → ``max_backoff``) by default.
        Requires ``pip install e2a[ws]``.
        """
        from e2a.v1.websocket import listen as _ws_listen

        return _ws_listen(
            client=self,
            agent_email=agent_email,
            reconnect=reconnect,
            max_backoff=max_backoff,
        )

    # ── Lifecycle ─────────────────────────────────────────────────

    async def close(self) -> None:
        await self.api.close()

    async def __aenter__(self) -> AsyncE2AClient:
        return self

    async def __aexit__(self, *args: object) -> None:
        await self.close()


async def fetch_info(
    base_url: str = "https://e2a.dev",
    timeout: float = 30,
) -> DeploymentInfo:
    """Async version of :func:`e2a.v1.api.fetch_info`.

    Fetch deployment info without an API key. Useful before login.
    """
    base = base_url.rstrip("/")
    async with httpx.AsyncClient(timeout=timeout) as c:
        resp = await c.get(f"{base}/api/v1/info")
        _check_response(resp)
        return DeploymentInfo.model_validate(resp.json())

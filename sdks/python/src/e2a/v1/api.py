"""Raw typed HTTP client for the e2a v1 API.

Every method maps 1:1 to a REST endpoint under ``/api/v1/``.
Responses are parsed into generated Pydantic models — no convenience
wrappers, no MIME parsing, no magic.

For a higher-level client with ``InboundEmail`` and ``.reply()``,
see :class:`e2a.v1.client.E2AClient`.
"""

from __future__ import annotations

import os
import uuid
from typing import Optional
from urllib.parse import quote

import httpx

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
    ListEventsResponse,
    ListMessagesResponse,
    ListPendingMessagesResponse,
    RedeliverRequest,
    RedeliverResponse,
    RedeliverSinceRequest,
    RedeliverSinceResponse,
    WebhookEvent,
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


class E2AApiError(Exception):
    """Raised when the e2a API returns an HTTP error."""

    def __init__(self, status_code: int, message: str) -> None:
        self.status_code = status_code
        self.message = message
        super().__init__(f"e2a API error ({status_code}): {message}")


def _check_response(resp: httpx.Response) -> None:
    if resp.status_code >= 400:
        try:
            message = resp.text.strip()
        except Exception:
            message = f"HTTP {resp.status_code}"
        raise E2AApiError(resp.status_code, message)


def _idempotency_header(idempotency_key: Optional[str]) -> dict:
    """Build the ``Idempotency-Key`` header for a side-effectful send.

    A caller-supplied key is passed through verbatim. When ``None``, a
    fresh UUIDv4 is generated so callers get retry-safe transport
    behavior by default. To benefit across an explicit retry loop the
    caller must supply a stable key (the per-call default does not
    survive retries — each call would mint a new UUID).
    """
    key = idempotency_key if idempotency_key is not None else uuid.uuid4().hex
    return {"Idempotency-Key": key}


def _encode_email(email: str) -> str:
    """URL-encode an email for use in path segments."""
    return quote(email, safe="")


class E2AApi:
    """Raw typed client for the e2a v1 REST API.

    All methods use ``/api/v1/...`` paths and return generated Pydantic models.

    Args:
        api_key: Your API key.
            Falls back to the ``E2A_API_KEY`` environment variable.
        base_url: e2a API base URL. Defaults to ``https://e2a.dev``.
        timeout: Request timeout in seconds. Defaults to 30.
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
                "api_key is required. Pass it to E2AApi() or set E2A_API_KEY in the environment."
            )
        self.base_url = base_url.rstrip("/")
        self._client = httpx.Client(
            base_url=self.base_url,
            headers={"Authorization": f"Bearer {self.api_key}"},
            timeout=timeout,
        )

    # ── Agents ────────────────────────────────────────────────────────

    def list_agents(self) -> ListAgentsResponse:
        resp = self._client.get("/api/v1/agents")
        _check_response(resp)
        return ListAgentsResponse.model_validate(resp.json())

    def register_agent(self, body: RegisterAgentRequest) -> RegisterAgentResponse:
        resp = self._client.post(
            "/api/v1/agents",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return RegisterAgentResponse.model_validate(resp.json())

    def get_agent(self, email: str) -> Agent:
        resp = self._client.get(f"/api/v1/agents/{_encode_email(email)}")
        _check_response(resp)
        return Agent.model_validate(resp.json())

    def delete_agent(self, email: str) -> None:
        resp = self._client.delete(f"/api/v1/agents/{_encode_email(email)}")
        _check_response(resp)

    def update_agent(self, email: str, body: UpdateAgentRequest) -> Agent:
        """Update an agent's configuration (HITL, webhook, or mode).

        Only fields set on the ``UpdateAgentRequest`` are applied;
        missing fields preserve their current server-side value, so
        callers can PATCH a single setting (e.g. toggle HITL) without
        re-sending the rest.
        """
        resp = self._client.put(
            f"/api/v1/agents/{_encode_email(email)}",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return Agent.model_validate(resp.json())

    def send_test_email(self, email: str) -> dict:
        """Send a test email from the platform to the agent's own address.

        Useful for verifying inbound delivery is wired up correctly.
        Requires the agent's domain to be verified. If the agent has
        HITL enabled, the response is HTTP 202 and the message is held
        for approval.
        """
        resp = self._client.post(f"/api/v1/agents/{_encode_email(email)}/test")
        _check_response(resp)
        return resp.json()

    # ── Domains ───────────────────────────────────────────────────────

    def list_domains(self) -> ListDomainsResponse:
        resp = self._client.get("/api/v1/domains")
        _check_response(resp)
        return ListDomainsResponse.model_validate(resp.json())

    def register_domain(self, body: RegisterDomainRequest) -> Domain:
        resp = self._client.post(
            "/api/v1/domains",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return Domain.model_validate(resp.json())

    def verify_domain(self, domain: str) -> VerifyDomainResponse:
        resp = self._client.post(f"/api/v1/domains/{quote(domain, safe='')}/verify")
        _check_response(resp)
        return VerifyDomainResponse.model_validate(resp.json())

    def delete_domain(self, domain: str) -> None:
        resp = self._client.delete(f"/api/v1/domains/{quote(domain, safe='')}")
        _check_response(resp)

    # ── Webhooks (top-level resource) ────────────────────────────────

    def list_webhooks(self) -> ListWebhooksResponse:
        resp = self._client.get("/api/v1/webhooks")
        _check_response(resp)
        return ListWebhooksResponse.model_validate(resp.json())

    def create_webhook(self, body: CreateWebhookRequest) -> WebhookResponse:
        resp = self._client.post(
            "/api/v1/webhooks",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return WebhookResponse.model_validate(resp.json())

    def get_webhook(self, webhook_id: str) -> WebhookResponse:
        resp = self._client.get(f"/api/v1/webhooks/{quote(webhook_id, safe='')}")
        _check_response(resp)
        return WebhookResponse.model_validate(resp.json())

    def update_webhook(
        self, webhook_id: str, body: UpdateWebhookRequest
    ) -> WebhookResponse:
        resp = self._client.patch(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return WebhookResponse.model_validate(resp.json())

    def delete_webhook(self, webhook_id: str) -> None:
        resp = self._client.delete(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}"
        )
        _check_response(resp)

    def rotate_webhook_secret(
        self, webhook_id: str
    ) -> RotateWebhookSecretResponse:
        resp = self._client.post(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}/rotate-secret"
        )
        _check_response(resp)
        return RotateWebhookSecretResponse.model_validate(resp.json())

    def test_webhook(
        self, webhook_id: str, body: Optional[TestWebhookRequest] = None
    ) -> TestWebhookResponse:
        payload = (
            body.model_dump(by_alias=True, exclude_none=True)
            if body is not None
            else {}
        )
        resp = self._client.post(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}/test",
            json=payload,
        )
        _check_response(resp)
        return TestWebhookResponse.model_validate(resp.json())

    def list_webhook_deliveries(
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
        resp = self._client.get(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}/deliveries",
            params=params,
        )
        _check_response(resp)
        return ListWebhookDeliveriesResponse.model_validate(resp.json())

    # ── Messages ──────────────────────────────────────────────────────

    def list_messages(
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
        """List messages for an agent.

        ``sort`` defaults server-side to ``"desc"`` (newest first).
        Pass ``"asc"`` for FIFO polling — drain the inbox in arrival
        order. The choice is encoded in ``next_token`` so subsequent
        pages keep the same order; switching mid-pagination returns
        400.

        ``from_``, ``subject_contains``: case-insensitive substring
        match (Postgres ILIKE). Capped server-side at 200 chars.

        ``conversation_id``: exact match — narrow to one thread.

        ``since`` / ``until``: RFC3339 timestamps (``datetime.isoformat()``
        produces a valid value as long as it ends in ``Z`` or has a
        timezone offset). Bracket on ``created_at`` (``>= since`` and
        ``< until``).

        ``labels``: AND-match. A row is returned only if EVERY label
        in the list is present on the row. Each entry must match the
        same charset as a writable label (``[a-z0-9:_-]+``, ≤64 chars).
        Reading by ``e2a:*`` system labels is allowed even though
        setting them is server-only. Encoded as repeated
        ``?labels=`` query params; the filter is part of the cursor
        identity so continuation pages must reuse the same labels.
        """
        # Build query string by hand so repeated `labels=` params
        # work — httpx accepts a list value for a key and emits each
        # element as its own occurrence, which is the shape the
        # server-side parser expects (`r.URL.Query()["labels"]`).
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
        resp = self._client.get(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages",
            params=params,
        )
        _check_response(resp)
        return ListMessagesResponse.model_validate(resp.json())

    def get_message(self, agent_email: str, message_id: str) -> MessageDetail:
        resp = self._client.get(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages/{message_id}",
        )
        _check_response(resp)
        return MessageDetail.model_validate(resp.json())

    def reply_to_message(
        self,
        agent_email: str,
        message_id: str,
        body: ReplyToMessageRequest,
        idempotency_key: Optional[str] = None,
    ) -> SendEmailResponse:
        resp = self._client.post(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages/{message_id}/reply",
            json=body.model_dump(by_alias=True, exclude_none=True),
            headers=_idempotency_header(idempotency_key),
        )
        _check_response(resp)
        return SendEmailResponse.model_validate(resp.json())

    def forward_message(
        self,
        agent_email: str,
        message_id: str,
        body: ForwardMessageRequest,
        idempotency_key: Optional[str] = None,
    ) -> SendEmailResponse:
        """Forward an inbound message to new recipients.

        The server prepends the caller's optional comment (``body`` /
        ``html_body``), then a Gmail-style "Forwarded message" block
        with the original headers and a best-effort extraction of the
        original body. **A forward is a NEW thread** — no
        ``In-Reply-To`` / ``References`` headers are emitted. Pass
        ``conversation_id`` to bind the forward to an existing thread
        explicitly.

        ``idempotency_key`` is sent as the ``Idempotency-Key`` header;
        a natural choice is the inbound ``message_id`` plus target
        list. Without it the SDK does not auto-mint one — the server
        will deliver every retry as a fresh forward.
        """
        resp = self._client.post(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages/{message_id}/forward",
            json=body.model_dump(by_alias=True, exclude_none=True),
            headers=_idempotency_header(idempotency_key),
        )
        _check_response(resp)
        return SendEmailResponse.model_validate(resp.json())

    def update_message_labels(
        self,
        agent_email: str,
        message_id: str,
        body: UpdateMessageRequest,
    ) -> UpdateMessageResponse:
        """Apply a labels delta to a message.

        ``body`` carries ``add_labels`` and/or ``remove_labels``. Labels
        are lowercase strings drawn from ``[a-z0-9:_-]+``, capped at 64
        chars each and 50 per request; the post-update label set is
        capped at 100 per message. The ``e2a:`` prefix is reserved for
        server-applied system labels and rejected on user writes. A
        label appearing in both lists is removed (remove wins).
        Returns the post-update label set so callers can echo state
        without a follow-up fetch.
        """
        resp = self._client.patch(
            f"/api/v1/agents/{_encode_email(agent_email)}/messages/{message_id}",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return UpdateMessageResponse.model_validate(resp.json())

    def send_email(
        self,
        body: SendEmailRequest,
        idempotency_key: Optional[str] = None,
    ) -> SendEmailResponse:
        resp = self._client.post(
            "/api/v1/send",
            json=body.model_dump(by_alias=True, exclude_none=True),
            headers=_idempotency_header(idempotency_key),
        )
        _check_response(resp)
        return SendEmailResponse.model_validate(resp.json())

    # ── Conversations ─────────────────────────────────────────────────

    def list_conversations(
        self,
        agent_email: str,
        page_size: Optional[int] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
    ) -> ListConversationsResponse:
        """List conversations for an agent.

        Returns one row per non-empty ``conversation_id``, with
        aggregated counts and the latest message's subject/sender.
        Sorted by ``last_message_at`` DESC. The server caps the
        response at 100 entries (pagination is intentionally deferred).

        ``since`` / ``until`` are RFC3339 timestamps bracketing
        ``last_message_at``.
        """
        params: list[tuple[str, str]] = []
        if page_size is not None:
            params.append(("page_size", str(page_size)))
        if since:
            params.append(("since", since))
        if until:
            params.append(("until", until))
        resp = self._client.get(
            f"/api/v1/agents/{_encode_email(agent_email)}/conversations",
            params=params,
        )
        _check_response(resp)
        return ListConversationsResponse.model_validate(resp.json())

    def get_conversation(
        self,
        agent_email: str,
        conversation_id: str,
    ) -> ConversationDetail:
        """Fetch a single conversation with all member messages.

        The detail response includes the aggregate summary fields,
        the participants union (sender + recipient + to + cc + bcc),
        the labels union across members, and every member message in
        chronological order (oldest-first).

        Returns a 404-mapped ``E2AApiError`` when no non-expired
        messages exist for ``(agent, conversation_id)``. Cross-agent
        access is indistinguishable from not-found.
        """
        resp = self._client.get(
            f"/api/v1/agents/{_encode_email(agent_email)}/conversations/{_encode_email(conversation_id)}",
        )
        _check_response(resp)
        return ConversationDetail.model_validate(resp.json())

    # ── HITL (human-in-the-loop approval) ─────────────────────────────

    def list_pending_messages(self) -> ListPendingMessagesResponse:
        """List pending-approval messages across every owned agent,
        sorted by soonest-expiring first. Body columns are omitted from
        the summary rows — use :meth:`get_pending_message` for detail.
        """
        # No query param needed: the server defaults `status` to
        # pending_approval (and rejects every other value), so the call
        # stays clean.
        resp = self._client.get("/api/v1/pending")
        _check_response(resp)
        return ListPendingMessagesResponse.model_validate(resp.json())

    def get_pending_message(self, message_id: str) -> PendingMessageDetail:
        """Fetch the full detail of one held outbound message, including
        stored body and attachments while the row is still pending."""
        resp = self._client.get(
            f"/api/v1/messages/{quote(message_id, safe='')}",
        )
        _check_response(resp)
        return PendingMessageDetail.model_validate(resp.json())

    def approve_message(
        self,
        agent_email: str,
        message_id: str,
        overrides: Optional[ApprovePendingMessageRequest] = None,
        idempotency_key: Optional[str] = None,
    ) -> ApprovePendingMessageResponse:
        """Approve a held outbound message.

        ``agent_email`` is the message's owning agent — available on
        the ``list_pending_messages`` response (`agent_id`) and on the
        inbound webhook payload. The server returns 404 if it doesn't
        match the message's owner (anti-cross-agent guard).

        Pass ``overrides`` to approve with edits (any subset of
        subject / body_text / body_html / to / cc / bcc / attachments).
        Pass ``None`` (the default) to approve the draft as-is.

        ``idempotency_key`` is sent as the ``Idempotency-Key`` header.
        Approve fires a real SES send, so supplying a stable key
        derived from the review event makes retries safe (the server
        replays the original response instead of double-sending).
        When omitted the SDK mints a fresh UUIDv4 per call — that
        gives network-layer retry safety only; the per-call default
        does not survive an explicit retry loop.
        """
        payload = overrides.model_dump(by_alias=True, exclude_none=True) if overrides else {}
        resp = self._client.post(
            f"/api/v1/agents/{quote(agent_email, safe='')}/messages/{quote(message_id, safe='')}/approve",
            json=payload,
            headers=_idempotency_header(idempotency_key),
        )
        _check_response(resp)
        return ApprovePendingMessageResponse.model_validate(resp.json())

    def reject_message(
        self,
        agent_email: str,
        message_id: str,
        reason: str = "",
    ) -> RejectPendingMessageResponse:
        """Reject a held outbound message. The message is discarded and
        never sent. The optional ``reason`` is stored for audit.

        ``agent_email`` requirements match :meth:`approve_message`.
        """
        body = RejectPendingMessageRequest(reason=reason)
        resp = self._client.post(
            f"/api/v1/agents/{quote(agent_email, safe='')}/messages/{quote(message_id, safe='')}/reject",
            json=body.model_dump(by_alias=True, exclude_none=True),
        )
        _check_response(resp)
        return RejectPendingMessageResponse.model_validate(resp.json())

    # ── Discovery ─────────────────────────────────────────────────────

    def get_info(self) -> DeploymentInfo:
        """Fetch deployment-specific configuration (shared domain, public URL).

        Unauthenticated; uses the configured base_url. Mirror of the TS
        SDK's ``E2AApi.getInfo()``.
        """
        resp = self._client.get("/api/v1/info")
        _check_response(resp)
        return DeploymentInfo.model_validate(resp.json())

    # ── Events (slice 6/7: customer-facing event log + replay) ────────

    def list_events(
        self,
        *,
        type: Optional[str] = None,
        agent_id: Optional[str] = None,
        conversation_id: Optional[str] = None,
        message_id: Optional[str] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
        page_size: Optional[int] = None,
        token: Optional[str] = None,
    ) -> ListEventsResponse:
        """List webhook events in reverse-chronological order.

        Cursor-paginated via ``token`` / ``next_token``. Events past the
        30-day retention boundary are not returned.
        """
        params: dict[str, str] = {}
        if type:
            params["type"] = type
        if agent_id:
            params["agent_id"] = agent_id
        if conversation_id:
            params["conversation_id"] = conversation_id
        if message_id:
            params["message_id"] = message_id
        if since:
            params["since"] = since
        if until:
            params["until"] = until
        if page_size is not None:
            params["page_size"] = str(page_size)
        if token:
            params["token"] = token
        resp = self._client.get("/api/v1/events", params=params)
        _check_response(resp)
        return ListEventsResponse.model_validate(resp.json())

    def get_event(self, event_id: str) -> WebhookEvent:
        """Fetch a single event by id.

        Raises :class:`E2AApiError` with status 410 if the event is past
        the 30-day retention boundary.
        """
        resp = self._client.get(f"/api/v1/events/{quote(event_id, safe='')}")
        _check_response(resp)
        return WebhookEvent.model_validate(resp.json())

    def redeliver_event(
        self, event_id: str, webhook_id: Optional[str] = None
    ) -> RedeliverResponse:
        """Replay an event.

        ``webhook_id`` targets one subscriber; omitting it fans out to
        every originally-matched webhook. Reuses the original event id so
        customer-side dedup discards the replay if already processed.
        """
        body: dict[str, str] = {}
        if webhook_id:
            body["webhook_id"] = webhook_id
        resp = self._client.post(
            f"/api/v1/events/{quote(event_id, safe='')}/redeliver",
            json=body,
        )
        _check_response(resp)
        return RedeliverResponse.model_validate(resp.json())

    def redeliver_webhook_since(
        self, webhook_id: str, since: str
    ) -> RedeliverSinceResponse:
        """Bulk-replay every event a webhook matched since ``since`` (RFC3339).

        Window capped at 7 days by the server. Idempotent — events with
        a pending delivery for this webhook are skipped.
        """
        resp = self._client.post(
            f"/api/v1/webhooks/{quote(webhook_id, safe='')}/redeliver-since",
            json={"since": since},
        )
        _check_response(resp)
        return RedeliverSinceResponse.model_validate(resp.json())

    # ── Lifecycle ─────────────────────────────────────────────────────

    def close(self) -> None:
        """Close the underlying HTTP client."""
        self._client.close()

    def __enter__(self) -> E2AApi:
        return self

    def __exit__(self, *args: object) -> None:
        self.close()


def fetch_info(
    base_url: str = "https://e2a.dev",
    timeout: float = 30,
) -> DeploymentInfo:
    """Fetch deployment info without an API key.

    Useful before login — CLIs hit this during the initial discovery flow
    to populate config from a single base URL. Mirror of the TS SDK's
    ``E2AApi.fetchInfo()`` static method. Raises :class:`E2AApiError` on
    non-2xx responses.
    """
    base = base_url.rstrip("/")
    with httpx.Client(timeout=timeout) as c:
        resp = c.get(f"{base}/api/v1/info")
        _check_response(resp)
        return DeploymentInfo.model_validate(resp.json())

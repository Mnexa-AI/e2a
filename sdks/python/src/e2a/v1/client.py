"""The e2a high-level async client (Slice 8c).

A thin, namespaced ergonomic layer over the generated ``generated/`` base: resource
sub-clients (``client.agents``, ``client.messages``, …) wrap the generated
``*Api`` classes (composition, never inheritance), map the generated
``ApiException`` to the typed :mod:`e2a.v1.errors` hierarchy, and expose cursor
lists as an :class:`~e2a.v1.pagination.AutoPager`. Async-only.
"""

from __future__ import annotations

import os
from typing import Any, Awaitable, Callable, List, Optional, Sequence, Type, TypeVar, Union

from pydantic import ValidationError

from ._retry import RetryConfig, request_with_retry
from .errors import E2AError, E2AValidationError
from .generated.api.account_api import AccountApi
from .generated.api.agents_api import AgentsApi
from .generated.api.conversations_api import ConversationsApi
from .generated.api.domains_api import DomainsApi
from .generated.api.events_api import EventsApi
from .generated.api.messages_api import MessagesApi
from .generated.api.meta_api import MetaApi
from .generated.api.webhooks_api import WebhooksApi
from .generated.api_client import ApiClient
from .generated.configuration import Configuration
from .generated.models import (
    AgentView,
    ApproveRequest,
    ApproveResultView,
    ConversationDetailView,
    ConversationSummaryView,
    CreateAgentRequest,
    CreateAgentResponse,
    CreateWebhookRequest,
    DeleteUserDataResult,
    DeploymentInfoView,
    DomainView,
    EventJSON,
    ForwardRequest,
    LimitsView,
    MessageSummaryView,
    MessageView,
    RedeliverEventInputBody,
    RedeliverView,
    RegisterDomainRequest,
    RejectInputBody,
    ReplyRequest,
    RotateSecretBody,
    SendEmailRequest,
    SendResultView,
    Suppression,
    TestWebhookOutputBody,
    TestWebhookRequest,
    UpdateAgentRequest,
    UpdateDomainRequest,
    UpdateMessageRequest,
    UpdateMessageResultView,
    UpdateWebhookRequest,
    UserExport,
    VerifyDomainView,
    WebhookDeliveryView,
    WebhookView,
)
from .pagination import AutoPager, Page

__all__ = ["E2AClient"]

T = TypeVar("T")
_Make = Callable[[Optional["dict[str, str]"]], Awaitable[Any]]
# A request body accepted as the typed model or a plain dict.
Body = Union[Any, dict]

DEFAULT_BASE_URL = "https://api.e2a.dev"


def _env(name: str) -> Optional[str]:
    v = os.environ.get(name)
    return v or None


def _coerce(model_cls: Type[T], body: Optional[Body]) -> T:
    if body is None:
        return model_cls()  # type: ignore[call-arg]
    if isinstance(body, model_cls):
        return body
    try:
        return model_cls.model_validate(body)  # type: ignore[attr-defined]
    except ValidationError as e:
        # A bad request body is the caller's input error — surface it typed
        # rather than leaking a raw pydantic ValidationError.
        raise E2AValidationError(
            code="invalid_request_body",
            message=f"invalid request body for {model_cls.__name__}: {e}",
            status=0,
            retryable=False,
        ) from e


class E2AClient:
    """Async client for the e2a /v1 API.

    Use as an async context manager so the underlying HTTP connections are
    closed::

        async with E2AClient(api_key="e2a_...") as client:
            agents = await client.agents.list().to_list(limit=100)
    """

    def __init__(
        self,
        api_key: Optional[str] = None,
        *,
        base_url: Optional[str] = None,
        max_retries: int = 2,
        max_elapsed_ms: Optional[float] = None,
        _retry_config: Optional[RetryConfig] = None,
    ) -> None:
        key = api_key or _env("E2A_API_KEY")
        if not key:
            raise E2AError(
                code="no_api_key",
                message="api_key is required — pass api_key=... or set E2A_API_KEY",
                status=0,
                retryable=False,
            )
        self._api_key = key
        self._base_url = base_url or _env("E2A_BASE_URL") or DEFAULT_BASE_URL
        self._cfg = _retry_config or RetryConfig(
            max_retries=max_retries, max_elapsed_ms=max_elapsed_ms
        )

        config = Configuration(host=self._base_url, access_token=key)
        self._api_client = ApiClient(config)

        self.agents = AgentsResource(AgentsApi(self._api_client), self)
        self.messages = MessagesResource(MessagesApi(self._api_client), self)
        self.conversations = ConversationsResource(ConversationsApi(self._api_client), self)
        self.domains = DomainsResource(DomainsApi(self._api_client), self)
        self.events = EventsResource(EventsApi(self._api_client), self)
        self.webhooks = WebhooksResource(WebhooksApi(self._api_client), self)
        self.account = AccountResource(AccountApi(self._api_client), self)
        self._meta = MetaApi(self._api_client)

    # ── lifecycle ───────────────────────────────────────────────────
    async def aclose(self) -> None:
        await self._api_client.close()

    async def __aenter__(self) -> "E2AClient":
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.aclose()

    # ── shared executors (retry + error mapping live here) ──────────
    async def _read(self, make: _Make) -> Any:
        return await request_with_retry(make, cfg=self._cfg, retryable=True, idempotency=False)

    async def _write_keyed(self, make: _Make, idempotency_key: Optional[str]) -> Any:
        # send/reply/forward/approve: server dedupes on the key → safe to retry.
        return await request_with_retry(
            make, cfg=self._cfg, retryable=True, idempotency=True, idempotency_key=idempotency_key
        )

    async def _write_idempotent(self, make: _Make) -> Any:
        # PUT/PATCH/DELETE: HTTP-idempotent → safe to retry.
        return await request_with_retry(make, cfg=self._cfg, retryable=True, idempotency=True)

    async def _write_unsafe(self, make: _Make) -> Any:
        # Bare POST (create/reject/redeliver/test): NOT retried (avoid double-create).
        return await request_with_retry(make, cfg=self._cfg, retryable=False, idempotency=False)

    # ── public top-level ────────────────────────────────────────────
    async def info(self) -> DeploymentInfoView:
        """Public deployment metadata."""
        return await self._read(lambda h: self._meta.get_info(_headers=h))

    def listen(self, address: Optional[str] = None) -> Any:
        """Open a notification stream for an agent's inbox.

        ``address`` falls back to ``E2A_AGENT_EMAIL``. Yields lightweight
        notifications; fetch the body with ``client.messages.get(address, id)``.
        """
        target = address or _env("E2A_AGENT_EMAIL")
        if not target:
            raise E2AError(
                code="missing_address",
                message="address is required — pass client.listen(address) or set E2A_AGENT_EMAIL",
                status=0,
                retryable=False,
            )
        from .websocket import WSStream  # local import: optional `websockets` dep

        return WSStream(api_key=self._api_key, agent_email=target, base_url=self._base_url)


def _page(items: Optional[Sequence[T]], next_cursor: Optional[str] = None) -> Page:
    return Page(items=items or [], next_cursor=next_cursor)


class AgentsResource:
    def __init__(self, api: AgentsApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self) -> AutoPager[AgentView]:
        async def fetch(_cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_agents(_headers=h))
            return _page(resp.items)

        return AutoPager(fetch)

    async def get(self, address: str) -> AgentView:
        return await self._c._read(lambda h: self._api.get_agent(address, _headers=h))

    async def create(self, body: Body) -> CreateAgentResponse:
        req = _coerce(CreateAgentRequest, body)
        return await self._c._write_unsafe(lambda h: self._api.create_agent(req, _headers=h))

    async def update(self, address: str, patch: Body) -> AgentView:
        req = _coerce(UpdateAgentRequest, patch)
        return await self._c._write_idempotent(
            lambda h: self._api.update_agent(address, req, _headers=h)
        )

    async def delete(self, address: str) -> None:
        await self._c._write_idempotent(lambda h: self._api.delete_agent(address, _headers=h))

    async def test(self, address: str) -> SendResultView:
        return await self._c._write_unsafe(lambda h: self._api.test_agent(address, _headers=h))


class MessagesResource:
    def __init__(self, api: MessagesApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(
        self,
        address: str,
        *,
        direction: Optional[str] = None,
        status: Optional[str] = None,
        sort: Optional[str] = None,
        var_from: Optional[str] = None,
        subject_contains: Optional[str] = None,
        conversation_id: Optional[str] = None,
        labels: Optional[List[str]] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> AutoPager[MessageSummaryView]:
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(
                lambda h: self._api.list_messages(
                    address,
                    direction=direction,
                    status=status,
                    sort=sort,
                    var_from=var_from,
                    subject_contains=subject_contains,
                    conversation_id=conversation_id,
                    labels=labels,
                    since=since,
                    until=until,
                    cursor=cursor,
                    limit=limit,
                    _headers=h,
                )
            )
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def get(self, address: str, message_id: str) -> MessageView:
        return await self._c._read(lambda h: self._api.get_message(address, message_id, _headers=h))

    async def send(
        self, address: str, body: Body, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        req = _coerce(SendEmailRequest, body)
        return await self._c._write_keyed(
            lambda h: self._api.send_message(address, req, _headers=h), idempotency_key
        )

    async def reply(
        self, address: str, message_id: str, body: Body, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        req = _coerce(ReplyRequest, body)
        return await self._c._write_keyed(
            lambda h: self._api.reply_to_message(address, message_id, req, _headers=h),
            idempotency_key,
        )

    async def forward(
        self, address: str, message_id: str, body: Body, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        req = _coerce(ForwardRequest, body)
        return await self._c._write_keyed(
            lambda h: self._api.forward_message(address, message_id, req, _headers=h),
            idempotency_key,
        )

    async def approve(
        self,
        address: str,
        message_id: str,
        body: Optional[Body] = None,
        *,
        idempotency_key: Optional[str] = None,
    ) -> ApproveResultView:
        req = _coerce(ApproveRequest, body)
        return await self._c._write_keyed(
            lambda h: self._api.approve_message(address, message_id, req, _headers=h),
            idempotency_key,
        )

    async def reject(self, address: str, message_id: str, body: Optional[Body] = None) -> Any:
        req = _coerce(RejectInputBody, body)
        return await self._c._write_unsafe(
            lambda h: self._api.reject_message(address, message_id, req, _headers=h)
        )

    async def update_labels(
        self, address: str, message_id: str, body: Body
    ) -> UpdateMessageResultView:
        req = _coerce(UpdateMessageRequest, body)
        return await self._c._write_idempotent(
            lambda h: self._api.update_message(address, message_id, req, _headers=h)
        )


class ConversationsResource:
    def __init__(self, api: ConversationsApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(
        self,
        address: str,
        *,
        since: Optional[str] = None,
        until: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> AutoPager[ConversationSummaryView]:
        # No cursor param — single page by contract; AutoPager for ergonomic
        # consistency with every other .list() (yields one page, terminates).
        async def fetch(_cursor: Optional[str]) -> Page:
            resp = await self._c._read(
                lambda h: self._api.list_conversations(
                    address, since=since, until=until, limit=limit, _headers=h
                )
            )
            return _page(resp.items)

        return AutoPager(fetch)

    async def get(self, address: str, conversation_id: str) -> ConversationDetailView:
        return await self._c._read(
            lambda h: self._api.get_conversation(address, conversation_id, _headers=h)
        )


class DomainsResource:
    def __init__(self, api: DomainsApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self) -> AutoPager[DomainView]:
        async def fetch(_cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_domains(_headers=h))
            return _page(resp.items)

        return AutoPager(fetch)

    async def get(self, domain: str) -> DomainView:
        return await self._c._read(lambda h: self._api.get_domain(domain, _headers=h))

    async def create(self, body: Body) -> DomainView:
        req = _coerce(RegisterDomainRequest, body)
        return await self._c._write_unsafe(lambda h: self._api.register_domain(req, _headers=h))

    async def update(self, domain: str, patch: Body) -> DomainView:
        req = _coerce(UpdateDomainRequest, patch)
        return await self._c._write_idempotent(
            lambda h: self._api.update_domain(domain, req, _headers=h)
        )

    async def delete(self, domain: str) -> None:
        await self._c._write_idempotent(lambda h: self._api.delete_domain(domain, _headers=h))

    async def verify(self, domain: str) -> VerifyDomainView:
        return await self._c._write_unsafe(lambda h: self._api.verify_domain(domain, _headers=h))


class EventsResource:
    def __init__(self, api: EventsApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(
        self,
        *,
        type: Optional[str] = None,
        agent_id: Optional[str] = None,
        conversation_id: Optional[str] = None,
        message_id: Optional[str] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> AutoPager[EventJSON]:
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(
                lambda h: self._api.list_events(
                    type=type,
                    agent_id=agent_id,
                    conversation_id=conversation_id,
                    message_id=message_id,
                    since=since,
                    until=until,
                    cursor=cursor,
                    limit=limit,
                    _headers=h,
                )
            )
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def get(self, event_id: str) -> EventJSON:
        return await self._c._read(lambda h: self._api.get_event(event_id, _headers=h))

    async def redeliver(self, event_id: str, body: Optional[Body] = None) -> RedeliverView:
        req = _coerce(RedeliverEventInputBody, body)
        return await self._c._write_unsafe(
            lambda h: self._api.redeliver_event(event_id, req, _headers=h)
        )


class WebhooksResource:
    def __init__(self, api: WebhooksApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self) -> AutoPager[WebhookView]:
        async def fetch(_cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_webhooks(_headers=h))
            return _page(resp.items)

        return AutoPager(fetch)

    async def get(self, webhook_id: str) -> WebhookView:
        return await self._c._read(lambda h: self._api.get_webhook(webhook_id, _headers=h))

    async def create(self, body: Body) -> WebhookView:
        req = _coerce(CreateWebhookRequest, body)
        return await self._c._write_unsafe(lambda h: self._api.create_webhook(req, _headers=h))

    async def update(self, webhook_id: str, patch: Body) -> WebhookView:
        req = _coerce(UpdateWebhookRequest, patch)
        return await self._c._write_idempotent(
            lambda h: self._api.update_webhook(webhook_id, req, _headers=h)
        )

    async def delete(self, webhook_id: str) -> None:
        await self._c._write_idempotent(lambda h: self._api.delete_webhook(webhook_id, _headers=h))

    async def rotate_secret(self, webhook_id: str) -> RotateSecretBody:
        # Server-deduped via Idempotency-Key: a retried rotate replays the first
        # secret instead of minting a second. Mint a key + retry (parity with the
        # TS SDK, which retries rotate for the same reason).
        return await self._c._write_idempotent(
            lambda h: self._api.rotate_webhook_secret(webhook_id, _headers=h)
        )

    async def test(self, webhook_id: str, body: Optional[Body] = None) -> TestWebhookOutputBody:
        req = _coerce(TestWebhookRequest, body)
        return await self._c._write_unsafe(
            lambda h: self._api.test_webhook(webhook_id, req, _headers=h)
        )

    def deliveries(
        self, webhook_id: str, *, status: Optional[str] = None, limit: Optional[int] = None
    ) -> AutoPager[WebhookDeliveryView]:
        async def fetch(_cursor: Optional[str]) -> Page:
            resp = await self._c._read(
                lambda h: self._api.list_webhook_deliveries(
                    webhook_id, status=status, limit=limit, _headers=h
                )
            )
            # No cursor param: drop next_cursor so the pager stops after one page.
            return _page(resp.items, None)

        return AutoPager(fetch)


class SuppressionsResource:
    def __init__(self, api: AccountApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self) -> AutoPager[Suppression]:
        async def fetch(_cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_suppressions(_headers=h))
            return _page(resp.items)

        return AutoPager(fetch)

    async def delete(self, address: str) -> None:
        await self._c._write_idempotent(lambda h: self._api.delete_suppression(address, _headers=h))


class AccountResource:
    def __init__(self, api: AccountApi, client: E2AClient) -> None:
        self._api = api
        self._c = client
        self.suppressions = SuppressionsResource(api, client)

    async def get(self) -> LimitsView:
        return await self._c._read(lambda h: self._api.get_account(_headers=h))

    async def export(self) -> UserExport:
        return await self._c._read(lambda h: self._api.export_account(_headers=h))

    async def delete(self, confirm: Optional[str] = None) -> DeleteUserDataResult:
        # Deliberately NOT retried (unlike the other DELETEs): account deletion is
        # irreversible, so a transient failure should surface loudly to the caller
        # rather than silently re-firing.
        return await self._c._write_unsafe(
            lambda h: self._api.delete_account(confirm=confirm, _headers=h)
        )

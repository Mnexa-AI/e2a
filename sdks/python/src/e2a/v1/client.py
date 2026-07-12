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
from .webhook_signature import WebhookEvent
from .generated.api.account_api import AccountApi
from .generated.api.agents_api import AgentsApi
from .generated.api.conversations_api import ConversationsApi
from .generated.api.domains_api import DomainsApi
from .generated.api.events_api import EventsApi
from .generated.api.messages_api import MessagesApi
from .generated.api.meta_api import MetaApi
from .generated.api.reviews_api import ReviewsApi
from .generated.api.templates_api import TemplatesApi
from .generated.api.webhooks_api import WebhooksApi
from .generated.api_client import ApiClient
from .generated.configuration import Configuration
from .generated.models import (
    AgentView,
    APIKeyView,
    ApproveRequest,
    ConversationDetailView,
    ConversationSummaryView,
    CreateAgentRequest,
    CreateAPIKeyRequest,
    CreateAPIKeyResponse,
    CreateWebhookRequest,
    CreateWebhookResponse,
    DeleteUserDataResult,
    DeploymentInfoView,
    DomainView,
    EventJSON,
    ForwardRequest,
    AccountView,
    AttachmentView,
    MessageSummaryView,
    MessageView,
    RedeliverEventRequest,
    RedeliverView,
    RegisterDomainRequest,
    RejectRequest,
    RejectResultView,
    ReplyRequest,
    ReviewView,
    RotateSecretResponse,
    SendEmailRequest,
    SendResultView,
    StarterTemplateDetailView,
    StarterTemplateView,
    Suppression,
    TemplateSummaryView,
    TemplateView,
    TestWebhookResponse,
    TestWebhookRequest,
    ProtectionConfigView,
    CreateTemplateRequest,
    UpdateAgentRequest,
    UpdateMessageRequest,
    UpdateMessageResultView,
    UpdateTemplateRequest,
    UpdateWebhookRequest,
    ValidateTemplateRequest,
    ValidateTemplateResponse,
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
        timeout_ms: Optional[float] = 30_000.0,
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

        # Per-request timeout (default 30s). The generated httpx transport applies
        # `_request_timeout or 300s` per call; we inject our default when the caller
        # didn't pass one, so every request is bounded without threading a timeout
        # through each resource method. A timeout raises httpx.TimeoutException (a
        # TransportError), which the retry layer treats as a retryable connection
        # failure. Pass timeout_ms=None or 0 to fall back to the transport default.
        self._timeout_s = (timeout_ms / 1000.0) if timeout_ms and timeout_ms > 0 else None
        if self._timeout_s is not None:
            _rest = self._api_client.rest_client
            _orig_request = _rest.request

            # Assumes the generated ApiClient calls rest_client.request(...) with
            # `_request_timeout` as a KEYWORD (it does — see generated
            # api_client.py). If a future openapi-generator bump passes it
            # positionally, `.get()` would miss it and we'd re-inject as a kwarg
            # → TypeError; `make generate-sdk-check` (CI) gates that drift, and a
            # regen would surface it here. Keep this in sync if that call shape
            # changes.
            async def _request_with_default_timeout(*args: Any, **kwargs: Any) -> Any:
                if kwargs.get("_request_timeout") is None:
                    kwargs["_request_timeout"] = self._timeout_s
                return await _orig_request(*args, **kwargs)

            _rest.request = _request_with_default_timeout  # type: ignore[method-assign]

        self.agents = AgentsResource(AgentsApi(self._api_client), self)
        self.messages = MessagesResource(MessagesApi(self._api_client), self)
        self.conversations = ConversationsResource(ConversationsApi(self._api_client), self)
        self.domains = DomainsResource(DomainsApi(self._api_client), self)
        self.events = EventsResource(EventsApi(self._api_client), self)
        self.webhooks = WebhooksResource(WebhooksApi(self._api_client), self)
        self.account = AccountResource(AccountApi(self._api_client), self)
        self.reviews = ReviewsResource(ReviewsApi(self._api_client), self)
        self.templates = TemplatesResource(TemplatesApi(self._api_client), self)
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

    def listen(self, email: str) -> Any:
        """Open a notification stream for an agent's inbox.

        Yields lightweight notifications; fetch the body with
        ``client.messages.get(email, id)``.
        """
        if not email:
            raise E2AError(
                code="missing_email",
                message="email is required — pass client.listen(email)",
                status=0,
                retryable=False,
            )
        from .websocket import WSStream  # local import: optional `websockets` dep

        return WSStream(api_key=self._api_key, agent_email=email, base_url=self._base_url)


def _page(items: Optional[Sequence[T]], next_cursor: Optional[str] = None) -> Page:
    return Page(items=items or [], next_cursor=next_cursor)


class AgentsResource:
    def __init__(self, api: AgentsApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self, *, limit: Optional[int] = None) -> AutoPager[AgentView]:
        # Cursor-paginated: the AutoPager walks next_cursor to completion.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_agents(cursor=cursor, limit=limit, _headers=h))
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def get(self, email: str) -> AgentView:
        return await self._c._read(lambda h: self._api.get_agent(email, _headers=h))

    async def create(self, body: Body) -> AgentView:
        req = _coerce(CreateAgentRequest, body)
        return await self._c._write_unsafe(lambda h: self._api.create_agent(req, _headers=h))

    async def update(self, email: str, patch: Body) -> AgentView:
        req = _coerce(UpdateAgentRequest, patch)
        return await self._c._write_idempotent(
            lambda h: self._api.update_agent(email, req, _headers=h)
        )

    async def get_protection(self, email: str) -> ProtectionConfigView:
        """Read an agent's protection config (gate + scan sensitivity + holds).

        Beta; account scope only — an agent-scoped key cannot read its own config.
        """
        return await self._c._read(lambda h: self._api.get_agent_protection(email, _headers=h))

    async def replace_protection(self, email: str, config: Body) -> ProtectionConfigView:
        """Replace an agent's protection config wholesale (all three top-level
        keys required). Beta; account scope only. PUT is idempotent."""
        req = _coerce(ProtectionConfigView, config)
        return await self._c._write_idempotent(
            lambda h: self._api.put_agent_protection(email, req, _headers=h)
        )

    async def delete(self, email: str) -> None:
        # The typed .delete() call is the confirmation; the SDK supplies the
        # ?confirm=DELETE guard the raw API requires (AG-6).
        await self._c._write_idempotent(lambda h: self._api.delete_agent(email, confirm="DELETE", _headers=h))

    async def test(self, email: str) -> SendResultView:
        return await self._c._write_unsafe(lambda h: self._api.test_agent(email, _headers=h))


class MessagesResource:
    def __init__(self, api: MessagesApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(
        self,
        email: str,
        *,
        direction: Optional[str] = None,
        read_status: Optional[str] = None,
        sort: Optional[str] = None,
        from_: Optional[str] = None,
        subject_contains: Optional[str] = None,
        conversation_id: Optional[str] = None,
        labels: Optional[List[str]] = None,
        since: Optional[str] = None,
        until: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> AutoPager[MessageSummaryView]:
        # `from` is a Python keyword; expose the idiomatic `from_` (PEP 8 trailing
        # underscore) and translate to the generated base's `var_from` here so the
        # generator's mangled name never leaks into the public SDK surface.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(
                lambda h: self._api.list_messages(
                    email,
                    direction=direction,
                    read_status=read_status,
                    sort=sort,
                    var_from=from_,
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

    async def get(self, email: str, message_id: str) -> MessageView:
        return await self._c._read(lambda h: self._api.get_message(email, message_id, _headers=h))

    async def get_attachment(
        self, email: str, message_id: str, index: int, *, inline: bool = False
    ) -> AttachmentView:
        # Metadata + a short-lived download_url (+ expires_at). inline=True also
        # returns base64 `data` for small attachments (<=256 KB; larger error).
        return await self._c._read(
            lambda h: self._api.get_attachment(email, message_id, index, inline=inline, _headers=h)
        )

    async def send(
        self, email: str, body: Body, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        req = _coerce(SendEmailRequest, body)
        return await self._c._write_keyed(
            lambda h: self._api.send_message(email, req, _headers=h), idempotency_key
        )

    async def reply(
        self, email: str, message_id: str, body: Body, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        req = _coerce(ReplyRequest, body)
        return await self._c._write_keyed(
            lambda h: self._api.reply_to_message(email, message_id, req, _headers=h),
            idempotency_key,
        )

    async def forward(
        self, email: str, message_id: str, body: Body, *, idempotency_key: Optional[str] = None
    ) -> SendResultView:
        req = _coerce(ForwardRequest, body)
        return await self._c._write_keyed(
            lambda h: self._api.forward_message(email, message_id, req, _headers=h),
            idempotency_key,
        )

    # Approve/reject a held message live on the account-scoped review queue —
    # ``client.reviews.approve(message_id, body)`` /
    # ``client.reviews.reject(message_id, body)``. The deprecated per-inbox
    # messages.approve/reject was removed in the pre-GA vocabulary freeze (a
    # review is addressed by message id alone).

    async def update_labels(
        self, email: str, message_id: str, body: Body
    ) -> UpdateMessageResultView:
        req = _coerce(UpdateMessageRequest, body)
        return await self._c._write_idempotent(
            lambda h: self._api.update_message(email, message_id, req, _headers=h)
        )


class ReviewsResource:
    """The account-scoped human-review queue: every message held in
    pending_review (outbound drafts awaiting send approval + inbound messages
    held by a screening gate). Supersedes the per-inbox messages.approve/reject
    path — reviews are addressed by message id alone, no inbox email. Account-
    scoped credentials only; an agent cannot see or resolve holds."""

    def __init__(self, api: ReviewsApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self, *, limit: Optional[int] = None) -> AutoPager[ReviewView]:
        # Cursor-paginated: the AutoPager walks next_cursor to completion.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_reviews(cursor=cursor, limit=limit, _headers=h))
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def get(self, message_id: str) -> MessageView:
        return await self._c._read(lambda h: self._api.get_review(message_id, _headers=h))

    async def approve(
        self,
        message_id: str,
        body: Optional[Body] = None,
        *,
        idempotency_key: Optional[str] = None,
    ) -> SendResultView:
        req = _coerce(ApproveRequest, body)
        return await self._c._write_keyed(
            lambda h: self._api.approve_review(message_id, req, _headers=h),
            idempotency_key,
        )

    async def reject(
        self, message_id: str, body: Optional[Body] = None
    ) -> RejectResultView:
        req = _coerce(RejectRequest, body)
        return await self._c._write_unsafe(
            lambda h: self._api.reject_review(message_id, req, _headers=h)
        )


class TemplatesResource:
    """Reusable email templates + the read-only starter catalog (beta — shapes
    may change before templates are declared stable). Account scope only; the
    send-side reference lives on ``messages.send`` (``template_id`` /
    ``template_alias`` / ``template_data``, mutually exclusive with a literal
    subject/body)."""

    def __init__(self, api: TemplatesApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self) -> AutoPager[TemplateSummaryView]:
        """List the account's stored templates, newest first. Summary rows only
        (no text/html sources) — ``get(id)`` returns the full sources."""

        async def fetch(_cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_templates(_headers=h))
            # No cursor param: single-page at GA — see AgentsResource.list.
            return _page(resp.items)

        return AutoPager(fetch)

    async def get(self, template_id: str) -> TemplateView:
        """Fetch one stored template by id (tmpl_…), including its sources."""
        return await self._c._read(lambda h: self._api.get_template(template_id, _headers=h))

    async def create(self, body: Body) -> TemplateView:
        """Create a template from literal source (name + subject + body), or copy
        a starter verbatim via ``from_starter`` (mutually exclusive with the
        source fields — edit the created copy afterwards with ``update``). Bare
        POST: not retried (mirrors agents/domains/webhooks create), since the
        create has no server-side idempotency dedup."""
        req = _coerce(CreateTemplateRequest, body)
        return await self._c._write_unsafe(lambda h: self._api.create_template(req, _headers=h))

    async def update(self, template_id: str, patch: Body) -> TemplateView:
        """Partial update; omitted fields are left unchanged. Changed parts are
        re-parsed. Set alias or html to "" to clear them. PATCH is
        idempotent → safe to retry."""
        req = _coerce(UpdateTemplateRequest, patch)
        return await self._c._write_idempotent(
            lambda h: self._api.update_template(template_id, req, _headers=h)
        )

    async def delete(self, template_id: str) -> None:
        # In-flight sends are unaffected (rendering happens at send time). DELETE
        # is idempotent → safe to retry.
        await self._c._write_idempotent(lambda h: self._api.delete_template(template_id, confirm="DELETE", _headers=h))

    async def validate(self, body: Body) -> ValidateTemplateResponse:
        """Dry-run template source without persisting: per-part parse errors, a
        rendered preview against test_data (present only when valid), and
        suggested_data — a nested placeholder object covering every variable the
        source references. Side-effect-free → treated as a retryable read."""
        req = _coerce(ValidateTemplateRequest, body)
        return await self._c._read(lambda h: self._api.validate_template(req, _headers=h))

    def list_starters(self) -> AutoPager[StarterTemplateView]:
        """List the pre-built starter templates shipped with the deployment
        (catalog metadata + variables; ``get_starter(alias)`` adds the full body
        sources)."""

        async def fetch(_cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_starter_templates(_headers=h))
            # No cursor param: single-page at GA — see AgentsResource.list.
            return _page(resp.items)

        return AutoPager(fetch)

    async def get_starter(self, alias: str) -> StarterTemplateDetailView:
        """Fetch one starter by alias, including its full body sources. Starters
        are read-only masters — copy one with ``create({"from_starter": alias})``."""
        return await self._c._read(lambda h: self._api.get_starter_template(alias, _headers=h))


class ConversationsResource:
    def __init__(self, api: ConversationsApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(
        self,
        email: str,
        *,
        since: Optional[str] = None,
        until: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> AutoPager[ConversationSummaryView]:
        # Cursor-paginated (CV-3): the AutoPager walks next_cursor to completion.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(
                lambda h: self._api.list_conversations(
                    email, since=since, until=until, cursor=cursor, limit=limit, _headers=h
                )
            )
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def get(self, email: str, conversation_id: str) -> ConversationDetailView:
        return await self._c._read(
            lambda h: self._api.get_conversation(email, conversation_id, _headers=h)
        )


class DomainsResource:
    def __init__(self, api: DomainsApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self, *, limit: Optional[int] = None) -> AutoPager[DomainView]:
        # Cursor-paginated: the AutoPager walks next_cursor to completion.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_domains(cursor=cursor, limit=limit, _headers=h))
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def get(self, domain: str) -> DomainView:
        return await self._c._read(lambda h: self._api.get_domain(domain, _headers=h))

    async def create(self, body: Body) -> DomainView:
        req = _coerce(RegisterDomainRequest, body)
        return await self._c._write_unsafe(lambda h: self._api.register_domain(req, _headers=h))

    async def delete(self, domain: str) -> None:
        await self._c._write_idempotent(lambda h: self._api.delete_domain(domain, confirm="DELETE", _headers=h))

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
        agent_email: Optional[str] = None,
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
                    agent_email=agent_email,
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
        req = _coerce(RedeliverEventRequest, body)
        return await self._c._write_unsafe(
            lambda h: self._api.redeliver_event(event_id, req, _headers=h)
        )


class WebhooksResource:
    def __init__(self, api: WebhooksApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    async def fetch_message(self, event: WebhookEvent) -> MessageView:
        """Fetch the full message referenced by an ``email.received`` event.

        The event is a metadata-only notification; this resolves its
        ``(delivered_to, message_id)`` fetch keys and returns the full
        :class:`MessageView` (body, attachments, signed headers). Raises
        ``ValueError`` if the event is not an ``email.received`` carrying those
        keys.
        """
        data = event.data if isinstance(event.data, dict) else {}
        message_id = data.get("message_id")
        delivered_to = data.get("delivered_to")
        if event.type != "email.received" or not message_id or not delivered_to:
            raise ValueError(
                "fetch_message expects an email.received event with message_id and delivered_to"
            )
        return await self._c.messages.get(delivered_to, message_id)

    def list(self, *, limit: Optional[int] = None) -> AutoPager[WebhookView]:
        # Cursor-paginated: the AutoPager walks next_cursor to completion.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_webhooks(cursor=cursor, limit=limit, _headers=h))
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def get(self, webhook_id: str) -> WebhookView:
        return await self._c._read(lambda h: self._api.get_webhook(webhook_id, _headers=h))

    async def create(self, body: Body) -> CreateWebhookResponse:
        req = _coerce(CreateWebhookRequest, body)
        return await self._c._write_unsafe(lambda h: self._api.create_webhook(req, _headers=h))

    async def update(self, webhook_id: str, patch: Body) -> WebhookView:
        req = _coerce(UpdateWebhookRequest, patch)
        return await self._c._write_idempotent(
            lambda h: self._api.update_webhook(webhook_id, req, _headers=h)
        )

    async def delete(self, webhook_id: str) -> None:
        await self._c._write_idempotent(lambda h: self._api.delete_webhook(webhook_id, confirm="DELETE", _headers=h))

    async def rotate_secret(self, webhook_id: str) -> RotateSecretResponse:
        # Server-deduped via Idempotency-Key: a retried rotate replays the first
        # secret instead of minting a second. Mint a key + retry (parity with the
        # TS SDK, which retries rotate for the same reason).
        return await self._c._write_idempotent(
            lambda h: self._api.rotate_webhook_secret(webhook_id, _headers=h)
        )

    async def test(self, webhook_id: str, body: Optional[Body] = None) -> TestWebhookResponse:
        req = _coerce(TestWebhookRequest, body)
        return await self._c._write_unsafe(
            lambda h: self._api.test_webhook(webhook_id, req, _headers=h)
        )

    def deliveries(
        self, webhook_id: str, *, status: Optional[str] = None, limit: Optional[int] = None
    ) -> AutoPager[WebhookDeliveryView]:
        # Cursor-paginated: the AutoPager walks next_cursor to completion. The
        # status filter is pinned into the cursor server-side, which the pager
        # honors by keeping status constant across follow-up requests.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(
                lambda h: self._api.list_webhook_deliveries(
                    webhook_id, status=status, cursor=cursor, limit=limit, _headers=h
                )
            )
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)


class SuppressionsResource:
    def __init__(self, api: AccountApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self) -> AutoPager[Suppression]:
        # Cursor-paginated (A-5): walks next_cursor to completion.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_suppressions(cursor=cursor, _headers=h))
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def delete(self, email: str) -> None:
        await self._c._write_idempotent(lambda h: self._api.delete_suppression(email, confirm="DELETE", _headers=h))


class APIKeysResource:
    def __init__(self, api: AccountApi, client: E2AClient) -> None:
        self._api = api
        self._c = client

    def list(self, *, limit: Optional[int] = None) -> AutoPager[APIKeyView]:
        # Cursor-paginated: the AutoPager walks next_cursor to completion.
        async def fetch(cursor: Optional[str]) -> Page:
            resp = await self._c._read(lambda h: self._api.list_api_keys(cursor=cursor, limit=limit, _headers=h))
            return _page(resp.items, resp.next_cursor)

        return AutoPager(fetch)

    async def create(self, body: Body) -> CreateAPIKeyResponse:
        # Returns the one-time plaintext key in `.key` — store it now. Not
        # retried (mirrors webhooks.create): minting a secret isn't safely
        # replayable.
        req = _coerce(CreateAPIKeyRequest, body)
        return await self._c._write_unsafe(lambda h: self._api.create_api_key(req, _headers=h))

    async def delete(self, key_id: str) -> None:
        await self._c._write_idempotent(lambda h: self._api.delete_api_key(key_id, confirm="DELETE", _headers=h))


class AccountResource:
    def __init__(self, api: AccountApi, client: E2AClient) -> None:
        self._api = api
        self._c = client
        self.suppressions = SuppressionsResource(api, client)
        self.api_keys = APIKeysResource(api, client)

    async def get(self) -> AccountView:
        return await self._c._read(lambda h: self._api.get_account(_headers=h))

    async def export(self) -> UserExport:
        return await self._c._read(lambda h: self._api.export_account(_headers=h))

    async def delete(self) -> DeleteUserDataResult:
        # Deliberately NOT retried (unlike the other DELETEs): account deletion is
        # irreversible, so a transient failure should surface loudly to the caller
        # rather than silently re-firing. The typed .delete() call is the
        # confirmation; the SDK supplies the ?confirm=DELETE guard.
        return await self._c._write_unsafe(
            lambda h: self._api.delete_account(confirm="DELETE", _headers=h)
        )

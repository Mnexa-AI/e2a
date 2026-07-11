"""Unit tests for the namespaced async E2AClient (Slice 8c-2).

Mocks httpx (the layer the generated base uses) so these exercise the full
stack: namespaced resource -> generated *Api -> bearer auth -> retry layer ->
httpx -> envelope unwrap -> typed-error mapping.
"""

import httpx
import pytest

from e2a.v1._retry import RetryConfig
from e2a.v1.client import E2AClient
from e2a.v1.errors import (
    E2AConflictError,
    E2AConnectionError,
    E2AError,
    E2ANotFoundError,
    E2APermissionError,
    E2AValidationError,
)
from e2a.v1.webhook_signature import WebhookEvent
from e2a.v1.generated.models import (
    AgentView,
    AttachmentView,
    ConversationSummaryView,
    DomainView,
    EventJSON,
    MessageSummaryView,
    MessageView,
    ReviewView,
    StarterTemplateView,
    Suppression,
    TemplateSummaryView,
    TemplateView,
    WebhookDeliveryView,
    WebhookView,
)

BASE = "http://test.local"


from datetime import datetime, timezone  # noqa: E402

_DT = datetime(2026, 6, 18, tzinfo=timezone.utc)
# Valid values for required fields (incl. enum-constrained ones) so a fixture is
# a real, deserializable server object rather than a stub.
_REQUIRED_DEFAULTS = {
    "created_at": _DT,
    "first_message_at": _DT,
    "last_message_at": _DT,
    "next_retry_at": _DT,
    "updated_at": _DT,
    "domain": "test.dev",
    "domain_verified": True,
    "email": "x@test.dev",
    "hitl_expiration_action": "reject",
    "hitl_ttl_seconds": 0,
    "id": "id_1",
    "inbound_policy": "open",
    "name": "n",
    "direction": "inbound",
    "var_from": "a@x.com",
    "labels": [],
    "message_id": "msg_1",
    "recipient": "bot@test.dev",
    "read_status": "unread",
    "subject": "s",
    "to": [],
}


def _dummy(ann):
    s = str(ann)
    if "bool" in s:
        return False
    if "int" in s:
        return 0
    if "float" in s:
        return 0.0
    if "List" in s or "list" in s:
        return []
    if "Dict" in s or "dict" in s or "Mapping" in s:
        return {}
    return ""


def _valid(model_cls, **overrides):
    """Construct a REAL model (valid by construction) and dump it to wire JSON —
    the Go server always returns full objects, so fixtures should too. Recurses
    into nested model fields (e.g. DomainView.dns_records) so deep views work."""
    from pydantic import BaseModel as _BaseModel

    kwargs = {}
    for name, field in model_cls.model_fields.items():
        if name in overrides:
            kwargs[name] = overrides[name]
        elif field.is_required():
            ann = field.annotation
            if isinstance(ann, type) and issubclass(ann, _BaseModel):
                kwargs[name] = _valid(ann)
            else:
                kwargs[name] = _REQUIRED_DEFAULTS.get(name, _dummy(field.annotation))
    inst = model_cls(**kwargs)
    return inst.model_dump(by_alias=True, mode="json")


@pytest.fixture(autouse=True)
def _clear_env(monkeypatch):
    for v in ("E2A_API_KEY", "E2A_BASE_URL", "E2A_AGENT_EMAIL"):
        monkeypatch.delenv(v, raising=False)


def _client():
    # No-sleep retry config so any retry path is instant.
    async def no_sleep(_s):
        return None

    return E2AClient(
        api_key="e2a_test", base_url=BASE, _retry_config=RetryConfig(sleep=no_sleep)
    )


# ── construction ────────────────────────────────────────────────────

def test_requires_api_key():
    with pytest.raises(E2AError, match="api_key is required"):
        E2AClient(base_url=BASE)


def test_resources_exposed():
    c = _client()
    for name in ("agents", "messages", "conversations", "domains", "events", "webhooks", "account", "reviews", "templates"):
        assert getattr(c, name) is not None
    assert c.account.suppressions is not None


@pytest.mark.parametrize(
    "timeout_ms, expected_s",
    [
        (None, 30.0),        # default
        (30_000.0, 30.0),    # explicit default
        (5_000.0, 5.0),      # custom
        (0, None),           # disabled → transport default applies
    ],
)
def test_timeout_ms_to_seconds(timeout_ms, expected_s):
    kwargs = {} if timeout_ms is None else {"timeout_ms": timeout_ms}
    c = E2AClient(api_key="e2a_test", base_url=BASE, **kwargs)
    assert c._timeout_s == expected_s


@pytest.mark.anyio
async def test_timeout_injected_into_transport(monkeypatch):
    # The real client wraps rest_client.request to inject _request_timeout when the
    # caller passes none. Patch the generated transport BEFORE construction so the
    # wrapper closes over our spy, then assert the default seconds value lands.
    from e2a.v1.generated import rest as _rest_mod

    seen = {}

    async def spy(self, method, url, headers=None, body=None, post_params=None, _request_timeout=None):
        seen["timeout"] = _request_timeout
        raise httpx.ConnectError("boom")  # short-circuit; we only inspect the arg

    monkeypatch.setattr(_rest_mod.RESTClientObject, "request", spy)
    c = E2AClient(api_key="e2a_test", base_url=BASE, timeout_ms=5_000.0)
    with pytest.raises(httpx.ConnectError):
        await c._api_client.rest_client.request("GET", "http://test.local/x")
    assert seen["timeout"] == 5.0


@pytest.mark.anyio
async def test_request_timeout_surfaces_as_connection_error(httpx_mock):
    # A transport timeout (httpx.TimeoutException family) must surface as a typed,
    # retryable connection error — same path as any connection-level failure.
    httpx_mock.add_exception(httpx.ReadTimeout("read timed out"))

    async def no_sleep(_s):
        return None

    c = E2AClient(
        api_key="e2a_test",
        base_url=BASE,
        timeout_ms=50.0,
        _retry_config=RetryConfig(max_retries=0, sleep=no_sleep),
    )
    async with c:
        with pytest.raises(E2AConnectionError):
            await c.agents.get("bot@test.dev")


# ── auth + agents ───────────────────────────────────────────────────

@pytest.mark.anyio
async def test_get_agent_sends_bearer_and_encodes_address(httpx_mock):
    httpx_mock.add_response(json=_valid(AgentView, id="ag_1", email="bot@test.dev"))
    async with _client() as c:
        agent = await c.agents.get("bot@test.dev")
    assert agent.email == "bot@test.dev"
    req = httpx_mock.get_requests()[-1]
    assert req.method == "GET"
    assert "/v1/agents/" in str(req.url)
    assert "bot%40test.dev" in str(req.url)
    assert req.headers["authorization"] == "Bearer e2a_test"


@pytest.mark.anyio
async def test_get_attachment_hits_endpoint_and_maps_view(httpx_mock):
    httpx_mock.add_response(
        json=_valid(
            AttachmentView,
            index=0,
            filename="report.pdf",
            content_type="application/pdf",
            size_bytes=14,
            download_url="https://api.test/d?token=tok",
            expires_at="2026-06-21T10:15:00Z",
        )
    )
    async with _client() as c:
        att = await c.messages.get_attachment("bot@test.dev", "msg_1", 0, inline=True)
    assert att.download_url == "https://api.test/d?token=tok"
    assert att.size_bytes == 14
    req = httpx_mock.get_requests()[-1]
    assert req.method == "GET"
    assert "/messages/msg_1/attachments/0" in str(req.url)
    assert "inline=true" in str(req.url)


@pytest.mark.anyio
async def test_webhooks_fetch_message_resolves_keys(httpx_mock):
    # email.received is metadata-only; fetch_message resolves (recipient,
    # message_id) into the full-message GET.
    httpx_mock.add_response(json=_valid(MessageView, message_id="msg_9", subject="Hi"))
    event = WebhookEvent(
        type="email.received",
        data={"message_id": "msg_9", "recipient": "bot@test.dev"},
        id="evt_1",
    )
    async with _client() as c:
        msg = await c.webhooks.fetch_message(event)
    assert msg.message_id == "msg_9"
    req = httpx_mock.get_requests()[-1]
    assert req.method == "GET"
    assert "/messages/msg_9" in str(req.url)
    assert "bot%40test.dev" in str(req.url)


@pytest.mark.anyio
async def test_webhooks_fetch_message_rejects_bad_event():
    async with _client() as c:
        with pytest.raises(ValueError, match="email.received"):
            await c.webhooks.fetch_message(
                WebhookEvent(type="email.bounced", data={"message_id": "m", "recipient": "r"})
            )
        with pytest.raises(ValueError, match="recipient"):
            await c.webhooks.fetch_message(
                WebhookEvent(type="email.received", data={"message_id": "m"})
            )


@pytest.mark.anyio
async def test_send_error_surfaces_machine_code(httpx_mock):
    # send/reply/forward now declare default: ErrorEnvelope (the custom Responses
    # map had suppressed Huma's auto default). The SDK must surface the machine
    # `code` for the send-family, not a generic/raw error. Guards §6a #4.
    httpx_mock.add_response(
        status_code=403,
        json={"error": {"code": "sending_not_verified", "message": "domain not verified", "request_id": "req_1"}},
    )
    async with _client() as c:
        with pytest.raises(E2APermissionError) as ei:
            await c.messages.send("bot@test.dev", {"to": ["a@x.com"], "subject": "Hi", "body": "Hello"})
    assert ei.value.code == "sending_not_verified"
    assert ei.value.status == 403


@pytest.mark.anyio
async def test_create_agent_posts_body(httpx_mock):
    httpx_mock.add_response(status_code=201, json=_valid(AgentView, id="ag_new", email="new@test.dev"))
    async with _client() as c:
        res = await c.agents.create({"email": "new@test.dev"})
    assert res.id == "ag_new"
    req = httpx_mock.get_requests()[-1]
    assert req.method == "POST"
    assert str(req.url).endswith("/v1/agents")


@pytest.mark.anyio
async def test_agents_list_autopager(httpx_mock):
    httpx_mock.add_response(
        json={"items": [_valid(AgentView, id="ag_1", email="bot@test.dev")], "next_cursor": None}
    )
    async with _client() as c:
        items = await c.agents.list().to_list(limit=10)
    assert [a.email for a in items] == ["bot@test.dev"]


# ── messages: idempotency + pagination ──────────────────────────────

@pytest.mark.anyio
async def test_send_mints_idempotency_key(httpx_mock):
    httpx_mock.add_response(json={"message_id": "msg_1", "status": "sent"})
    async with _client() as c:
        await c.messages.send("bot@test.dev", {"to": ["a@x.com"], "subject": "Hi", "body": "yo"})
    req = httpx_mock.get_requests()[-1]
    assert req.method == "POST"
    assert "/v1/agents/bot%40test.dev/messages" in str(req.url)
    assert req.headers.get("Idempotency-Key")


@pytest.mark.anyio
async def test_reviews_approve_hits_reviews_path_no_email(httpx_mock):
    # The review queue is account-scoped + id-addressed: approve(id) must hit
    # /v1/reviews/{id}/approve, NOT the deprecated /v1/agents/{email}/... path.
    httpx_mock.add_response(json={"message_id": "msg_r1", "status": "sent"})
    async with _client() as c:
        await c.reviews.approve("msg_r1")
    req = httpx_mock.get_requests()[-1]
    assert req.method == "POST"
    assert "/v1/reviews/msg_r1/approve" in str(req.url)
    assert "/agents/" not in str(req.url)
    assert req.headers.get("Idempotency-Key")


@pytest.mark.anyio
async def test_reviews_reject_hits_reviews_path(httpx_mock):
    httpx_mock.add_response(json={"message_id": "msg_r2", "status": "rejected"})
    async with _client() as c:
        await c.reviews.reject("msg_r2", {"reason": "spam"})
    req = httpx_mock.get_requests()[-1]
    assert req.method == "POST"
    assert "/v1/reviews/msg_r2/reject" in str(req.url)


@pytest.mark.anyio
async def test_reviews_list_reads_reviews_endpoint(httpx_mock):
    httpx_mock.add_response(
        json={"items": [_valid(ReviewView, id="msg_r1")], "next_cursor": None}
    )
    async with _client() as c:
        items = await c.reviews.list().to_list(limit=50)
    assert [r.id for r in items] == ["msg_r1"]
    req = httpx_mock.get_requests()[-1]
    assert req.method == "GET"
    assert "/v1/reviews" in str(req.url)


@pytest.mark.anyio
async def test_send_uses_caller_idempotency_key(httpx_mock):
    httpx_mock.add_response(json={"message_id": "msg_2", "status": "sent"})
    async with _client() as c:
        await c.messages.send(
            "bot@test.dev",
            {"to": ["a@x.com"], "subject": "Hi", "body": "yo"},
            idempotency_key="caller-key-123",
        )
    assert httpx_mock.get_requests()[-1].headers["Idempotency-Key"] == "caller-key-123"


@pytest.mark.anyio
async def test_messages_list_threads_cursor(httpx_mock):
    httpx_mock.add_response(json={"items": [_valid(MessageSummaryView, message_id="msg_1")], "next_cursor": "cur_2"})
    httpx_mock.add_response(json={"items": [_valid(MessageSummaryView, message_id="msg_2")], "next_cursor": None})
    async with _client() as c:
        items = await c.messages.list("bot@test.dev").to_list(limit=50)
    assert [m.message_id for m in items] == ["msg_1", "msg_2"]
    reqs = httpx_mock.get_requests()
    assert len(reqs) == 2
    assert "cursor=cur_2" in str(reqs[1].url)


# ── pagination: cursor-walking endpoints ────────────────────────────
# messages/conversations/events/suppressions take a `cursor` query param; the
# AutoPager must replay next_cursor until the server returns null, threading the
# cursor on each follow-up request. (messages is covered above.)


@pytest.mark.anyio
async def test_conversations_list_threads_cursor(httpx_mock):
    httpx_mock.add_response(
        json={"items": [_valid(ConversationSummaryView, conversation_id="conv_1")], "next_cursor": "cur_2"}
    )
    httpx_mock.add_response(
        json={"items": [_valid(ConversationSummaryView, conversation_id="conv_2")], "next_cursor": None}
    )
    async with _client() as c:
        items = await c.conversations.list("bot@test.dev").to_list(limit=50)
    assert [cv.conversation_id for cv in items] == ["conv_1", "conv_2"]
    reqs = httpx_mock.get_requests()
    assert len(reqs) == 2
    assert "cursor=cur_2" in str(reqs[1].url)


@pytest.mark.anyio
async def test_events_list_threads_cursor(httpx_mock):
    httpx_mock.add_response(json={"items": [_valid(EventJSON, id="evt_1")], "next_cursor": "cur_2"})
    httpx_mock.add_response(json={"items": [_valid(EventJSON, id="evt_2")], "next_cursor": None})
    async with _client() as c:
        items = await c.events.list().to_list(limit=50)
    assert [e.id for e in items] == ["evt_1", "evt_2"]
    reqs = httpx_mock.get_requests()
    assert len(reqs) == 2
    assert "cursor=cur_2" in str(reqs[1].url)


@pytest.mark.anyio
async def test_suppressions_list_threads_cursor(httpx_mock):
    httpx_mock.add_response(json={"items": [_valid(Suppression, address="a@x.com")], "next_cursor": "cur_2"})
    httpx_mock.add_response(json={"items": [_valid(Suppression, address="b@x.com")], "next_cursor": None})
    async with _client() as c:
        items = await c.account.suppressions.list().to_list(limit=50)
    assert [s.address for s in items] == ["a@x.com", "b@x.com"]
    reqs = httpx_mock.get_requests()
    assert len(reqs) == 2
    assert "cursor=cur_2" in str(reqs[1].url)


# ── pagination: cursorless (single-page) endpoints ──────────────────
# agents/domains/webhooks/deliveries have NO cursor query param. Even if the
# server hands back a non-null next_cursor, the pager must stop after exactly one
# page: it can't forward a cursor, and following it would re-fetch page 1 and
# trip the AutoPager cycle guard. This locks the intentional cursor-drop so a
# future "fix" that surfaces next_cursor (without a cursor param) is caught.


@pytest.mark.anyio
@pytest.mark.parametrize(
    "fixture, lister",
    [
        (
            {"items": [_valid(AgentView, id="ag_1", email="bot@test.dev")], "next_cursor": "IGNORE_ME"},
            lambda c: c.agents.list(),
        ),
        (
            {"items": [_valid(DomainView, domain="test.dev")], "next_cursor": "IGNORE_ME"},
            lambda c: c.domains.list(),
        ),
        (
            {"items": [_valid(WebhookView, id="wh_1")], "next_cursor": "IGNORE_ME"},
            lambda c: c.webhooks.list(),
        ),
        (
            {"items": [_valid(WebhookDeliveryView, id="del_1")], "next_cursor": "IGNORE_ME"},
            lambda c: c.webhooks.deliveries("wh_1"),
        ),
        (
            {"items": [_valid(TemplateSummaryView, id="tmpl_1")], "next_cursor": "IGNORE_ME"},
            lambda c: c.templates.list(),
        ),
        (
            {"items": [_valid(StarterTemplateView, alias="welcome")], "next_cursor": "IGNORE_ME"},
            lambda c: c.templates.list_starters(),
        ),
    ],
    ids=["agents", "domains", "webhooks", "deliveries", "templates", "starters"],
)
async def test_cursorless_lists_stop_after_one_page(httpx_mock, fixture, lister):
    httpx_mock.add_response(json=fixture)
    async with _client() as c:
        items = await lister(c).to_list(limit=100)
    assert len(items) == 1
    assert len(httpx_mock.get_requests()) == 1


# ── templates (beta) ────────────────────────────────────────────────
# Full parity with the TS SDK's TemplatesResource: the stored-template CRUD +
# validate dry-run + the read-only starter catalog. Python models are snake_case
# on both sides (no camel↔snake mapping), so wire keys equal field names.


@pytest.mark.anyio
async def test_templates_list_reads_templates_endpoint(httpx_mock):
    httpx_mock.add_response(
        json={"items": [_valid(TemplateSummaryView, id="tmpl_1", name="Welcome")], "next_cursor": None}
    )
    async with _client() as c:
        items = await c.templates.list().to_list(limit=50)
    assert [t.id for t in items] == ["tmpl_1"]
    req = httpx_mock.get_requests()[-1]
    assert req.method == "GET"
    assert str(req.url).endswith("/v1/templates")


@pytest.mark.anyio
async def test_templates_get_maps_wire_fields(httpx_mock):
    httpx_mock.add_response(
        json=_valid(
            TemplateView,
            id="tmpl_1",
            name="Welcome",
            subject="Welcome, {{name}}!",
            body="Hi {{name}}",
            html_body="<p>Hi {{name}}</p>",
            from_starter_alias="welcome",
            from_starter_version="1",
        )
    )
    async with _client() as c:
        tmpl = await c.templates.get("tmpl_1")
    assert tmpl.html_body == "<p>Hi {{name}}</p>"
    assert tmpl.from_starter_alias == "welcome"
    assert tmpl.from_starter_version == "1"
    req = httpx_mock.get_requests()[-1]
    assert req.method == "GET"
    assert "/v1/templates/tmpl_1" in str(req.url)


@pytest.mark.anyio
async def test_templates_create_posts_from_starter_body(httpx_mock):
    # Exactly the caller's fields reach the wire — no fabricated subject/body keys
    # that would trip the server's from_starter exclusivity.
    httpx_mock.add_response(
        status_code=201,
        json=_valid(TemplateView, id="tmpl_new", name="Approvals", subject="s", body="b"),
    )
    async with _client() as c:
        res = await c.templates.create({"from_starter": "approval-request", "alias": "my-approvals"})
    assert res.id == "tmpl_new"
    req = httpx_mock.get_requests()[-1]
    assert req.method == "POST"
    assert str(req.url).endswith("/v1/templates")
    import json as _json

    assert _json.loads(req.content) == {"from_starter": "approval-request", "alias": "my-approvals"}


@pytest.mark.anyio
async def test_templates_update_patches_and_keeps_explicit_html_clear(httpx_mock):
    httpx_mock.add_response(
        json=_valid(TemplateView, id="tmpl_1", name="Welcome", subject="New {{x}}", body="b")
    )
    async with _client() as c:
        await c.templates.update("tmpl_1", {"subject": "New {{x}}", "html_body": ""})
    req = httpx_mock.get_requests()[-1]
    assert req.method == "PATCH"
    assert "/v1/templates/tmpl_1" in str(req.url)
    import json as _json

    # An explicit html_body:"" is a deliberate clear — it must survive to the wire.
    assert _json.loads(req.content) == {"subject": "New {{x}}", "html_body": ""}


@pytest.mark.anyio
async def test_templates_delete_issues_delete(httpx_mock):
    httpx_mock.add_response(status_code=204)
    async with _client() as c:
        await c.templates.delete("tmpl_1")
    req = httpx_mock.get_requests()[-1]
    assert req.method == "DELETE"
    assert "/v1/templates/tmpl_1" in str(req.url)


@pytest.mark.anyio
async def test_templates_validate_posts_and_maps_response(httpx_mock):
    httpx_mock.add_response(
        json={
            "valid": True,
            "errors": [],
            "rendered": {"subject": "Welcome, Ada!", "body": "Hi Ada", "html_body": "<p>Hi Ada</p>"},
            "suggested_data": {"user": {"name": "example"}},
        }
    )
    async with _client() as c:
        res = await c.templates.validate(
            {"subject": "Welcome, {{user.name}}!", "body": "Hi {{user.name}}", "test_data": {"user": {"name": "Ada"}}}
        )
    assert res.valid is True
    assert res.rendered is not None and res.rendered.html_body == "<p>Hi Ada</p>"
    assert res.suggested_data == {"user": {"name": "example"}}
    req = httpx_mock.get_requests()[-1]
    assert req.method == "POST"
    assert "/v1/templates/validate" in str(req.url)
    import json as _json

    assert _json.loads(req.content) == {
        "subject": "Welcome, {{user.name}}!",
        "body": "Hi {{user.name}}",
        "test_data": {"user": {"name": "Ada"}},
    }


@pytest.mark.anyio
async def test_templates_get_starter_reads_starter_catalog(httpx_mock):
    httpx_mock.add_response(
        json={
            "alias": "approval-request",
            "name": "Approval request",
            "description": "Ask a human to approve an action.",
            "version": "1",
            "subject": "Approval needed: {{action}}",
            "body": "Approve: {{approve_url}}",
            "html_body": '<a href="{{approve_url}}">Approve</a>',
            "variables": [
                {"name": "approve_url", "required": True, "raw": False, "description": "d", "example": "https://x/approve"}
            ],
        }
    )
    async with _client() as c:
        starter = await c.templates.get_starter("approval-request")
    assert "{{approve_url}}" in starter.html_body
    assert starter.variables[0].name == "approve_url"
    req = httpx_mock.get_requests()[-1]
    assert req.method == "GET"
    assert "/v1/starter-templates/approval-request" in str(req.url)


@pytest.mark.anyio
async def test_templates_create_maps_parse_failure_to_validation_error(httpx_mock):
    # A template-part parse failure (400 invalid_template) must surface as a typed,
    # non-retryable E2AValidationError carrying the machine code.
    httpx_mock.add_response(
        status_code=400,
        json={"error": {"code": "invalid_template", "message": "template part body failed to parse"}},
    )
    async with _client() as c:
        with pytest.raises(E2AValidationError) as ei:
            await c.templates.create({"name": "x", "subject": "s", "body": "{{#bad}}"})
    assert ei.value.code == "invalid_template"
    assert ei.value.retryable is False


@pytest.mark.anyio
async def test_templates_round_trip_create_update_delete(httpx_mock):
    # A create→update→delete round-trip against the mocked generated layer,
    # asserting each hits the right method+path.
    httpx_mock.add_response(status_code=201, json=_valid(TemplateView, id="tmpl_rt", name="RT", subject="s", body="b"))
    httpx_mock.add_response(json=_valid(TemplateView, id="tmpl_rt", name="RT", subject="s2", body="b"))
    httpx_mock.add_response(status_code=204)
    async with _client() as c:
        created = await c.templates.create({"name": "RT", "subject": "s", "body": "b"})
        assert created.id == "tmpl_rt"
        updated = await c.templates.update("tmpl_rt", {"subject": "s2"})
        assert updated.subject == "s2"
        await c.templates.delete("tmpl_rt")
    methods = [(r.method, str(r.url)) for r in httpx_mock.get_requests()]
    assert methods[0][0] == "POST" and methods[0][1].endswith("/v1/templates")
    assert methods[1][0] == "PATCH" and "/v1/templates/tmpl_rt" in methods[1][1]
    assert methods[2][0] == "DELETE" and "/v1/templates/tmpl_rt" in methods[2][1]


def test_templates_view_types_exported_from_v1():
    # Parity with the TS SDK, which re-exports TemplateView et al. from its public
    # index. In Python these ride the `from generated.models import *` wildcard —
    # the same mechanism as AgentView/DomainView — so they must import from e2a.v1.
    from e2a.v1 import (  # noqa: F401
        CreateTemplateRequest,
        StarterTemplateDetailView,
        StarterTemplateView,
        TemplateSummaryView,
        TemplateView,
        UpdateTemplateRequest,
        ValidateTemplateRequest,
        ValidateTemplateResponse,
    )

    assert TemplateView is not None
    assert ValidateTemplateResponse is not None


# ── error mapping ───────────────────────────────────────────────────

@pytest.mark.anyio
async def test_404_maps_to_not_found(httpx_mock):
    httpx_mock.add_response(
        status_code=404, json={"error": {"code": "agent_not_found", "message": "no such agent"}}
    )
    async with _client() as c:
        with pytest.raises(E2ANotFoundError) as ei:
            await c.agents.get("ghost@test.dev")
    assert ei.value.code == "agent_not_found"
    assert ei.value.status == 404


@pytest.mark.anyio
async def test_409_maps_to_conflict(httpx_mock):
    httpx_mock.add_response(
        status_code=409, json={"error": {"code": "domain_exists", "message": "dup"}}
    )
    async with _client() as c:
        with pytest.raises(E2AConflictError):
            await c.domains.create({"domain": "dup.dev"})


@pytest.mark.anyio
async def test_422_maps_to_validation(httpx_mock):
    httpx_mock.add_response(
        status_code=422, json={"error": {"code": "invalid_request", "message": "bad"}}
    )
    async with _client() as c:
        with pytest.raises(E2AValidationError):
            # A coercible body so the request reaches the server, which 422s.
            await c.agents.create({"email": "x@test.dev"})


@pytest.mark.anyio
async def test_retries_500_then_succeeds(httpx_mock):
    httpx_mock.add_response(status_code=503, json={"error": {"code": "x", "message": "down"}})
    httpx_mock.add_response(json=_valid(AgentView, id="ag_1", email="bot@test.dev"))
    async with _client() as c:
        agent = await c.agents.get("bot@test.dev")  # GET is retryable
    assert agent.email == "bot@test.dev"
    assert len(httpx_mock.get_requests()) == 2


# ── listen() ────────────────────────────────────────────────────────

def test_listen_requires_email():
    c = _client()
    with pytest.raises(E2AError, match="email is required"):
        c.listen("")


@pytest.mark.anyio
async def test_invalid_request_body_raises_typed_validation_error():
    # Regression: a bad body must raise E2AValidationError, not a raw pydantic
    # ValidationError (the SDK contract is "everything is an E2AError").
    async with _client() as c:
        with pytest.raises(E2AValidationError):
            await c.agents.create([1, 2, 3])

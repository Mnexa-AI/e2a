"""Unit tests for the namespaced async E2AClient (Slice 8c-2).

Mocks httpx (the layer the generated base uses) so these exercise the full
stack: namespaced resource -> generated *Api -> bearer auth -> retry layer ->
httpx -> envelope unwrap -> typed-error mapping.
"""

import pytest

from e2a.v1._retry import RetryConfig
from e2a.v1.client import E2AClient
from e2a.v1.errors import (
    E2AConflictError,
    E2AError,
    E2ANotFoundError,
    E2AValidationError,
)
from e2a.v1.generated.models import (
    AgentView,
    CreateAgentResponse,
    MessageSummaryView,
)

BASE = "http://test.local"


from datetime import datetime, timezone  # noqa: E402

_DT = datetime(2026, 6, 18, tzinfo=timezone.utc)
# Valid values for required fields (incl. enum-constrained ones) so a fixture is
# a real, deserializable server object rather than a stub.
_REQUIRED_DEFAULTS = {
    "created_at": _DT,
    "domain": "test.dev",
    "domain_verified": True,
    "email": "x@test.dev",
    "hitl_enabled": False,
    "hitl_expiration_action": "reject",
    "hitl_mode": "all",
    "hitl_ttl_seconds": 0,
    "id": "id_1",
    "inbound_policy": "open",
    "name": "n",
    "direction": "inbound",
    "var_from": "a@x.com",
    "labels": [],
    "message_id": "msg_1",
    "recipient": "bot@test.dev",
    "status": "unread",
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
    return ""


def _valid(model_cls, **overrides):
    """Construct a REAL model (valid by construction) and dump it to wire JSON —
    the Go server always returns full objects, so fixtures should too."""
    kwargs = {}
    for name, field in model_cls.model_fields.items():
        if name in overrides:
            kwargs[name] = overrides[name]
        elif field.is_required():
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
    for name in ("agents", "messages", "conversations", "domains", "events", "webhooks", "account"):
        assert getattr(c, name) is not None
    assert c.account.suppressions is not None


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
async def test_create_agent_posts_body(httpx_mock):
    httpx_mock.add_response(status_code=201, json=_valid(CreateAgentResponse, id="ag_new", email="new@test.dev"))
    async with _client() as c:
        res = await c.agents.create({"slug": "new", "agent_mode": "drive"})
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
            await c.agents.create({"slug": ""})


@pytest.mark.anyio
async def test_retries_500_then_succeeds(httpx_mock):
    httpx_mock.add_response(status_code=503, json={"error": {"code": "x", "message": "down"}})
    httpx_mock.add_response(json=_valid(AgentView, id="ag_1", email="bot@test.dev"))
    async with _client() as c:
        agent = await c.agents.get("bot@test.dev")  # GET is retryable
    assert agent.email == "bot@test.dev"
    assert len(httpx_mock.get_requests()) == 2


# ── listen() ────────────────────────────────────────────────────────

def test_listen_requires_address():
    c = _client()
    with pytest.raises(E2AError, match="address is required"):
        c.listen()


@pytest.mark.anyio
async def test_invalid_request_body_raises_typed_validation_error():
    # Regression: a bad body must raise E2AValidationError, not a raw pydantic
    # ValidationError (the SDK contract is "everything is an E2AError").
    async with _client() as c:
        with pytest.raises(E2AValidationError):
            await c.agents.create([1, 2, 3])

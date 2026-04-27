"""Tests for the raw E2AApi v1 client."""

import json

import pytest

from e2a.v1.api import E2AApi, E2AApiError, _encode_email
from e2a.v1.generated import (
    Agent,
    ApprovePendingMessageRequest,
    ApprovePendingMessageResponse,
    Domain,
    ListAgentsResponse,
    ListDomainsResponse,
    ListMessagesResponse,
    ListPendingMessagesResponse,
    MessageDetail,
    PendingMessageDetail,
    RegisterAgentRequest,
    RegisterAgentResponse,
    RegisterDomainRequest,
    RejectPendingMessageResponse,
    ReplyToMessageRequest,
    SendEmailRequest,
    SendEmailResponse,
    UpdateAgentRequest,
    VerifyDomainResponse,
)


BASE = "https://e2a.dev"


# ── URL encoding ──────────────────────────────────────────────────


def test_encode_email_encodes_at_sign():
    assert _encode_email("bot@agents.e2a.dev") == "bot%40agents.e2a.dev"


def test_encode_email_encodes_plus():
    assert _encode_email("bot+tag@agents.e2a.dev") == "bot%2Btag%40agents.e2a.dev"


# ── Auth ──────────────────────────────────────────────────────────


def test_auth_header_uses_resolved_api_key(httpx_mock):
    """The Bearer token should use self.api_key (with env fallback), not the raw ctor arg."""
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="GET",
        json={"agents": []},
    )

    with E2AApi(api_key="e2a_mykey") as api:
        api.list_agents()

    req = httpx_mock.get_request()
    assert req.headers["Authorization"] == "Bearer e2a_mykey"


def test_auth_from_env(monkeypatch, httpx_mock):
    monkeypatch.setenv("E2A_API_KEY", "e2a_from_env")
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="GET",
        json={"agents": []},
    )

    with E2AApi() as api:
        assert api.api_key == "e2a_from_env"
        api.list_agents()

    req = httpx_mock.get_request()
    assert req.headers["Authorization"] == "Bearer e2a_from_env"


def test_explicit_key_overrides_env(monkeypatch):
    monkeypatch.setenv("E2A_API_KEY", "e2a_env")
    api = E2AApi(api_key="e2a_explicit")
    assert api.api_key == "e2a_explicit"
    api.close()


def test_custom_base_url(httpx_mock):
    httpx_mock.add_response(
        url="http://localhost:8080/api/v1/agents",
        method="GET",
        json={"agents": []},
    )

    with E2AApi(api_key="k", base_url="http://localhost:8080") as api:
        api.list_agents()


# ── Error handling ────────────────────────────────────────────────


def test_error_raises_e2a_api_error(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="GET",
        status_code=401,
        text="unauthorized",
    )

    with E2AApi(api_key="bad") as api:
        with pytest.raises(E2AApiError) as exc:
            api.list_agents()

    assert exc.value.status_code == 401
    assert "unauthorized" in exc.value.message


def test_404_error(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev",
        method="GET",
        status_code=404,
        text="agent not found",
    )

    with E2AApi(api_key="k") as api:
        with pytest.raises(E2AApiError) as exc:
            api.get_agent("bot@agents.e2a.dev")

    assert exc.value.status_code == 404


# ── Agents CRUD ───────────────────────────────────────────────────


def test_list_agents(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="GET",
        json={
            "agents": [
                {"email": "bot@agents.e2a.dev", "id": "ag_1", "agent_mode": "local"},
            ]
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.list_agents()

    assert isinstance(result, ListAgentsResponse)
    assert len(result.agents) == 1
    assert result.agents[0].email == "bot@agents.e2a.dev"


def test_register_agent(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="POST",
        json={"id": "ag_new", "email": "new@agents.e2a.dev", "domain": "agents.e2a.dev"},
    )

    with E2AApi(api_key="k") as api:
        result = api.register_agent(RegisterAgentRequest(slug="new"))

    assert isinstance(result, RegisterAgentResponse)
    assert result.email == "new@agents.e2a.dev"

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"slug": "new"}


def test_get_agent(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev",
        method="GET",
        json={"email": "bot@agents.e2a.dev", "id": "ag_1", "agent_mode": "local"},
    )

    with E2AApi(api_key="k") as api:
        result = api.get_agent("bot@agents.e2a.dev")

    assert isinstance(result, Agent)
    assert result.email == "bot@agents.e2a.dev"


def test_delete_agent(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev",
        method="DELETE",
        status_code=204,
        text="",
    )

    with E2AApi(api_key="k") as api:
        api.delete_agent("bot@agents.e2a.dev")  # should not raise


# ── Domains CRUD ──────────────────────────────────────────────────


def test_list_domains(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/domains",
        method="GET",
        json={
            "domains": [
                {"domain": "mycompany.com", "verified": True},
                {"domain": "staging.dev", "verified": False},
            ]
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.list_domains()

    assert isinstance(result, ListDomainsResponse)
    assert len(result.domains) == 2
    assert result.domains[0].domain == "mycompany.com"
    assert result.domains[0].verified is True


def test_register_domain(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/domains",
        method="POST",
        json={
            "domain": "mycompany.com",
            "dns_records": {
                "mx": {"host": "mycompany.com", "value": "mx.e2a.dev", "priority": 10},
                "txt": {"host": "mycompany.com", "value": "e2a-verify=abc123"},
            },
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.register_domain(RegisterDomainRequest(domain="mycompany.com"))

    assert isinstance(result, Domain)
    assert result.domain == "mycompany.com"
    assert result.dns_records.mx.value == "mx.e2a.dev"

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"domain": "mycompany.com"}


def test_verify_domain(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/domains/mycompany.com/verify",
        method="POST",
        json={"domain": "mycompany.com", "verified": True, "verified_at": "2026-03-30T00:00:00Z"},
    )

    with E2AApi(api_key="k") as api:
        result = api.verify_domain("mycompany.com")

    assert isinstance(result, VerifyDomainResponse)
    assert result.verified is True


def test_delete_domain(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/domains/mycompany.com",
        method="DELETE",
        status_code=204,
        text="",
    )

    with E2AApi(api_key="k") as api:
        api.delete_domain("mycompany.com")


# ── Messages ──────────────────────────────────────────────────────


def test_list_messages(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages?status=unread&page_size=50",
        method="GET",
        json={
            "messages": [
                {
                    "message_id": "msg_1",
                    "from": "alice@example.com",
                    "to": ["bot@agents.e2a.dev"],
                    "recipient": "bot@agents.e2a.dev",
                    "subject": "Hello",
                    "status": "unread",
                    "created_at": "2026-03-30T10:00:00Z",
                }
            ],
            "next_token": "tok_abc",
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.list_messages("bot@agents.e2a.dev")

    assert isinstance(result, ListMessagesResponse)
    assert len(result.messages) == 1
    assert result.messages[0].message_id == "msg_1"
    assert result.messages[0].from_ == "alice@example.com"
    assert result.next_token == "tok_abc"


def test_list_messages_with_params(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages?status=all&page_size=10&token=abc",
        method="GET",
        json={"messages": []},
    )

    with E2AApi(api_key="k") as api:
        result = api.list_messages("bot@agents.e2a.dev", status="all", page_size=10, token="abc")

    assert result.messages == []


def test_list_messages_url_encodes_email(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%2Btag%40agents.e2a.dev/messages?status=unread&page_size=50",
        method="GET",
        json={"messages": []},
    )

    with E2AApi(api_key="k") as api:
        api.list_messages("bot+tag@agents.e2a.dev")


def test_get_message(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123",
        method="GET",
        json={
            "message_id": "msg_123",
            "from": "alice@example.com",
            "to": ["bot@agents.e2a.dev"],
            "recipient": "bot@agents.e2a.dev",
            "subject": "Hello",
            "created_at": "2026-03-30T10:00:00Z",
            "raw_message": "U3ViamVjdDogSGVsbG8=",
            "auth_headers": {"X-E2A-Auth-Verified": "true"},
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.get_message("bot@agents.e2a.dev", "msg_123")

    assert isinstance(result, MessageDetail)
    assert result.message_id == "msg_123"
    assert result.from_ == "alice@example.com"
    assert result.subject == "Hello"
    assert result.raw_message is not None


def test_reply_to_message(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "reply_456", "method": "smtp"},
    )

    with E2AApi(api_key="k") as api:
        result = api.reply_to_message(
            "bot@agents.e2a.dev",
            "msg_123",
            ReplyToMessageRequest(body="Thanks!"),
        )

    assert isinstance(result, SendEmailResponse)
    assert result.status == "sent"
    assert result.message_id == "reply_456"

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"body": "Thanks!"}


def test_reply_with_html_and_conversation(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "reply_789", "method": "smtp"},
    )

    with E2AApi(api_key="k") as api:
        api.reply_to_message(
            "bot@agents.e2a.dev",
            "msg_123",
            ReplyToMessageRequest(
                body="Thanks!",
                html_body="<p>Thanks!</p>",
                conversation_id="conv_abc",
            ),
        )

    body = json.loads(httpx_mock.get_request().content)
    assert body == {
        "body": "Thanks!",
        "html_body": "<p>Thanks!</p>",
        "conversation_id": "conv_abc",
    }


def test_send_email(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "send_abc", "method": "webhook"},
    )

    with E2AApi(api_key="k") as api:
        result = api.send_email(
            SendEmailRequest(
                to=["alice@example.com"],
                subject="Hello",
                body="Hi Alice",
                from_="bot@agents.e2a.dev",
            )
        )

    assert isinstance(result, SendEmailResponse)
    assert result.method == "webhook"

    body = json.loads(httpx_mock.get_request().content)
    # The "from" alias must serialize correctly
    assert body["from"] == "bot@agents.e2a.dev"
    assert body["to"] == ["alice@example.com"]
    assert body["subject"] == "Hello"
    assert body["body"] == "Hi Alice"


def test_send_email_from_alias_serialization(httpx_mock):
    """Verify that from_ serializes as 'from' on the wire (alias handling)."""
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "s1", "method": "smtp"},
    )

    with E2AApi(api_key="k") as api:
        api.send_email(
            SendEmailRequest(from_="bot@agents.e2a.dev", to=["x@y.com"], subject="S", body="B")
        )

    body = json.loads(httpx_mock.get_request().content)
    assert "from" in body
    assert "from_" not in body


# ── Paths use /api/v1/ ────────────────────────────────────────────


def test_all_paths_use_v1(httpx_mock):
    """Ensure no request uses the legacy unversioned /api/ path."""
    httpx_mock.add_response(json={"agents": []})

    with E2AApi(api_key="k") as api:
        api.list_agents()

    req = httpx_mock.get_request()
    assert "/api/v1/" in str(req.url)


# ── HITL (human-in-the-loop approval) ───────────────────────────


def test_update_agent_puts_the_body(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev",
        method="PUT",
        json={
            "email": "bot@agents.e2a.dev",
            "id": "ag_1",
            "agent_mode": "local",
            "hitl_enabled": True,
            "hitl_ttl_seconds": 3600,
            "hitl_expiration_action": "reject",
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.update_agent(
            "bot@agents.e2a.dev",
            UpdateAgentRequest(
                hitl_enabled=True,
                hitl_ttl_seconds=3600,
                hitl_expiration_action="reject",
            ),
        )

    assert isinstance(result, Agent)
    assert result.hitl_enabled is True
    body = json.loads(httpx_mock.get_request().content)
    assert body == {
        "hitl_enabled": True,
        "hitl_ttl_seconds": 3600,
        "hitl_expiration_action": "reject",
    }


def test_list_pending_messages_sends_filter(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/messages?status=pending_approval",
        method="GET",
        json={
            "messages": [
                {
                    "id": "msg_p1",
                    "agent_id": "bot@agents.e2a.dev",
                    "subject": "held",
                    "to": ["alice@example.com"],
                    "status": "pending_approval",
                    "direction": "outbound",
                    "created_at": "2026-01-15T10:00:00Z",
                }
            ]
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.list_pending_messages()

    assert isinstance(result, ListPendingMessagesResponse)
    assert result.messages is not None
    assert len(result.messages) == 1


def test_get_pending_message_returns_detail(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/messages/msg_x",
        method="GET",
        json={
            "id": "msg_x",
            "agent_id": "bot@agents.e2a.dev",
            "subject": "held",
            "to": ["alice@example.com"],
            "status": "pending_approval",
            "direction": "outbound",
            "body_text": "preview body",
            "created_at": "2026-01-15T10:00:00Z",
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.get_pending_message("msg_x")

    assert isinstance(result, PendingMessageDetail)
    assert result.body_text == "preview body"


def test_approve_message_without_overrides_posts_empty_body(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/messages/msg_x/approve",
        method="POST",
        json={
            "status": "sent",
            "message_id": "msg_x",
            "provider_message_id": "<ses@amazonses.com>",
            "method": "smtp",
            "edited": False,
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.approve_message("msg_x")

    assert isinstance(result, ApprovePendingMessageResponse)
    assert result.status == "sent"
    body = json.loads(httpx_mock.get_request().content)
    assert body == {}


def test_approve_message_with_overrides_forwards_them(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/messages/msg_x/approve",
        method="POST",
        json={"status": "sent", "message_id": "msg_x", "edited": True},
    )

    with E2AApi(api_key="k") as api:
        api.approve_message(
            "msg_x",
            ApprovePendingMessageRequest(subject="edited", to=["bob@example.com"]),
        )

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"subject": "edited", "to": ["bob@example.com"]}


def test_reject_message_sends_reason(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/messages/msg_x/reject",
        method="POST",
        json={
            "status": "rejected",
            "message_id": "msg_x",
            "rejection_reason": "bad tone",
        },
    )

    with E2AApi(api_key="k") as api:
        result = api.reject_message("msg_x", "bad tone")

    assert isinstance(result, RejectPendingMessageResponse)
    assert result.rejection_reason == "bad tone"
    body = json.loads(httpx_mock.get_request().content)
    assert body == {"reason": "bad tone"}


def test_reject_message_defaults_to_empty_reason(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/messages/msg_x/reject",
        method="POST",
        json={"status": "rejected", "message_id": "msg_x"},
    )

    with E2AApi(api_key="k") as api:
        api.reject_message("msg_x")

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"reason": ""}


# ── Context manager ──────────────────────────────────────────────


def test_context_manager():
    api = E2AApi(api_key="k")
    with api:
        pass
    # Should not raise after close


# ── Constructor strictness ───────────────────────────────────────


def test_e2aapi_requires_api_key(monkeypatch):
    """Match TS: construction fails fast when no key is passed AND no
    E2A_API_KEY env var is set. Better than silently making 401-ing requests.
    """
    monkeypatch.delenv("E2A_API_KEY", raising=False)
    with pytest.raises(ValueError, match="api_key is required"):
        E2AApi(api_key=None)
    with pytest.raises(ValueError, match="api_key is required"):
        E2AApi(api_key="")


def test_e2aapi_uses_env_var_when_no_arg(monkeypatch):
    monkeypatch.setenv("E2A_API_KEY", "k_from_env")
    api = E2AApi()
    assert api.api_key == "k_from_env"


# ── get_info / fetch_info ────────────────────────────────────────


def test_get_info(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/info",
        method="GET",
        json={"shared_domain": "agents.example.com", "slug_registration_enabled": True},
    )
    with E2AApi(api_key="k", base_url=BASE) as api:
        info = api.get_info()
    assert info.shared_domain == "agents.example.com"
    assert info.slug_registration_enabled is True


def test_fetch_info_module_level(httpx_mock):
    """Discovery flow before login — no API key required."""
    from e2a.v1.api import fetch_info
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/info",
        method="GET",
        json={"shared_domain": "agents.example.com", "public_url": "https://e2a.example.com"},
    )
    info = fetch_info(base_url=BASE)
    assert info.shared_domain == "agents.example.com"
    assert info.public_url == "https://e2a.example.com"

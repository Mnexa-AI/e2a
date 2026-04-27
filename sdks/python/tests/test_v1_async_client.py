"""Tests for e2a.v1.async_client — AsyncE2AApi and AsyncE2AClient."""

import base64
import json

import pytest

from e2a.v1.async_client import AsyncE2AClient
from e2a.v1.handler import AsyncInboundEmail, Attachment, MessageList, SendResult


BASE = "https://e2a.dev"


# ── Helpers ───────────────────────────────────────────────────────


def _make_raw_email(subject="Hello", text="Hi there"):
    lines = [
        "From: alice@example.com",
        "To: bot@agents.e2a.dev",
        f"Subject: {subject}",
        "Content-Type: text/plain; charset=utf-8",
        "",
        text,
    ]
    return "\r\n".join(lines).encode()


def _make_message_detail_json(message_id="msg_123", raw=None):
    if raw is None:
        raw = _make_raw_email()
    return {
        "message_id": message_id,
        "from": "alice@example.com",
        "to": ["bot@agents.e2a.dev"], "recipient": "bot@agents.e2a.dev",
        "subject": "Hello",
        "conversation_id": "conv_abc",
        "status": "read",
        "created_at": "2026-03-30T10:00:00Z",
        "auth_headers": {
            "X-E2A-Auth-Verified": "true",
            "X-E2A-Auth-Sender": "alice@example.com",
            "X-E2A-Auth-Entity-Type": "human",
        },
        "raw_message": base64.b64encode(raw).decode(),
    }


# ── parse() ──────────────────────────────────────────────────────


@pytest.mark.anyio
async def test_parse_bytes(httpx_mock):
    webhook = _make_message_detail_json()
    body = json.dumps(webhook).encode()

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse(body)

    assert isinstance(email, AsyncInboundEmail)
    assert email.message_id == "msg_123"
    assert email.sender == "alice@example.com"
    assert email.text_body == "Hi there"


@pytest.mark.anyio
async def test_parse_string(httpx_mock):
    webhook = _make_message_detail_json()
    body = json.dumps(webhook)

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse(body)

    assert email.message_id == "msg_123"


@pytest.mark.anyio
async def test_parse_dict(httpx_mock):
    webhook = _make_message_detail_json()

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse(webhook)

    assert email.message_id == "msg_123"
    assert email.sender == "alice@example.com"


@pytest.mark.anyio
async def test_parse_message_detail(httpx_mock):
    from e2a.v1.generated import MessageDetail

    raw = _make_raw_email()
    detail = MessageDetail(
        message_id="msg_456",
        from_="bob@example.com",
        to=["bot@agents.e2a.dev"], recipient="bot@agents.e2a.dev",
        subject="Test",
        created_at="2026-03-30T12:00:00Z",
        raw_message=base64.b64encode(raw).decode(),
        auth_headers={"X-E2A-Auth-Verified": "false"},
    )

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse(detail)

    assert email.message_id == "msg_456"
    assert email.sender == "bob@example.com"
    assert email.received_at == "2026-03-30T12:00:00Z"
    assert email.is_verified is False


@pytest.mark.anyio
async def test_parse_unsupported_type():
    async with AsyncE2AClient(api_key="k") as client:
        with pytest.raises(TypeError, match="Unsupported body type"):
            client.parse(12345)


# ── get_message() ────────────────────────────────────────────────


@pytest.mark.anyio
async def test_get_message(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123",
        method="GET",
        json=_make_message_detail_json(),
    )

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = await client.get_message("msg_123")

    assert isinstance(email, AsyncInboundEmail)
    assert email.message_id == "msg_123"
    assert email.sender == "alice@example.com"
    assert email.subject == "Hello"
    assert email.text_body == "Hi there"
    assert email.conversation_id == "conv_abc"
    assert email.is_verified is True
    assert email.received_at == "2026-03-30T10:00:00Z"


# ── get_messages() ───────────────────────────────────────────────


@pytest.mark.anyio
async def test_get_messages(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages?status=unread&page_size=50",
        method="GET",
        json={
            "messages": [
                {
                    "message_id": "msg_1",
                    "from": "alice@example.com",
                    "to": ["bot@agents.e2a.dev"], "recipient": "bot@agents.e2a.dev",
                    "subject": "Hello",
                    "status": "unread",
                    "created_at": "2026-03-30T10:00:00Z",
                    "conversation_id": "conv_1",
                },
            ],
            "next_token": "tok_abc",
        },
    )

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        result = await client.get_messages()

    assert isinstance(result, MessageList)
    assert len(result.messages) == 1
    assert result.messages[0].sender == "alice@example.com"
    assert result.next_token == "tok_abc"


# ── reply() ──────────────────────────────────────────────────────


@pytest.mark.anyio
async def test_reply(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "reply_456", "method": "smtp"},
    )

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        result = await client.reply("msg_123", "Thanks!")

    assert isinstance(result, SendResult)
    assert result.status == "sent"

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"body": "Thanks!"}


@pytest.mark.anyio
async def test_reply_with_attachments(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "r1", "method": "smtp"},
    )

    att = Attachment(filename="doc.pdf", content_type="application/pdf", data=b"pdf-data", size=8)

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        await client.reply("msg_123", "See attached", attachments=[att])

    body = json.loads(httpx_mock.get_request().content)
    assert body["attachments"][0]["filename"] == "doc.pdf"


# ── send() ───────────────────────────────────────────────────────


@pytest.mark.anyio
async def test_send(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "send_abc", "method": "smtp"},
    )

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        result = await client.send(["alice@example.com"], "Hello", "Hi Alice")

    assert isinstance(result, SendResult)
    body = json.loads(httpx_mock.get_request().content)
    assert body["from"] == "bot@agents.e2a.dev"


@pytest.mark.anyio
async def test_send_with_attachments(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "s1", "method": "smtp"},
    )

    att = Attachment(filename="img.png", content_type="image/png", data=b"png-data", size=8)

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        await client.send(["alice@example.com"], "Photo", "See attached", attachments=[att])

    body = json.loads(httpx_mock.get_request().content)
    assert body["attachments"][0]["data"] == base64.b64encode(b"png-data").decode()


# ── Agent CRUD ───────────────────────────────────────────────────


@pytest.mark.anyio
async def test_register_agent_slug(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="POST",
        json={"id": "ag_new", "email": "new@agents.e2a.dev"},
    )

    async with AsyncE2AClient(api_key="k") as client:
        await client.register_agent("new")

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"slug": "new"}


@pytest.mark.anyio
async def test_register_agent_custom_domain(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="POST",
        json={"id": "ag_cd", "email": "support@mycompany.com"},
    )

    async with AsyncE2AClient(api_key="k") as client:
        await client.register_agent(email="support@mycompany.com", agent_mode="cloud", webhook_url="https://mycompany.com/webhook")

    body = json.loads(httpx_mock.get_request().content)
    assert body["email"] == "support@mycompany.com"
    assert body["agent_mode"] == "cloud"


# ── Env fallback ─────────────────────────────────────────────────


@pytest.mark.anyio
async def test_env_fallback(monkeypatch):
    monkeypatch.setenv("E2A_API_KEY", "e2a_from_env")
    monkeypatch.setenv("E2A_AGENT_EMAIL", "env@agents.e2a.dev")

    async with AsyncE2AClient() as client:
        assert client.api.api_key == "e2a_from_env"
        assert client.agent_email == "env@agents.e2a.dev"


@pytest.mark.anyio
async def test_explicit_overrides_env(monkeypatch):
    monkeypatch.setenv("E2A_API_KEY", "e2a_env")

    async with AsyncE2AClient(api_key="explicit", agent_email="explicit@agents.e2a.dev") as client:
        assert client.api.api_key == "explicit"
        assert client.agent_email == "explicit@agents.e2a.dev"


# ── _require_agent_email ─────────────────────────────────────────


@pytest.mark.anyio
async def test_require_agent_email_raises(monkeypatch):
    monkeypatch.delenv("E2A_AGENT_EMAIL", raising=False)

    async with AsyncE2AClient(api_key="k") as client:
        with pytest.raises(ValueError, match="agent_email is required"):
            await client.get_messages()
        with pytest.raises(ValueError, match="agent_email is required"):
            await client.reply("msg_123", "Hello")
        with pytest.raises(ValueError, match="agent_email is required"):
            await client.send("alice@example.com", "Subject", "Body")


# ── AsyncInboundEmail.reply() through client ─────────────────────


@pytest.mark.anyio
async def test_async_inbound_email_reply(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123",
        method="GET",
        json=_make_message_detail_json(),
    )
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "reply_789", "method": "smtp"},
    )

    async with AsyncE2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = await client.get_message("msg_123")
        result = await email.reply("Got it!")

    assert result.status == "sent"
    assert result.message_id == "reply_789"


# ── Constructor strictness + discovery (parity with sync) ────────


def test_async_e2aapi_requires_api_key(monkeypatch):
    from e2a.v1.async_client import AsyncE2AApi
    monkeypatch.delenv("E2A_API_KEY", raising=False)
    with pytest.raises(ValueError, match="api_key is required"):
        AsyncE2AApi()


@pytest.mark.anyio
async def test_async_get_info(httpx_mock):
    from e2a.v1.async_client import AsyncE2AApi
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/info",
        method="GET",
        json={"shared_domain": "agents.example.com", "slug_registration_enabled": True},
    )
    async with AsyncE2AApi(api_key="k", base_url=BASE) as api:
        info = await api.get_info()
    assert info.shared_domain == "agents.example.com"


@pytest.mark.anyio
async def test_fetch_info_async_module_level(httpx_mock):
    from e2a.v1.async_client import fetch_info
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/info",
        method="GET",
        json={"shared_domain": "agents.example.com"},
    )
    info = await fetch_info(base_url=BASE)
    assert info.shared_domain == "agents.example.com"

"""Tests for e2a.v1.client — high-level sync E2AClient."""

import base64
import json

import pytest

from e2a.v1.client import E2AClient
from e2a.v1.handler import Attachment, InboundEmail, MessageList, SendResult


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


def test_parse_bytes(httpx_mock):
    webhook = _make_message_detail_json()
    body = json.dumps(webhook).encode()

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse(body)
        email._verified = True  # test fixture: bypass verify gate

    assert isinstance(email, InboundEmail)
    assert email.message_id == "msg_123"
    assert email.sender == "alice@example.com"
    assert email.text_body == "Hi there"


def test_parse_string(httpx_mock):
    webhook = _make_message_detail_json()
    body = json.dumps(webhook)

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse(body)
        email._verified = True  # test fixture: bypass verify gate

    assert email.message_id == "msg_123"


def test_parse_dict(httpx_mock):
    webhook = _make_message_detail_json()

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse(webhook)
        email._verified = True  # test fixture: bypass verify gate

    assert email.message_id == "msg_123"
    assert email.sender == "alice@example.com"


def test_parse_message_detail(httpx_mock):
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

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse(detail)
        email._verified = True  # test fixture: bypass verify gate

    assert email.message_id == "msg_456"
    assert email.sender == "bob@example.com"
    assert email.received_at == "2026-03-30T12:00:00Z"
    assert email.is_verified is False


def test_parse_unsupported_type():
    with E2AClient(api_key="k") as client:
        with pytest.raises(TypeError, match="Unsupported body type"):
            client.parse(12345)


# ── get_message() ────────────────────────────────────────────────


def test_get_message(httpx_mock):
    raw = _make_raw_email()
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123",
        method="GET",
        json=_make_message_detail_json(),
    )

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.get_message("msg_123")

    assert isinstance(email, InboundEmail)
    assert email.message_id == "msg_123"
    assert email.sender == "alice@example.com"
    assert email.subject == "Hello"
    assert email.text_body == "Hi there"
    assert email.conversation_id == "conv_abc"
    assert email.is_verified is True
    assert email.received_at == "2026-03-30T10:00:00Z"


# ── get_messages() ───────────────────────────────────────────────


def test_get_messages(httpx_mock):
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

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        result = client.get_messages()

    assert isinstance(result, MessageList)
    assert len(result.messages) == 1
    assert result.messages[0].message_id == "msg_1"
    assert result.messages[0].sender == "alice@example.com"
    assert result.messages[0].recipient == "bot@agents.e2a.dev"
    assert result.messages[0].status == "unread"
    assert result.next_token == "tok_abc"


def test_get_messages_empty(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages?status=unread&page_size=50",
        method="GET",
        json={"messages": []},
    )

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        result = client.get_messages()

    assert result.messages == []
    assert result.next_token is None


# ── reply() ──────────────────────────────────────────────────────


def test_reply(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "reply_456", "method": "smtp"},
    )

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        result = client.reply("msg_123", "Thanks!")

    assert isinstance(result, SendResult)
    assert result.status == "sent"
    assert result.message_id == "reply_456"

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"body": "Thanks!"}


def test_reply_with_html_and_conversation(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "r1", "method": "smtp"},
    )

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        client.reply("msg_123", "Thanks!", html_body="<p>Thanks!</p>", conversation_id="conv_1")

    body = json.loads(httpx_mock.get_request().content)
    assert body["body"] == "Thanks!"
    assert body["html_body"] == "<p>Thanks!</p>"
    assert body["conversation_id"] == "conv_1"


def test_reply_with_reply_all_cc_bcc(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "r1", "method": "smtp"},
    )

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        client.reply(
            "msg_123", "Thanks!",
            reply_all=True,
            cc=["bob@example.com"],
            bcc=["carol@example.com"],
        )

    body = json.loads(httpx_mock.get_request().content)
    assert body["reply_all"] is True
    assert body["cc"] == ["bob@example.com"]
    assert body["bcc"] == ["carol@example.com"]


def test_reply_with_attachments(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_123/reply",
        method="POST",
        json={"status": "sent", "message_id": "r1", "method": "smtp"},
    )

    att = Attachment(filename="doc.pdf", content_type="application/pdf", data=b"pdf-data", size=8)

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        client.reply("msg_123", "See attached", attachments=[att])

    body = json.loads(httpx_mock.get_request().content)
    assert len(body["attachments"]) == 1
    assert body["attachments"][0]["filename"] == "doc.pdf"
    assert body["attachments"][0]["data"] == base64.b64encode(b"pdf-data").decode()


# ── send() ───────────────────────────────────────────────────────


def test_send(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "send_abc", "method": "smtp"},
    )

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        result = client.send(["alice@example.com"], "Hello", "Hi Alice")

    assert isinstance(result, SendResult)
    assert result.status == "sent"

    body = json.loads(httpx_mock.get_request().content)
    assert body["from"] == "bot@agents.e2a.dev"
    assert body["to"] == ["alice@example.com"]
    assert body["subject"] == "Hello"
    assert body["body"] == "Hi Alice"


def test_send_with_cc_bcc_html(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "s1", "method": "smtp"},
    )

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        client.send(
            ["alice@example.com", "bob@example.com"],
            "Hello",
            "Hi",
            html_body="<p>Hi</p>",
            cc=["carol@example.com"],
            bcc=["dave@example.com"],
        )

    body = json.loads(httpx_mock.get_request().content)
    assert body["to"] == ["alice@example.com", "bob@example.com"]
    assert body["html_body"] == "<p>Hi</p>"
    assert body["cc"] == ["carol@example.com"]
    assert body["bcc"] == ["dave@example.com"]


def test_send_with_attachments(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "s1", "method": "smtp"},
    )

    att = Attachment(filename="img.png", content_type="image/png", data=b"png-data", size=8)

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        client.send(["alice@example.com"], "Photo", "See attached", attachments=[att])

    body = json.loads(httpx_mock.get_request().content)
    assert body["attachments"][0]["filename"] == "img.png"
    assert body["attachments"][0]["data"] == base64.b64encode(b"png-data").decode()


# ── Domain convenience methods ───────────────────────────────────


def test_list_domains(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/domains",
        method="GET",
        json={"domains": [{"domain": "mycompany.com", "verified": True}]},
    )

    with E2AClient(api_key="k") as client:
        result = client.list_domains()

    assert result.domains[0].domain == "mycompany.com"


def test_register_domain(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/domains",
        method="POST",
        json={"domain": "mycompany.com", "dns_records": {"mx": {"host": "@", "value": "mx.e2a.dev"}}},
    )

    with E2AClient(api_key="k") as client:
        result = client.register_domain("mycompany.com")

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"domain": "mycompany.com"}


def test_verify_domain(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/domains/mycompany.com/verify",
        method="POST",
        json={"domain": "mycompany.com", "verified": True},
    )

    with E2AClient(api_key="k") as client:
        result = client.verify_domain("mycompany.com")

    assert result.verified is True


def test_delete_domain(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/domains/mycompany.com",
        method="DELETE",
        status_code=204,
        text="",
    )

    with E2AClient(api_key="k") as client:
        client.delete_domain("mycompany.com")


# ── Agent convenience methods ────────────────────────────────────


def test_list_agents(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="GET",
        json={"agents": [{"email": "bot@agents.e2a.dev", "id": "ag_1"}]},
    )

    with E2AClient(api_key="k") as client:
        result = client.list_agents()

    assert result.agents[0].email == "bot@agents.e2a.dev"


def test_register_agent_slug_only(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="POST",
        json={"id": "ag_new", "email": "new@agents.e2a.dev"},
    )

    with E2AClient(api_key="k") as client:
        result = client.register_agent("new")

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"slug": "new"}


def test_register_agent_custom_domain(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents",
        method="POST",
        json={"id": "ag_cd", "email": "support@mycompany.com"},
    )

    with E2AClient(api_key="k") as client:
        result = client.register_agent(email="support@mycompany.com", agent_mode="cloud", webhook_url="https://mycompany.com/webhook")

    body = json.loads(httpx_mock.get_request().content)
    assert body == {"email": "support@mycompany.com", "agent_mode": "cloud", "webhook_url": "https://mycompany.com/webhook"}


# ── Env fallback ─────────────────────────────────────────────────


def test_env_fallback(monkeypatch):
    monkeypatch.setenv("E2A_API_KEY", "e2a_from_env")
    monkeypatch.setenv("E2A_AGENT_EMAIL", "env@agents.e2a.dev")

    client = E2AClient()
    assert client.api.api_key == "e2a_from_env"
    assert client.agent_email == "env@agents.e2a.dev"
    client.close()


def test_explicit_overrides_env(monkeypatch):
    monkeypatch.setenv("E2A_API_KEY", "e2a_env")
    monkeypatch.setenv("E2A_AGENT_EMAIL", "env@agents.e2a.dev")

    client = E2AClient(api_key="explicit", agent_email="explicit@agents.e2a.dev")
    assert client.api.api_key == "explicit"
    assert client.agent_email == "explicit@agents.e2a.dev"
    client.close()


# ── _require_agent_email ─────────────────────────────────────────


def test_require_agent_email_raises(monkeypatch):
    monkeypatch.delenv("E2A_AGENT_EMAIL", raising=False)

    with E2AClient(api_key="k") as client:
        with pytest.raises(ValueError, match="agent_email is required"):
            client.get_messages()
        with pytest.raises(ValueError, match="agent_email is required"):
            client.reply("msg_123", "Hello")
        with pytest.raises(ValueError, match="agent_email is required"):
            client.send("alice@example.com", "Subject", "Body")


# ── InboundEmail.reply() through client ──────────────────────────


def test_inbound_email_reply_through_client(httpx_mock):
    raw = _make_raw_email()
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

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.get_message("msg_123")
        result = email.reply("Got it!")

    assert result.status == "sent"
    assert result.message_id == "reply_789"

    # Verify it used the recipient as agent_email in the reply URL
    requests = httpx_mock.get_requests()
    reply_req = requests[1]
    assert "/agents/bot%40agents.e2a.dev/messages/msg_123/reply" in str(reply_req.url)


# --- parse_webhook (combined parse + verify) ---

def test_parse_webhook_returns_verified_email_on_success(monkeypatch):
    """parse_webhook(body, secret) returns an already-verified email."""
    import e2a.v1.handler as h
    monkeypatch.setattr(h, "_verify_auth_headers", lambda *a, **kw: True)

    raw = b"From: alice@gmail.com\r\nSubject: Hi\r\n\r\nbody"
    payload = json.dumps({
        "message_id": "msg_pw",
        "from": "alice@gmail.com",
        "recipient": "bot@agents.e2a.dev",
        "to": ["bot@agents.e2a.dev"],
        "cc": [],
        "subject": "Hi",
        "raw_message": base64.b64encode(raw).decode(),
        "auth_headers": {"X-E2A-Auth-Verified": "true", "X-E2A-Auth-Sender": "alice@gmail.com"},
    })

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        email = client.parse_webhook(payload, secret="any-secret")

    assert email.verified is True
    # Field access works without further intervention.
    assert email.sender == "alice@gmail.com"


def test_parse_webhook_raises_on_signature_failure(monkeypatch):
    """parse_webhook raises PermissionError when verify returns False."""
    import e2a.v1.handler as h
    monkeypatch.setattr(h, "_verify_auth_headers", lambda *a, **kw: False)

    payload = json.dumps({
        "message_id": "msg_bad",
        "from": "a@b.c",
        "recipient": "bot@agents.e2a.dev",
        "to": ["bot@agents.e2a.dev"], "cc": [],
        "subject": "x",
        "raw_message": base64.b64encode(b"x").decode(),
        "auth_headers": {"X-E2A-Auth-Verified": "true"},
    })

    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev") as client:
        with pytest.raises(PermissionError, match="HMAC"):
            client.parse_webhook(payload, secret="wrong")


# --- get_message returns trusted (pre-verified) emails ---

def test_get_message_returns_pre_verified_email(httpx_mock):
    """REST-fetched emails are trusted (channel auth) — no verify needed."""
    raw = _make_raw_email()
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40agents.e2a.dev/messages/msg_rest",
        method="GET",
        json={
            "message_id": "msg_rest",
            "from": "alice@gmail.com",
            "recipient": "bot@agents.e2a.dev",
            "to": ["bot@agents.e2a.dev"], "cc": [],
            "subject": "REST fetched",
            "raw_message": base64.b64encode(raw).decode(),
            "auth_headers": {"X-E2A-Auth-Verified": "true"},
        },
    )
    with E2AClient(api_key="k", agent_email="bot@agents.e2a.dev", base_url=BASE) as client:
        email = client.get_message("msg_rest")

    # Pre-verified — no verify_signature() needed for field access.
    assert email.verified is True
    assert email.sender == "alice@gmail.com"
    # Subject comes from the parsed raw RFC 2822 (the fixture's default
    # "Hello") since the SDK prefers that over the JSON subject field.
    assert email.subject == "Hello"

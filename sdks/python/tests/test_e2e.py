"""
E2E tests against the live e2a API.

Requires:
    E2A_API_KEY and E2A_AGENT_EMAIL env vars (or ~/.e2a/config.json)

Run: pytest tests/test_e2e.py -v -s
"""
import json
import os
import time
from pathlib import Path

import pytest

from e2a import E2AClient, E2AApiError


def _load_config():
    api_key = os.environ.get("E2A_API_KEY", "")
    agent_email = os.environ.get("E2A_AGENT_EMAIL", "")

    if not api_key:
        try:
            config = json.loads((Path.home() / ".e2a" / "config").read_text())
            api_key = config.get("api_key", "")
            agent_email = agent_email or config.get("agent_email", "")
        except (FileNotFoundError, json.JSONDecodeError):
            pass

    if not api_key or not agent_email:
        pytest.skip("E2E tests require E2A_API_KEY and E2A_AGENT_EMAIL (or ~/.e2a/config.json)")

    return api_key, agent_email


# Shared state across tests in this module
_sent_message_subject = None


@pytest.fixture(scope="module")
def client():
    api_key, agent_email = _load_config()
    with E2AClient(api_key=api_key, agent_email=agent_email) as c:
        yield c


@pytest.fixture(scope="module")
def agent_email():
    _, email = _load_config()
    return email


def _wait_for_message(client, subject, timeout=15, interval=2):
    """Poll inbox until a message with the given subject appears."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        msg_list = client.get_messages(status="all", page_size=50)
        found = next((m for m in msg_list.messages if m.subject == subject), None)
        if found:
            return found
        time.sleep(interval)
    return None


# ── send ─────────────────────────────────────────────────────────


def test_send(client, agent_email):
    global _sent_message_subject
    _sent_message_subject = f"Python SDK e2e test {int(time.time())}"

    send_result = client.send(agent_email, _sent_message_subject, "Hello from Python SDK e2e test")
    assert send_result.status == "sent"
    assert send_result.message_id
    assert send_result.method == "smtp"


# ── get_messages ─────────────────────────────────────────────────


def test_get_messages_finds_sent_email(client):
    found = _wait_for_message(client, _sent_message_subject)
    assert found is not None, f"Message '{_sent_message_subject}' not found after 15s"
    assert found.message_id.startswith("msg_")
    assert found.sender
    assert found.recipient


def test_get_messages_pagination(client):
    result = client.get_messages(status="all", page_size=2)
    assert len(result.messages) <= 2
    for m in result.messages:
        assert m.message_id
        assert m.sender
        assert m.recipient


def test_get_messages_status_filter(client):
    result = client.get_messages(status="unread")
    assert isinstance(result.messages, list)

    result = client.get_messages(status="read")
    assert isinstance(result.messages, list)


# ── get_message ──────────────────────────────────────────────────


def test_get_message(client):
    found = _wait_for_message(client, _sent_message_subject)
    assert found is not None

    email = client.get_message(found.message_id)
    assert email.subject == _sent_message_subject
    assert "Hello from Python SDK e2e test" in email.text_body
    assert email.sender
    assert email.recipient


# ── reply ────────────────────────────────────────────────────────


def test_reply(client):
    found = _wait_for_message(client, _sent_message_subject)
    assert found is not None

    email = client.get_message(found.message_id)
    reply_result = email.reply("Reply from Python SDK e2e test")
    assert reply_result.status == "sent"
    assert reply_result.message_id


def test_reply_with_conversation_id(client):
    found = _wait_for_message(client, _sent_message_subject)
    assert found is not None

    email = client.get_message(found.message_id)
    conv_id = f"reply_conv_{int(time.time())}"
    reply_result = email.reply("Reply with conv_id", conversation_id=conv_id)
    assert reply_result.status == "sent"


# ── parse (local, no network) ───────────────────────────────────


def test_parse_webhook_payload(client):
    import base64

    raw = b"From: test@example.com\r\nTo: bot@agent.dev\r\nSubject: Parse test\r\n\r\nBody text"
    payload = json.dumps({
        "message_id": "msg_parse_test",
        "from": "test@example.com",
        "to": "bot@agent.dev",
        "raw_message": base64.b64encode(raw).decode(),
        "auth_headers": {
            "X-E2A-Auth-Verified": "true",
            "X-E2A-Auth-Sender": "test@example.com",
            "X-E2A-Auth-Entity-Type": "human",
        },
    }).encode()

    email = client.parse(payload)
    assert email.message_id == "msg_parse_test"
    assert email.sender == "test@example.com"
    assert email.subject == "Parse test"
    assert email.text_body == "Body text"
    assert email.is_verified is True


# ── error handling ───────────────────────────────────────────────


def test_get_nonexistent_message_404(client):
    with pytest.raises(E2AApiError) as exc_info:
        client.get_message(f"msg_nonexistent_{int(time.time())}")
    assert exc_info.value.status_code == 404


def test_reply_to_nonexistent_message_404(client):
    with pytest.raises(E2AApiError) as exc_info:
        client.reply(f"msg_nonexistent_{int(time.time())}", "should fail")
    assert exc_info.value.status_code == 404


def test_client_requires_agent_email_at_point_of_use(monkeypatch):
    monkeypatch.delenv("E2A_AGENT_EMAIL", raising=False)
    client = E2AClient(api_key="e2a_test")
    with pytest.raises(ValueError, match="agent_email is required"):
        client.get_messages()
    client.close()

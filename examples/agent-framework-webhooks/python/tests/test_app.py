from __future__ import annotations

import hashlib
import hmac
import json
import time
from types import SimpleNamespace
from typing import Any
from unittest.mock import AsyncMock, Mock

import pytest
from fastapi.testclient import TestClient

from agent_webhooks.app import create_app
from agent_webhooks.delivery_state import EventDeduper

SECRET = "whsec_app_test"


@pytest.fixture(autouse=True)
def openai_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "test-openai-key")


def signed_delivery(*, event_id: str = "evt_app_1") -> tuple[bytes, str]:
    payload = {
        "id": event_id,
        "type": "email.received",
        "schema_version": "1",
        "created_at": "2026-07-20T12:00:00.000Z",
        "data": {
            "message_id": "msg_app_1",
            "agent_email": "agent@example.com",
            "direction": "inbound",
            "conversation_id": "conv_app_1",
            "header_from": "sender@example.net",
            "envelope_from": "sender@example.net",
            "verified_domain": "example.net",
            "to": ["agent@example.com"],
            "cc": [],
            "reply_to": [],
            "delivered_to": "agent@example.com",
            "subject": "Hello",
            "received_at": "2026-07-20T12:00:00.000Z",
            "attachments": [],
            "authentication": {
                "spf": {"status": "pass", "domain": "example.net", "aligned": True},
                "dkim": [],
                "dmarc": {
                    "status": "pass",
                    "domain": "example.net",
                    "policy": "reject",
                    "aligned_by": ["spf"],
                },
            },
        },
    }
    body = json.dumps(payload, separators=(",", ":"), sort_keys=True).encode()
    timestamp = str(int(time.time()))
    digest = hmac.new(
        SECRET.encode(), timestamp.encode() + b"." + body, hashlib.sha256
    ).hexdigest()
    return body, f"t={timestamp},v1={digest}"


class FakeClient:
    def __init__(self) -> None:
        self.email = SimpleNamespace(
            conversation_id="conv_app_1",
            from_="sender@example.net",
            subject="Hello",
            verified=True,
            flagged=False,
            text="Please reply.",
            reply=AsyncMock(return_value=SimpleNamespace(status="accepted")),
        )
        self.inbound = SimpleNamespace(from_event=AsyncMock(return_value=self.email))
        self.aclose = AsyncMock()


class FakeAgent:
    def __init__(self) -> None:
        self.reply = AsyncMock(return_value="reply")


class ClosableAgent(FakeAgent):
    def __init__(self) -> None:
        super().__init__()
        self.aclose = AsyncMock()


def test_health_and_lifespan_create_and_close_one_client() -> None:
    clients: list[FakeClient] = []

    def client_factory(**_: Any) -> FakeClient:
        client = FakeClient()
        clients.append(client)
        return client

    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        client_factory=client_factory,
        agent_factory=FakeAgent,
    )
    assert clients == []

    with TestClient(app) as http:
        assert http.get("/health").json() == {"status": "ok"}
        assert len(clients) == 1

    clients[0].aclose.assert_awaited_once_with()


def test_invalid_signature_returns_401_without_downstream_calls() -> None:
    client = FakeClient()
    agent = FakeAgent()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        client_factory=lambda **_: client,
        agent_factory=lambda: agent,
    )
    body, signature = signed_delivery()

    with TestClient(app) as http:
        response = http.post(
            "/webhook",
            content=body,
            headers={"X-E2A-Signature": signature + "bad"},
        )

    assert response.status_code == 401
    client.inbound.from_event.assert_not_awaited()
    client.email.reply.assert_not_awaited()
    agent.reply.assert_not_awaited()


def test_oversized_stream_without_content_length_returns_413_before_delivery() -> None:
    client = FakeClient()
    agent = FakeAgent()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        client_factory=lambda **_: client,
        agent_factory=lambda: agent,
    )

    def chunks() -> Any:
        yield b"x" * 700_000
        yield b"y" * 700_000

    with TestClient(app) as http:
        response = http.post(
            "/webhook",
            content=chunks(),
            headers={"X-E2A-Signature": "invalid"},
        )

    assert response.request.headers.get("content-length") is None
    assert response.status_code == 413
    client.inbound.from_event.assert_not_awaited()
    client.email.reply.assert_not_awaited()
    agent.reply.assert_not_awaited()


def test_delivery_in_progress_returns_503() -> None:
    client = FakeClient()
    agent = FakeAgent()
    deduper = EventDeduper()
    body, signature = signed_delivery()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        client_factory=lambda **_: client,
        deduper_factory=lambda: deduper,
        agent_factory=lambda: agent,
    )

    with TestClient(app) as http:
        assert http.portal is not None
        assert http.portal.call(deduper.claim, "evt_app_1") == "new"
        response = http.post(
            "/webhook", content=body, headers={"X-E2A-Signature": signature}
        )

    assert response.status_code == 503
    client.inbound.from_event.assert_not_awaited()
    agent.reply.assert_not_awaited()


def test_valid_signed_delivery_replies_once() -> None:
    client = FakeClient()
    agent = FakeAgent()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        client_factory=lambda **_: client,
        agent_factory=lambda: agent,
    )
    body, signature = signed_delivery()

    with TestClient(app) as http:
        response = http.post(
            "/webhook", content=body, headers={"X-E2A-Signature": signature}
        )

    assert response.status_code == 200
    assert response.json() == {
        "status": "replied",
        "conversation_id": "conv_app_1",
    }
    client.inbound.from_event.assert_awaited_once()
    client.email.reply.assert_awaited_once()
    agent.reply.assert_awaited_once()


def test_startup_requires_openai_key_before_creating_dependencies(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.delenv("OPENAI_API_KEY")
    client_factory = Mock()
    agent_factory = Mock()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        client_factory=client_factory,
        agent_factory=agent_factory,
    )

    with pytest.raises(ValueError, match="OPENAI_API_KEY is required"):
        with TestClient(app):
            pass

    agent_factory.assert_not_called()
    client_factory.assert_not_called()


def test_lifespan_closes_agent_and_client() -> None:
    client = FakeClient()
    agent = ClosableAgent()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        client_factory=lambda **_: client,
        agent_factory=lambda: agent,
    )

    with TestClient(app):
        pass

    client.aclose.assert_awaited_once_with()
    agent.aclose.assert_awaited_once_with()


def test_partial_startup_failure_closes_created_agent() -> None:
    agent = ClosableAgent()

    def fail_client(**_: Any) -> Any:
        raise RuntimeError("client construction failed")

    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        client_factory=fail_client,
        agent_factory=lambda: agent,
    )

    with pytest.raises(RuntimeError, match="client construction failed"):
        with TestClient(app):
            pass

    agent.aclose.assert_awaited_once_with()

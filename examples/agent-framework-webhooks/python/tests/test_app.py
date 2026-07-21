from __future__ import annotations

import hashlib
import hmac
import json
import time
from types import SimpleNamespace
from typing import Any
from unittest.mock import AsyncMock

import pytest
from fastapi.testclient import TestClient

from agent_webhooks.app import create_app, select_agent
from agent_webhooks.delivery_state import EventDeduper

SECRET = "whsec_app_test"


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
        self.inbound = SimpleNamespace(
            from_event=AsyncMock(return_value=self.email)
        )
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
        framework="fake",
        client_factory=client_factory,
    )
    assert clients == []

    with TestClient(app) as http:
        assert http.get("/health").json() == {"status": "ok"}
        assert len(clients) == 1

    clients[0].aclose.assert_awaited_once_with()


def test_invalid_signature_returns_401_without_downstream_calls() -> None:
    client = FakeClient()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="fake",
        client_factory=lambda **_: client,
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
    assert app.state.agent.call_count == 0


def test_delivery_in_progress_returns_503() -> None:
    client = FakeClient()
    deduper = EventDeduper()
    body, signature = signed_delivery()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="fake",
        client_factory=lambda **_: client,
        deduper_factory=lambda: deduper,
    )

    with TestClient(app) as http:
        assert http.portal is not None
        assert http.portal.call(deduper.claim, "evt_app_1") == "new"
        response = http.post(
            "/webhook", content=body, headers={"X-E2A-Signature": signature}
        )

    assert response.status_code == 503
    client.inbound.from_event.assert_not_awaited()
    assert app.state.agent.call_count == 0


def test_valid_signed_fake_delivery_replies_once() -> None:
    client = FakeClient()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="fake",
        client_factory=lambda **_: client,
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
    assert app.state.agent.call_count == 1


@pytest.mark.parametrize(
    "framework", ["openai", "anthropic", "langchain", "adk", "fake"]
)
def test_framework_selection_accepts_exact_supported_names(
    framework: str,
) -> None:
    sentinel = object()
    factories = {name: lambda: sentinel for name in (
        "openai", "anthropic", "langchain", "adk", "fake"
    )}

    assert select_agent(framework, factories=factories) is sentinel


@pytest.mark.parametrize("framework", ["", "OPENAI", "unknown", " fake "])
def test_framework_selection_rejects_unknown_names(framework: str) -> None:
    with pytest.raises(ValueError, match="AGENT_FRAMEWORK must be one of"):
        select_agent(framework)


def test_unknown_framework_fails_startup_before_creating_client() -> None:
    factory = AsyncMock()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="unknown",
        client_factory=factory,
    )

    with pytest.raises(ValueError, match="AGENT_FRAMEWORK must be one of"):
        with TestClient(app):
            pass

    factory.assert_not_called()

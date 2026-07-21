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


class ClosableAgent:
    def __init__(self) -> None:
        self.aclose = AsyncMock()

    async def reply(self, email: Any) -> str:
        return "reply"


def agent_factories(agent: Any) -> dict[str, Any]:
    return {
        name: lambda: agent
        for name in ("openai", "anthropic", "langchain", "adk", "fake")
    }


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


def test_oversized_stream_without_content_length_returns_413_before_delivery() -> None:
    client = FakeClient()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="fake",
        client_factory=lambda **_: client,
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


@pytest.mark.parametrize(
    ("framework", "missing", "message"),
    [
        ("openai", ["OPENAI_API_KEY"], "OPENAI_API_KEY"),
        ("anthropic", ["ANTHROPIC_API_KEY"], "ANTHROPIC_API_KEY"),
        ("langchain", ["OPENAI_API_KEY"], "OPENAI_API_KEY"),
        ("adk", ["GEMINI_API_KEY", "GOOGLE_API_KEY"], "GEMINI_API_KEY or GOOGLE_API_KEY"),
    ],
)
def test_real_framework_missing_provider_config_fails_before_client_creation(
    framework: str,
    missing: list[str],
    message: str,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    for name in missing:
        monkeypatch.delenv(name, raising=False)
    monkeypatch.delenv("LANGCHAIN_MODEL", raising=False)
    monkeypatch.delenv("GOOGLE_GENAI_USE_VERTEXAI", raising=False)
    factory = Mock()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework=framework,
        client_factory=factory,
        agent_factories=agent_factories(ClosableAgent()),
    )

    with pytest.raises(ValueError, match=message):
        with TestClient(app):
            pass

    factory.assert_not_called()


@pytest.mark.parametrize("key_name", ["GEMINI_API_KEY", "GOOGLE_API_KEY"])
def test_adk_accepts_either_api_key(
    key_name: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("GEMINI_API_KEY", raising=False)
    monkeypatch.delenv("GOOGLE_API_KEY", raising=False)
    monkeypatch.delenv("GOOGLE_GENAI_USE_VERTEXAI", raising=False)
    monkeypatch.setenv(key_name, "test-provider-key")
    client = FakeClient()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="adk",
        client_factory=lambda **_: client,
        agent_factories=agent_factories(ClosableAgent()),
    )

    with TestClient(app) as http:
        assert http.get("/health").status_code == 200


def test_adk_accepts_complete_vertex_configuration(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.delenv("GEMINI_API_KEY", raising=False)
    monkeypatch.delenv("GOOGLE_API_KEY", raising=False)
    monkeypatch.setenv("GOOGLE_GENAI_USE_VERTEXAI", "true")
    monkeypatch.setenv("GOOGLE_CLOUD_PROJECT", "test-project")
    monkeypatch.setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
    client = FakeClient()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="adk",
        client_factory=lambda **_: client,
        agent_factories=agent_factories(ClosableAgent()),
    )

    with TestClient(app) as http:
        assert http.get("/health").status_code == 200


def test_langchain_rejects_uninstalled_provider_prefix(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "test-provider-key")
    monkeypatch.setenv("LANGCHAIN_MODEL", "anthropic:claude-example")
    factory = Mock()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="langchain",
        client_factory=factory,
        agent_factories=agent_factories(ClosableAgent()),
    )

    with pytest.raises(ValueError, match="must use the installed openai: provider"):
        with TestClient(app):
            pass

    factory.assert_not_called()


def test_lifespan_closes_agent_and_client() -> None:
    client = FakeClient()
    agent = ClosableAgent()
    app = create_app(
        api_key="e2a_test",
        webhook_secret=SECRET,
        framework="fake",
        client_factory=lambda **_: client,
        agent_factories=agent_factories(agent),
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
        framework="fake",
        client_factory=fail_client,
        agent_factories=agent_factories(agent),
    )

    with pytest.raises(RuntimeError, match="client construction failed"):
        with TestClient(app):
            pass

    agent.aclose.assert_awaited_once_with()

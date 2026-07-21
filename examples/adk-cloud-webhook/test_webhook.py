from __future__ import annotations

import hashlib
import hmac
import json
import os
import time
import unittest
import warnings
from types import SimpleNamespace
from typing import Any, AsyncIterator, Iterator
from unittest.mock import AsyncMock, patch

os.environ.setdefault("E2A_API_KEY", "e2a_test")
os.environ.setdefault("E2A_WEBHOOK_SECRET", "whsec_test")
os.environ.setdefault("GOOGLE_API_KEY", "google_test")

from starlette.exceptions import StarletteDeprecationWarning

warnings.filterwarnings(
    "ignore",
    message="Using `httpx` with `starlette.testclient` is deprecated.*",
    category=StarletteDeprecationWarning,
)
from starlette.testclient import TestClient

import webhook as webhook_module
from delivery_state import EventDeduper

SECRET = os.environ["E2A_WEBHOOK_SECRET"]


def signed_delivery(*, event_id: str = "evt_test_1") -> tuple[bytes, str]:
    payload = {
        "type": "email.received",
        "id": event_id,
        "schema_version": "1",
        "created_at": "2026-07-20T12:00:00Z",
        "data": {
            "message_id": "msg_test_1",
            "agent_email": "bot@agents.e2a.dev",
            "direction": "inbound",
            "conversation_id": "conv_test_1",
            "header_from": "Alice <alice@example.com>",
            "envelope_from": "alice@example.com",
            "verified_domain": "example.com",
            "authentication": None,
            "to": ["bot@agents.e2a.dev"],
            "cc": [],
            "reply_to": [],
            "delivered_to": "bot@agents.e2a.dev",
            "subject": "Hello",
            "received_at": "2026-07-20T12:00:00Z",
        },
    }
    body = json.dumps(payload, separators=(",", ":")).encode()
    timestamp = str(int(time.time()))
    digest = hmac.new(
        SECRET.encode(), timestamp.encode() + b"." + body, hashlib.sha256
    ).hexdigest()
    return body, f"t={timestamp},v1={digest}"


class _Runner:
    def __init__(self, reply: str = "Thanks") -> None:
        self.calls = 0
        self.reply = reply

    async def run_async(self, **_kwargs: Any) -> AsyncIterator[Any]:
        self.calls += 1
        yield SimpleNamespace(
            is_final_response=lambda: True,
            content=SimpleNamespace(parts=[SimpleNamespace(text=self.reply)]),
        )


class _FailingRunner:
    def __init__(self) -> None:
        self.calls = 0

    async def run_async(self, **_kwargs: Any) -> AsyncIterator[Any]:
        self.calls += 1
        if False:
            yield None
        raise RuntimeError("agent failed")


class WebhookBehaviorTest(unittest.TestCase):
    def setUp(self) -> None:
        webhook_module.event_deduper = EventDeduper()

    @staticmethod
    def _email() -> SimpleNamespace:
        return SimpleNamespace(
            id="msg_test_1",
            inbox="bot@agents.e2a.dev",
            conversation_id="conv_test_1",
            from_="Alice <alice@example.com>",
            subject="Hello",
            text="Please reply.",
            verified=True,
            flagged=False,
            reply=AsyncMock(return_value=SimpleNamespace(status="accepted")),
        )

    def test_chunked_oversized_body_is_rejected_before_hydration(self) -> None:
        email = self._email()

        def chunks() -> Iterator[bytes]:
            yield b"x" * 700_000
            yield b"y" * 700_000

        with patch.object(webhook_module, "construct_event") as construct:
            with TestClient(webhook_module.app) as http:
                inbound = SimpleNamespace(from_event=AsyncMock(return_value=email))
                webhook_module.app.state.e2a = SimpleNamespace(inbound=inbound)
                response = http.post(
                    "/webhook",
                    content=chunks(),
                    headers={"X-E2A-Signature": "invalid"},
                )

        self.assertIsNone(response.request.headers.get("content-length"))
        self.assertEqual(response.status_code, 413)
        construct.assert_not_called()
        inbound.from_event.assert_not_awaited()

    def test_invalid_signature_precedes_hydration_and_agent(self) -> None:
        email = self._email()
        body, _ = signed_delivery()
        runner = _Runner()

        with TestClient(webhook_module.app) as http:
            inbound = SimpleNamespace(from_event=AsyncMock(return_value=email))
            webhook_module.app.state.e2a = SimpleNamespace(inbound=inbound)
            webhook_module.app.state.runner = runner
            response = http.post(
                "/webhook",
                content=body,
                headers={"X-E2A-Signature": "invalid"},
            )

        self.assertEqual(response.status_code, 401)
        inbound.from_event.assert_not_awaited()
        self.assertEqual(runner.calls, 0)
        email.reply.assert_not_awaited()

    def test_bound_reply_uses_event_context(self) -> None:
        email = self._email()
        body, signature = signed_delivery()
        runner = _Runner("Confirmed")

        with TestClient(webhook_module.app) as http:
            inbound = SimpleNamespace(from_event=AsyncMock(return_value=email))
            webhook_module.app.state.e2a = SimpleNamespace(inbound=inbound)
            webhook_module.app.state.runner = runner
            response = http.post(
                "/webhook",
                content=body,
                headers={"X-E2A-Signature": signature},
            )

        self.assertEqual(response.status_code, 200)
        inbound.from_event.assert_awaited_once()
        email.reply.assert_awaited_once_with(
            {"text": "Confirmed", "conversation_id": "conv_test_1"},
            idempotency_key="evt_test_1",
        )

    def test_agent_failure_releases_claim_for_retry(self) -> None:
        email = self._email()
        body, signature = signed_delivery(event_id="evt_retry")
        runner = _FailingRunner()

        with TestClient(webhook_module.app, raise_server_exceptions=False) as http:
            inbound = SimpleNamespace(from_event=AsyncMock(return_value=email))
            webhook_module.app.state.e2a = SimpleNamespace(inbound=inbound)
            webhook_module.app.state.runner = runner
            first = http.post(
                "/webhook", content=body, headers={"X-E2A-Signature": signature}
            )
            second = http.post(
                "/webhook", content=body, headers={"X-E2A-Signature": signature}
            )

        self.assertEqual(first.status_code, 500)
        self.assertEqual(second.status_code, 500)
        self.assertEqual(runner.calls, 2)
        self.assertEqual(inbound.from_event.await_count, 2)


if __name__ == "__main__":
    unittest.main()

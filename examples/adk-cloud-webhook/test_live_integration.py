"""Opt-in live dry run for the ADK webhook example.

Run this against the repository's contract server. The test uses the real
Python SDK and HTTP handlers for agent creation, self-send loopback, message
fetch, and reply. Only the Gemini turn is replaced with a deterministic local
runner so the dry run is repeatable and never consumes model quota.
"""

from __future__ import annotations

import asyncio
import hashlib
import hmac
import importlib
import json
import os
import time
from types import SimpleNamespace
import unittest
from urllib.parse import quote


class _DeterministicRunner:
    def __init__(self, reply_text: str) -> None:
        self.calls = 0
        self.reply_text = reply_text

    async def run_async(self, **_kwargs):
        self.calls += 1
        yield SimpleNamespace(
            is_final_response=lambda: True,
            content=SimpleNamespace(
                parts=[SimpleNamespace(text=self.reply_text)],
            ),
        )


@unittest.skipUnless(
    os.environ.get("E2A_DRY_RUN") == "1",
    "set E2A_DRY_RUN=1 with a live contract server to run",
)
class LiveWebhookIntegrationTest(unittest.TestCase):
    def test_signed_inbound_fetches_and_replies_once(self) -> None:
        from fastapi.testclient import TestClient
        import httpx

        base_url = os.environ["E2A_API_URL"]
        api_key = os.environ["E2A_API_KEY"]
        signing_secret = os.environ["E2A_WEBHOOK_SECRET"]
        bot = f"adk-dry-run-{time.time_ns():x}@agents.e2a.dev"
        subject = f"ADK webhook dry run {time.time_ns()}"
        encoded_bot = quote(bot, safe="")
        auth = {"Authorization": f"Bearer {api_key}"}

        async def arrange() -> str:
            # Provisioning is deliberately raw HTTP: the application under test
            # uses an agent-scoped SDK client and never creates/deletes agents.
            async with httpx.AsyncClient(base_url=base_url, headers=auth) as client:
                created = await client.post(
                    "/v1/agents", json={"email": bot, "name": "ADK webhook dry run"}
                )
                self.assertEqual(created.status_code, 201, created.text)
                sent = await client.post(
                    f"/v1/agents/{encoded_bot}/messages",
                    json={"to": [bot], "subject": subject, "text": "Please confirm receipt."},
                    headers={"Idempotency-Key": f"dry-run-send-{time.time_ns()}"},
                )
                self.assertEqual(sent.status_code, 200, sent.text)
                self.assertIn(sent.json()["status"], {"sent", "accepted"})

                for _ in range(20):
                    listed = await client.get(
                        f"/v1/agents/{encoded_bot}/messages",
                        params={"direction": "inbound", "limit": 20},
                    )
                    self.assertEqual(listed.status_code, 200, listed.text)
                    match = next(
                        (item for item in listed.json()["items"] if item["subject"] == subject),
                        None,
                    )
                    if match is not None:
                        return match["id"]
                    await asyncio.sleep(0.25)
                self.fail("self-send did not produce an inbound message")

        async def cleanup() -> None:
            async with httpx.AsyncClient(base_url=base_url, headers=auth) as client:
                deleted = await client.delete(
                    f"/v1/agents/{encoded_bot}", params={"confirm": "DELETE"}
                )
                self.assertEqual(deleted.status_code, 200, deleted.text)

        message_id = asyncio.run(arrange())
        try:
            event_id = f"evt_dryrun_{time.time_ns():x}"
            payload = {
                "type": "email.received",
                "id": event_id,
                "schema_version": "1",
                "created_at": "2026-07-19T00:00:00Z",
                "data": {
                    "message_id": message_id,
                    "agent_email": bot,
                    "direction": "inbound",
                    "header_from": bot,
                    "envelope_from": None,
                    "verified_domain": None,
                    "authentication": None,
                    "to": [bot],
                    "cc": [],
                    "reply_to": [],
                    "delivered_to": bot,
                    "subject": subject,
                    "received_at": "2026-07-19T00:00:00Z",
                },
            }
            raw_body = json.dumps(payload, separators=(",", ":")).encode()
            timestamp = str(int(time.time()))
            signature = hmac.new(
                signing_secret.encode(),
                timestamp.encode() + b"." + raw_body,
                hashlib.sha256,
            ).hexdigest()

            webhook_app = importlib.import_module("webhook")
            runner = _DeterministicRunner("Confirmed by the ADK webhook dry run.")
            with TestClient(webhook_app.app) as http:
                webhook_app.app.state.runner = runner
                headers = {"X-E2A-Signature": f"t={timestamp},v1={signature}"}

                response = http.post("/webhook", content=raw_body, headers=headers)
                self.assertEqual(response.status_code, 200, response.text)
                self.assertEqual(response.json()["status"], "replied")

                duplicate = http.post("/webhook", content=raw_body, headers=headers)
                self.assertEqual(duplicate.status_code, 200, duplicate.text)
                self.assertEqual(duplicate.json()["status"], "duplicate")
                self.assertEqual(runner.calls, 1)
        finally:
            asyncio.run(cleanup())


if __name__ == "__main__":
    unittest.main()

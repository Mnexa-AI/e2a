from __future__ import annotations

import hashlib
import hmac
import json
import time
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock

from e2a import E2AWebhookSignatureError, construct_event

from agent_webhooks.delivery_state import EventDeduper
from agent_webhooks.handler import DeliveryInProgress, handle_delivery

SECRET = "whsec_test"


def signed_delivery(
    *, event_id: str = "evt_1", event_type: str = "email.received"
) -> tuple[bytes, str]:
    payload = {
        "id": event_id,
        "type": event_type,
        "schema_version": "1",
        "created_at": "2026-07-20T12:00:00.000Z",
        "data": {
            "message_id": "msg_1",
            "agent_email": "agent@example.com",
            "direction": "inbound",
            "conversation_id": "conv_1",
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
                "spf": {
                    "status": "pass",
                    "domain": "example.net",
                    "aligned": True,
                },
                "dkim": [
                    {
                        "status": "pass",
                        "domain": "example.net",
                        "selector": "selector1",
                        "aligned": True,
                    }
                ],
                "dmarc": {
                    "status": "pass",
                    "domain": "example.net",
                    "policy": "reject",
                    "aligned_by": ["spf", "dkim"],
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


def collaborators(
    *, agent_output: object = "Thanks", send_status: str = "accepted"
) -> tuple[SimpleNamespace, SimpleNamespace, SimpleNamespace]:
    email = SimpleNamespace(
        conversation_id="conv_1",
        reply=AsyncMock(return_value=SimpleNamespace(status=send_status)),
    )
    inbound = SimpleNamespace(from_event=AsyncMock(return_value=email))
    agent = SimpleNamespace(reply=AsyncMock(return_value=agent_output))
    return inbound, agent, email


class HandleDeliveryTest(unittest.IsolatedAsyncioTestCase):
    async def test_verified_email_is_fetched_and_replied_to(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, email = collaborators(agent_output="  Thanks  ")
        deduper = EventDeduper()

        result = await handle_delivery(
            body, signature, SECRET, inbound, agent, deduper
        )

        self.assertEqual(result, {"status": "replied", "conversation_id": "conv_1"})
        expected_event = construct_event(body, signature, SECRET)
        inbound.from_event.assert_awaited_once_with(expected_event)
        agent.reply.assert_awaited_once_with(email)
        email.reply.assert_awaited_once_with(
            {"text": "Thanks", "conversation_id": "conv_1"},
            idempotency_key="evt_1",
        )

    async def test_invalid_signature_has_no_effect_and_does_not_claim_event(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, email = collaborators()
        deduper = EventDeduper()
        bad_signature = signature[:-1] + ("0" if signature[-1] != "0" else "1")

        with self.assertRaises(E2AWebhookSignatureError):
            await handle_delivery(body, bad_signature, SECRET, inbound, agent, deduper)

        inbound.from_event.assert_not_awaited()
        agent.reply.assert_not_awaited()
        email.reply.assert_not_awaited()
        self.assertEqual(await deduper.claim("evt_1"), "new")

    async def test_non_email_event_is_ignored_before_claim_or_fetch(self) -> None:
        body, signature = signed_delivery(event_type="email.sent")
        inbound, agent, email = collaborators()
        deduper = EventDeduper()

        result = await handle_delivery(
            body, signature, SECRET, inbound, agent, deduper
        )

        self.assertEqual(result, {"status": "ignored"})
        inbound.from_event.assert_not_awaited()
        agent.reply.assert_not_awaited()
        email.reply.assert_not_awaited()
        self.assertEqual(await deduper.claim("evt_1"), "new")

    async def test_completed_duplicate_runs_only_one_agent_turn_and_reply(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, email = collaborators()
        deduper = EventDeduper()

        first = await handle_delivery(body, signature, SECRET, inbound, agent, deduper)
        second = await handle_delivery(body, signature, SECRET, inbound, agent, deduper)

        self.assertEqual(first["status"], "replied")
        self.assertEqual(second, {"status": "duplicate"})
        inbound.from_event.assert_awaited_once()
        agent.reply.assert_awaited_once()
        email.reply.assert_awaited_once()

    async def test_in_flight_duplicate_exposes_event_id(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, email = collaborators()
        deduper = EventDeduper()
        self.assertEqual(await deduper.claim("evt_1"), "new")

        with self.assertRaises(DeliveryInProgress) as raised:
            await handle_delivery(body, signature, SECRET, inbound, agent, deduper)

        self.assertEqual(raised.exception.event_id, "evt_1")
        inbound.from_event.assert_not_awaited()
        agent.reply.assert_not_awaited()
        email.reply.assert_not_awaited()

    async def test_whitespace_only_agent_output_completes_without_reply(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, email = collaborators(agent_output=" \n\t ")
        deduper = EventDeduper()

        result = await handle_delivery(
            body, signature, SECRET, inbound, agent, deduper
        )
        duplicate = await handle_delivery(
            body, signature, SECRET, inbound, agent, deduper
        )

        self.assertEqual(result, {"status": "no_reply", "conversation_id": "conv_1"})
        self.assertEqual(duplicate, {"status": "duplicate"})
        email.reply.assert_not_awaited()

    async def test_agent_exception_releases_claim_for_retry(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, email = collaborators()
        agent.reply.side_effect = [RuntimeError("agent failed"), "Thanks"]
        deduper = EventDeduper()

        with self.assertRaisesRegex(RuntimeError, "agent failed"):
            await handle_delivery(body, signature, SECRET, inbound, agent, deduper)

        result = await handle_delivery(body, signature, SECRET, inbound, agent, deduper)

        self.assertEqual(result["status"], "replied")
        self.assertEqual(agent.reply.await_count, 2)
        email.reply.assert_awaited_once()

    async def test_reply_exception_releases_claim_for_retry(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, email = collaborators()
        email.reply.side_effect = [
            RuntimeError("send failed"),
            SimpleNamespace(status="accepted"),
        ]
        deduper = EventDeduper()

        with self.assertRaisesRegex(RuntimeError, "send failed"):
            await handle_delivery(body, signature, SECRET, inbound, agent, deduper)

        result = await handle_delivery(body, signature, SECRET, inbound, agent, deduper)

        self.assertEqual(result["status"], "replied")
        self.assertEqual(inbound.from_event.await_count, 2)
        self.assertEqual(agent.reply.await_count, 2)
        self.assertEqual(email.reply.await_count, 2)

    async def test_nonstandard_send_status_is_preserved(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, _ = collaborators(send_status="pending_review")

        result = await handle_delivery(
            body, signature, SECRET, inbound, agent, EventDeduper()
        )

        self.assertEqual(
            result, {"status": "pending_review", "conversation_id": "conv_1"}
        )

    async def test_sent_send_status_is_mapped_to_replied(self) -> None:
        body, signature = signed_delivery()
        inbound, agent, _ = collaborators(send_status="sent")

        result = await handle_delivery(
            body, signature, SECRET, inbound, agent, EventDeduper()
        )

        self.assertEqual(
            result, {"status": "replied", "conversation_id": "conv_1"}
        )

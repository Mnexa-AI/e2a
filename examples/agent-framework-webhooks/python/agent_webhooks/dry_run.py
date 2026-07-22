"""No-key, no-network exercise of the complete signed-delivery lifecycle."""

from __future__ import annotations

import asyncio
import hashlib
import hmac
import json
import time
from typing import Any, TypedDict

from e2a import AsyncInboundEmail, AsyncInboundResource, models

from .delivery_state import EventDeduper
from .handler import handle_delivery
from .prompt import email_prompt

SECRET = "whsec_agent_framework_dry_run"


class Evidence(TypedDict):
    first_status: str
    second_status: str
    reply: str
    reply_count: int


class FakeReplyAgent:
    """Deterministic no-key agent used only by this local dry run."""

    def __init__(self, response: str) -> None:
        self.response = response
        self.prompts: list[str] = []

    async def reply(
        self, email: AsyncInboundEmail, conversation_id: str
    ) -> str:
        del conversation_id
        self.prompts.append(email_prompt(email))
        return self.response


class FakeMessageOperations:
    """In-memory message operations implementing the public inbound protocol."""

    def __init__(self) -> None:
        self.replies: list[tuple[str, str, Any, str | None]] = []
        self.message = models.MessageView.model_validate(
            {
                "attachments": [],
                "authentication": {
                    "spf": {
                        "status": "pass",
                        "domain": "example.net",
                        "aligned": True,
                    },
                    "dkim": [],
                    "dmarc": {
                        "status": "pass",
                        "domain": "example.net",
                        "policy": "reject",
                        "aligned_by": ["spf"],
                    },
                },
                "cc": [],
                "conversation_id": "",
                "created_at": "2026-07-20T12:00:00.000Z",
                "delivered_to": "agent@example.com",
                "direction": "inbound",
                "envelope_from": "sender@example.net",
                "flagged": False,
                "header_from": "Sender <sender@example.net>",
                "id": "msg_dry_run",
                "labels": [],
                "parsed": {
                    "text": "Please send the deterministic response.",
                    "truncated": False,
                },
                "raw_message": None,
                "read_status": "unread",
                "reply_to": [],
                "subject": "Dry run",
                "to": ["agent@example.com"],
                "verified_domain": "example.net",
            }
        )

    async def get(self, email: str, message_id: str) -> models.MessageView:
        if (email, message_id) != ("agent@example.com", "msg_dry_run"):
            raise AssertionError("unexpected message fetch")
        return self.message

    async def get_attachment(
        self,
        email: str,
        message_id: str,
        index: int,
        *,
        inline: bool = False,
    ) -> models.AttachmentView:
        raise AssertionError("fixture has no attachments")

    async def reply(
        self,
        email: str,
        message_id: str,
        body: Any,
        *,
        idempotency_key: str | None = None,
    ) -> models.SendResultView:
        self.replies.append((email, message_id, body, idempotency_key))
        return models.SendResultView(
            message_id="msg_reply_dry_run", status="accepted"
        )

    async def forward(
        self,
        email: str,
        message_id: str,
        body: Any,
        *,
        idempotency_key: str | None = None,
    ) -> models.SendResultView:
        raise AssertionError("dry run must not forward")


def _signed_delivery() -> tuple[bytes, str]:
    payload = {
        "id": "evt_dry_run",
        "type": "email.received",
        "schema_version": "1",
        "created_at": "2026-07-20T12:00:00.000Z",
        "data": {
            "message_id": "msg_dry_run",
            "agent_email": "agent@example.com",
            "direction": "inbound",
            "conversation_id": "",
            "header_from": "Sender <sender@example.net>",
            "envelope_from": "sender@example.net",
            "verified_domain": "example.net",
            "to": ["agent@example.com"],
            "cc": [],
            "reply_to": [],
            "delivered_to": "agent@example.com",
            "subject": "Dry run",
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


async def async_main() -> Evidence:
    operations = FakeMessageOperations()
    inbound = AsyncInboundResource(operations)
    agent = FakeReplyAgent("Deterministic fake reply")
    deduper = EventDeduper()
    body, signature = _signed_delivery()

    first = await handle_delivery(body, signature, SECRET, inbound, agent, deduper)
    second = await handle_delivery(body, signature, SECRET, inbound, agent, deduper)
    if len(operations.replies) != 1:
        raise RuntimeError("dry run expected exactly one reply")

    captured = operations.replies[0]
    expected_body = {
        "text": "Deterministic fake reply",
        "conversation_id": "conv_dry_run",
    }
    if captured != (
        "agent@example.com",
        "msg_dry_run",
        expected_body,
        "evt_dry_run",
    ):
        raise RuntimeError("dry run captured an unexpected bound reply")

    captured_body = captured[2]
    reply = captured_body["text"]
    evidence: Evidence = {
        "first_status": first["status"],
        "second_status": second["status"],
        "reply": reply,
        "reply_count": len(operations.replies),
    }
    print(
        f"status={evidence['first_status']} "
        f"status={evidence['second_status']} "
        f"reply={evidence['reply']} reply_count={evidence['reply_count']}"
    )
    return evidence


def run() -> Evidence:
    return asyncio.run(async_main())


def cli_main() -> None:
    """Console entry point that reports success through a zero exit status."""

    run()


if __name__ == "__main__":
    cli_main()

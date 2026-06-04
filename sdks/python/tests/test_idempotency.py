"""Tests for the Idempotency-Key transport behavior in the Python SDK."""

import re

import pytest

from e2a.v1.api import E2AApi
from e2a.v1.client import E2AClient
from e2a.v1.generated import ReplyToMessageRequest, SendEmailRequest


BASE = "https://e2a.dev"

UUIDV4_RE = re.compile(
    r"^[0-9a-f]{32}$|^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$",
    re.IGNORECASE,
)


def test_send_email_auto_generates_idempotency_key(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "msg_abc", "method": "smtp"},
    )

    with E2AApi(api_key="e2a_test") as api:
        api.send_email(
            SendEmailRequest(to=["alice@example.com"], subject="x", body="y")
        )

    req = httpx_mock.get_request()
    key = req.headers["Idempotency-Key"]
    assert key, "Idempotency-Key header not set"
    assert UUIDV4_RE.match(key), f"key {key!r} is not a UUIDv4 hex/canonical shape"


def test_send_email_honors_caller_supplied_key(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "msg_abc", "method": "smtp"},
    )

    with E2AApi(api_key="e2a_test") as api:
        api.send_email(
            SendEmailRequest(to=["alice@example.com"], subject="x", body="y"),
            idempotency_key="user-supplied-key-42",
        )

    req = httpx_mock.get_request()
    assert req.headers["Idempotency-Key"] == "user-supplied-key-42"


def test_reply_to_message_carries_idempotency_key(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40test.dev/messages/msg_in_xyz/reply",
        method="POST",
        json={"status": "sent", "message_id": "msg_out_xyz", "method": "smtp"},
    )

    with E2AApi(api_key="e2a_test") as api:
        api.reply_to_message(
            "bot@test.dev",
            "msg_in_xyz",
            ReplyToMessageRequest(body="hi"),
            idempotency_key="reply-key-1",
        )

    req = httpx_mock.get_request()
    assert req.headers["Idempotency-Key"] == "reply-key-1"


def test_send_email_generates_different_key_each_call(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "msg_a", "method": "smtp"},
    )
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "msg_b", "method": "smtp"},
    )

    with E2AApi(api_key="e2a_test") as api:
        api.send_email(SendEmailRequest(to=["a@b.com"], subject="x", body="y"))
        api.send_email(SendEmailRequest(to=["a@b.com"], subject="x", body="y"))

    reqs = httpx_mock.get_requests()
    keys = [r.headers["Idempotency-Key"] for r in reqs]
    assert len(keys) == 2
    assert keys[0] != keys[1], "auto-generated keys should differ per call"


def test_high_level_client_send_threads_idempotency_key(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/send",
        method="POST",
        json={"status": "sent", "message_id": "msg_xyz", "method": "smtp"},
    )

    with E2AClient(
        api_key="e2a_test", agent_email="bot@test.dev"
    ) as client:
        client.send(
            ["alice@example.com"],
            "x",
            "y",
            idempotency_key="client-key-99",
        )

    req = httpx_mock.get_request()
    assert req.headers["Idempotency-Key"] == "client-key-99"


def test_high_level_client_reply_threads_idempotency_key(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40test.dev/messages/msg_in_abc/reply",
        method="POST",
        json={"status": "sent", "message_id": "msg_out_abc", "method": "smtp"},
    )

    with E2AClient(
        api_key="e2a_test", agent_email="bot@test.dev"
    ) as client:
        client.reply("msg_in_abc", "hi", idempotency_key="client-reply-key")

    req = httpx_mock.get_request()
    assert req.headers["Idempotency-Key"] == "client-reply-key"


# approve_message is also side-effectful — fires a real SES send when
# the reviewer approves a held draft. Without an Idempotency-Key a
# transient retry after a successful approve could double-send. Cover
# the same contract: auto-generated key by default, caller-supplied
# key passes through verbatim, high-level client threads it through.


def test_approve_message_auto_generates_idempotency_key(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40example.com/messages/msg_p/approve",
        method="POST",
        json={"status": "sent", "message_id": "msg_p", "method": "smtp", "edited": False},
    )

    with E2AApi(api_key="e2a_test") as api:
        api.approve_message("bot@example.com", "msg_p")

    req = httpx_mock.get_request()
    key = req.headers["Idempotency-Key"]
    assert key, "Idempotency-Key header not set"
    assert UUIDV4_RE.match(key), f"key {key!r} is not a UUIDv4 hex/canonical shape"


def test_approve_message_honors_caller_supplied_key(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40example.com/messages/msg_p/approve",
        method="POST",
        json={"status": "sent", "message_id": "msg_p", "method": "smtp", "edited": False},
    )

    with E2AApi(api_key="e2a_test") as api:
        api.approve_message("bot@example.com", "msg_p", idempotency_key="approve-key-1")

    req = httpx_mock.get_request()
    assert req.headers["Idempotency-Key"] == "approve-key-1"


def test_high_level_client_approve_threads_idempotency_key(httpx_mock):
    httpx_mock.add_response(
        url=f"{BASE}/api/v1/agents/bot%40example.com/messages/msg_p/approve",
        method="POST",
        json={"status": "sent", "message_id": "msg_p", "method": "smtp", "edited": False},
    )

    with E2AClient(
        api_key="e2a_test", agent_email="bot@test.dev"
    ) as client:
        client.approve_message("bot@example.com", "msg_p", idempotency_key="high-level-approve-key")

    req = httpx_mock.get_request()
    assert req.headers["Idempotency-Key"] == "high-level-approve-key"

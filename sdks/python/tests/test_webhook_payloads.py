"""Golden-payload lock (contract freeze PR-2).

These are the SAME fixture files the server's builder tests assert their
marshaled output against (internal/eventpayload/testdata). Parsing them
through ``construct_event`` and asserting the typed field access proves the
Python payload types match the wire bytes — a server-side field change fails
here until the SDK types are consciously updated.
"""

import hashlib
import hmac
import json
import time
from pathlib import Path

import pytest

from e2a.v1.webhook_signature import (
    WebhookEvent,
    construct_event,
    is_domain_sending_failed,
    is_domain_sending_verified,
    is_domain_suppression_added,
    is_email_bounced,
    is_email_complained,
    is_email_delivered,
    is_email_failed,
    is_email_received,
    is_email_sent,
)

FIXTURE_DIR = Path(__file__).resolve().parents[3] / "internal" / "eventpayload" / "testdata"
SECRET = "whsec_golden"

pytestmark = pytest.mark.skipif(
    not FIXTURE_DIR.is_dir(),
    reason="golden fixtures only present in the monorepo checkout",
)


def _construct(name: str) -> WebhookEvent:
    raw = (FIXTURE_DIR / name).read_text()
    t = str(int(time.time()))
    sig = hmac.new(SECRET.encode(), f"{t}.{raw}".encode(), hashlib.sha256).hexdigest()
    return construct_event(raw, f"t={t},v1={sig}", SECRET)


def test_email_received():
    e = _construct("email.received.json")
    assert e.schema_version == "1"
    assert is_email_received(e)
    d = e.data
    assert d["message_id"].startswith("msg_")
    assert d["agent_email"] == "support@agents.example.com"
    assert d["direction"] == "inbound"
    assert d["from"] == "reply@customer.example.com"
    assert d["authenticated_from"] == "alice@customer.example.com"
    assert d["to"] == ["support@agents.example.com"]
    assert d["delivered_to"] == "support@agents.example.com"
    assert d["subject"] == "Order #1234 delayed"
    assert d["auth_headers"]["X-E2A-Auth-Verified"] == "true"
    assert isinstance(d["received_at"], str)
    assert d["attachments"] == [
        {"filename": "invoice.pdf", "content_type": "application/pdf", "size_bytes": 12345, "index": 0}
    ]


def test_email_sent():
    e = _construct("email.sent.json")
    assert is_email_sent(e)
    d = e.data
    assert d["provider_message_id"]
    assert d["method"] == "smtp"
    assert d["direction"] == "outbound"
    assert d["to"] == ["alice@customer.example.com"]
    assert d["message_type"] == "reply"


def test_email_failed():
    e = _construct("email.failed.json")
    assert is_email_failed(e)
    d = e.data
    assert d["reason"] == "550 5.1.1 user unknown"
    assert d["message_type"] == "send"
    # provider_message_id is not part of email.failed (never accepted).
    assert "provider_message_id" not in d


def test_email_delivered():
    e = _construct("email.delivered.json")
    assert is_email_delivered(e)
    d = e.data
    assert d["delivered_to"] == "alice@customer.example.com"
    assert d["subject"] == "Re: Order #1234 delayed"
    # The redundant `status` field was DROPPED — the event type is the outcome.
    assert "status" not in d


def test_email_bounced():
    e = _construct("email.bounced.json")
    assert is_email_bounced(e)
    d = e.data
    assert d["bounce_type"] == "permanent"
    assert d["bounce_sub_type"] == "General"
    assert d["smtp_detail"] == "550 5.1.1 no such user"
    assert "status" not in d


def test_email_complained():
    e = _construct("email.complained.json")
    assert is_email_complained(e)
    assert e.data["delivered_to"] == "carol@customer.example.com"
    assert "status" not in e.data


def test_domain_sending_verified():
    e = _construct("domain.sending_verified.json")
    assert is_domain_sending_verified(e)
    assert e.data == {"domain": "mail.customer.example.com", "sending_status": "verified"}


def test_domain_sending_failed():
    e = _construct("domain.sending_failed.json")
    assert is_domain_sending_failed(e)
    assert e.data["sending_status"] == "failed"
    assert e.data["reason"] == "DKIM tokens not found in DNS"


def test_domain_suppression_added():
    e = _construct("domain.suppression_added.json")
    assert is_domain_suppression_added(e)
    d = e.data
    assert d["address"] == "bob@customer.example.com"
    assert d["source"] == "bounce"
    assert d["message_id"].startswith("msg_")


def test_unknown_event_type_still_parses():
    raw = json.dumps({
        "type": "email.future_kind",
        "id": "evt_x",
        "schema_version": "1",
        "created_at": "2026-07-01T10:30:00Z",
        "data": {"anything": True},
    })
    t = str(int(time.time()))
    sig = hmac.new(SECRET.encode(), f"{t}.{raw}".encode(), hashlib.sha256).hexdigest()
    e = construct_event(raw, f"t={t},v1={sig}", SECRET)
    assert e.type == "email.future_kind"
    assert not is_email_received(e)
    assert e.data == {"anything": True}


# ── Minimal (required-fields-only) fixtures ─────────────────────────────
# The same files the server's presence-semantics lock generates. Parsing
# them proves every optional field is genuinely optional (ABSENT, not
# null/empty) and every required field is present even on the sparsest
# real payload.


def test_email_received_minimal():
    e = _construct("email.received.min.json")
    assert is_email_received(e)
    d = e.data
    assert d["message_id"].startswith("msg_")
    assert d["delivered_to"] == "support@agents.example.com"
    # Required present-but-empty, never absent.
    assert d["authenticated_from"] == ""
    assert d["auth_headers"] == {}
    assert d["to"] == ["support@agents.example.com"]
    # Optional fields are ABSENT on the wire.
    assert "conversation_id" not in d
    assert "cc" not in d
    assert "reply_to" not in d
    assert "attachments" not in d


def test_email_sent_minimal():
    e = _construct("email.sent.min.json")
    assert is_email_sent(e)
    d = e.data
    assert d["provider_message_id"]
    assert "conversation_id" not in d
    assert "cc" not in d
    assert "bcc" not in d


def test_email_failed_minimal():
    e = _construct("email.failed.min.json")
    assert is_email_failed(e)
    d = e.data
    assert d["reason"] == "550 5.1.1 user unknown"
    assert "conversation_id" not in d
    assert "cc" not in d
    assert "bcc" not in d
    assert "reason_code" not in d
    assert "retryable" not in d


def test_email_delivered_minimal():
    e = _construct("email.delivered.min.json")
    assert is_email_delivered(e)
    d = e.data
    assert d["delivered_to"] == "alice@customer.example.com"
    assert "subject" not in d
    assert "smtp_detail" not in d


def test_email_bounced_minimal():
    e = _construct("email.bounced.min.json")
    assert is_email_bounced(e)
    d = e.data
    # The required classification stays even on the sparsest bounce.
    assert d["bounce_type"] == "permanent"
    assert "subject" not in d
    assert "smtp_detail" not in d
    assert "bounce_sub_type" not in d


def test_email_complained_minimal():
    e = _construct("email.complained.min.json")
    assert is_email_complained(e)
    d = e.data
    assert d["delivered_to"] == "carol@customer.example.com"
    assert "subject" not in d
    assert "smtp_detail" not in d


def test_domain_sending_failed_minimal():
    e = _construct("domain.sending_failed.min.json")
    assert is_domain_sending_failed(e)
    assert e.data["sending_status"] == "failed"
    assert "reason" not in e.data


def test_domain_suppression_added_minimal():
    e = _construct("domain.suppression_added.min.json")
    assert is_domain_suppression_added(e)
    d = e.data
    assert d["address"] == "bob@customer.example.com"
    assert d["source"] == "bounce"
    assert "reason" not in d
    assert "message_id" not in d

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
import typing
from pathlib import Path

import pytest
from pydantic import ValidationError
from typing_extensions import is_typeddict

from e2a.v1 import Authentication, DKIMResult, DMARCResult, SPFResult
from e2a.v1.generated.models import MessageLifecycleTransition

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


def test_public_authentication_types_are_wire_typed_dicts():
    assert all(is_typeddict(t) for t in (Authentication, SPFResult, DKIMResult, DMARCResult))
    status = typing.get_type_hints(SPFResult)["status"]
    assert "policy" not in typing.get_args(status)


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
    assert d["header_from"] == "alice@customer.example.com"
    assert d["verified_domain"] == "customer.example.com"
    assert d["envelope_from"] == "bounce@customer.example.com"
    assert d["to"] == ["support@agents.example.com"]
    assert d["delivered_to"] == "support@agents.example.com"
    assert d["subject"] == "Order #1234 delayed"
    assert d["authentication"]["dmarc"]["status"] == "pass"
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


@pytest.mark.parametrize(
    "fixture_name",
    [
        "email.received.json",
        "email.sent.json",
        "email.failed.json",
        "email.delivered.json",
        "email.bounced.json",
        "email.complained.json",
        "domain.suppression_added.json",
    ],
)
def test_stable_mapped_payloads_parse_generated_lifecycle_models(fixture_name):
    event = _construct(fixture_name)
    transitions = event.data["lifecycle_transitions"]
    assert transitions
    assert all(isinstance(row, MessageLifecycleTransition) for row in transitions)
    first = transitions[0]
    assert first.evidence is not None
    assert first.correlation_ids is not None
    assert first.reconstructed is False


@pytest.mark.parametrize(
    "fixture_name",
    [
        "email.received.min.json",
        "email.sent.min.json",
        "email.failed.min.json",
        "email.delivered.min.json",
        "email.bounced.min.json",
        "email.complained.min.json",
        "domain.suppression_added.min.json",
    ],
)
def test_stable_payloads_remain_backward_compatible_without_lifecycle(fixture_name):
    assert "lifecycle_transitions" not in _construct(fixture_name).data


def test_lifecycle_payload_preserves_nullable_recipient_open_maps_and_reconstruction():
    payload = json.loads((FIXTURE_DIR / "email.received.min.json").read_text())
    payload["data"]["lifecycle_transitions"] = [
        {
            "id": "mlt_recon_1",
            "message_id": payload["data"]["message_id"],
            "direction": "inbound",
            "recipient": None,
            "stage": "accepted",
            "outcome": "accepted",
            "reason_code": "acceptance.inbound_smtp",
            "retryable": False,
            "evidence": {"source": "message", "future": {"nested": True}},
            "correlation_ids": {"future_id": "future_1"},
            "occurred_at": "2026-07-22T00:00:00Z",
            "reconstructed": True,
        }
    ]
    raw = json.dumps(payload)
    t = str(int(time.time()))
    sig = hmac.new(SECRET.encode(), f"{t}.{raw}".encode(), hashlib.sha256).hexdigest()
    event = construct_event(raw, f"t={t},v1={sig}", SECRET)
    assert is_email_received(event)
    transition = event.data["lifecycle_transitions"][0]
    assert isinstance(transition, MessageLifecycleTransition)
    assert transition.recipient is None
    assert transition.evidence["future"] == {"nested": True}
    assert transition.correlation_ids["future_id"] == "future_1"
    assert transition.reconstructed is True
    assert isinstance(event.raw["data"]["lifecycle_transitions"][0], dict)


@pytest.mark.parametrize("field", ["direction", "stage", "outcome", "reason_code"])
def test_lifecycle_payload_rejects_unknown_closed_values(field):
    values = {
        "id": "mlt_1",
        "message_id": "msg_1",
        "direction": "inbound",
        "stage": "accepted",
        "outcome": "accepted",
        "reason_code": "acceptance.inbound_smtp",
        "retryable": False,
        "evidence": {},
        "correlation_ids": {},
        "occurred_at": "2026-07-22T00:00:00Z",
        "reconstructed": False,
    }
    values[field] = "future_unknown_value"
    payload = json.loads((FIXTURE_DIR / "email.received.min.json").read_text())
    payload["data"]["lifecycle_transitions"] = [values]
    raw = json.dumps(payload)
    t = str(int(time.time()))
    sig = hmac.new(SECRET.encode(), f"{t}.{raw}".encode(), hashlib.sha256).hexdigest()
    with pytest.raises(ValidationError, match=field):
        construct_event(raw, f"t={t},v1={sig}", SECRET)


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


def test_future_envelope_version_stays_generic():
    raw = json.dumps({
        "type": "email.received",
        "id": "evt_v2",
        "schema_version": "2",
        "created_at": "2030-01-02T03:04:05Z",
        "data": {
            "future": True,
            "lifecycle_transitions": [
                {
                    "id": "future_1",
                    "message_id": "msg_future",
                    "direction": "sideways",
                    "stage": "future_stage",
                    "outcome": "future_outcome",
                    "reason_code": "future.reason",
                    "retryable": "evolved",
                    "evidence": {"future": True},
                    "correlation_ids": {},
                    "occurred_at": "future-time",
                    "reconstructed": "maybe",
                }
            ],
        },
        "future_envelope_field": True,
    })
    t = str(int(time.time()))
    sig = hmac.new(SECRET.encode(), f"{t}.{raw}".encode(), hashlib.sha256).hexdigest()
    e = construct_event(raw, f"t={t},v1={sig}", SECRET)
    assert e.schema_version == "2"
    assert e.raw["future_envelope_field"] is True
    assert e.data is e.raw["data"]
    assert e.data["lifecycle_transitions"][0]["stage"] == "future_stage"
    assert not is_email_received(e)


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
    # Required nullable authentication/identity fields remain present.
    assert d["header_from"] is None
    assert d["verified_domain"] is None
    assert d["envelope_from"] is None
    assert d["authentication"] is None
    assert d["to"] == ["support@agents.example.com"]
    assert d["cc"] == []
    assert d["reply_to"] == []
    # Optional fields are ABSENT on the wire.
    assert "conversation_id" not in d
    assert "attachments" not in d
    assert "lifecycle_transitions" not in d
    assert d is e.raw["data"]


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

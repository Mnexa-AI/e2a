"""Unit tests for verify_webhook_signature + construct_event."""

import hashlib
import hmac
import json
import time

import pytest

from e2a.v1 import construct_event, verify_webhook_signature
from e2a.v1.errors import E2AWebhookSignatureError


SECRET = "whsec_test1234567890abcdef"


def _sign(secret: str, t: str, body: str) -> str:
    return hmac.new(
        secret.encode("utf-8"),
        f"{t}.{body}".encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()


def test_accepts_valid_signature() -> None:
    body = '{"event":"email.received"}'
    t = str(int(time.time()))
    v1 = _sign(SECRET, t, body)
    assert verify_webhook_signature(
        body, f"t={t},v1={v1}", SECRET
    )


def test_rejects_tampered_body() -> None:
    body = '{"event":"email.received"}'
    t = str(int(time.time()))
    v1 = _sign(SECRET, t, body)
    assert not verify_webhook_signature(
        '{"event":"email.received","tampered":true}',
        f"t={t},v1={v1}",
        SECRET,
    )


def test_rejects_wrong_secret() -> None:
    body = "{}"
    t = str(int(time.time()))
    v1 = _sign(SECRET, t, body)
    assert not verify_webhook_signature(
        body, f"t={t},v1={v1}", "whsec_wrongkey"
    )


def test_rejects_old_timestamp_outside_tolerance() -> None:
    body = "{}"
    now = 1_700_000_000.0
    t = str(int(now - 600))  # 10 min ago
    v1 = _sign(SECRET, t, body)
    assert not verify_webhook_signature(
        body, f"t={t},v1={v1}", SECRET, now=now
    )


def test_accepts_either_v1_during_rotation_grace() -> None:
    body = "{}"
    t = str(int(time.time()))
    old_secret = "whsec_old"
    new_secret = "whsec_new"
    v1_old = _sign(old_secret, t, body)
    v1_new = _sign(new_secret, t, body)
    header = f"t={t},v1={v1_old},v1={v1_new}"
    assert verify_webhook_signature(body, header, old_secret)
    assert verify_webhook_signature(body, header, new_secret)


def test_rejects_nan_timestamp() -> None:
    # Regression: float("nan") parses, and abs(now - nan) > tol is False, which
    # would silently disable replay protection. Even a correctly-signed t=nan
    # must be rejected.
    body = "{}"
    t = "nan"
    v1 = _sign(SECRET, t, body)
    assert not verify_webhook_signature(body, f"t={t},v1={v1}", SECRET)
    assert not verify_webhook_signature(body, f"t=inf,v1={_sign(SECRET, 'inf', body)}", SECRET)


def test_rejects_malformed_header() -> None:
    assert not verify_webhook_signature("{}", "", SECRET)
    assert not verify_webhook_signature("{}", "t=123", SECRET)
    assert not verify_webhook_signature("{}", "v1=abc", SECRET)


def test_accepts_any_secret_in_a_list() -> None:
    body = "{}"
    t = str(int(time.time()))
    v1 = _sign("whsec_b", t, body)
    header = f"t={t},v1={v1}"
    assert verify_webhook_signature(body, header, ["whsec_a", "whsec_b", "whsec_c"])
    assert not verify_webhook_signature(body, header, ["whsec_a", "whsec_x"])


def test_empty_secret_list_rejects() -> None:
    body = "{}"
    t = str(int(time.time()))
    v1 = _sign(SECRET, t, body)
    assert not verify_webhook_signature(body, f"t={t},v1={v1}", [])


# ── construct_event ──────────────────────────────────────────────


def test_construct_event_verifies_and_parses() -> None:
    body = json.dumps({"id": "evt_1", "type": "email.received", "data": {"message_id": "msg_1"}})
    t = str(int(time.time()))
    header = f"t={t},v1={_sign(SECRET, t, body)}"
    event = construct_event(body, header, SECRET)
    assert event.type == "email.received"
    assert event.id == "evt_1"
    assert event.data == {"message_id": "msg_1"}
    assert event.raw["type"] == "email.received"


def test_construct_event_rejects_bad_signature() -> None:
    body = json.dumps({"type": "email.received"})
    t = str(int(time.time()))
    header = f"t={t},v1={_sign('whsec_wrong', t, body)}"
    with pytest.raises(E2AWebhookSignatureError):
        construct_event(body, header, SECRET)


def test_construct_event_rejects_replay() -> None:
    body = json.dumps({"type": "email.received"})
    now = 1_700_000_000.0
    t = str(int(now - 600))
    header = f"t={t},v1={_sign(SECRET, t, body)}"
    with pytest.raises(E2AWebhookSignatureError):
        construct_event(body, header, SECRET, now=now)


def test_construct_event_rejects_non_json() -> None:
    body = "not json"
    t = str(int(time.time()))
    header = f"t={t},v1={_sign(SECRET, t, body)}"
    with pytest.raises(E2AWebhookSignatureError, match="not valid JSON"):
        construct_event(body, header, SECRET)


def test_construct_event_rejects_missing_type() -> None:
    body = json.dumps({"data": {}})
    t = str(int(time.time()))
    header = f"t={t},v1={_sign(SECRET, t, body)}"
    with pytest.raises(E2AWebhookSignatureError, match="missing a string"):
        construct_event(body, header, SECRET)


def test_construct_event_accepts_bytes_body() -> None:
    body = json.dumps({"type": "email.sent"})
    t = str(int(time.time()))
    header = f"t={t},v1={_sign(SECRET, t, body)}"
    event = construct_event(body.encode("utf-8"), header, SECRET)
    assert event.type == "email.sent"

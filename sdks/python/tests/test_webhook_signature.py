"""Unit tests for verify_webhook_signature."""

import hashlib
import hmac
import time

from e2a.v1 import verify_webhook_signature


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


def test_rejects_malformed_header() -> None:
    assert not verify_webhook_signature("{}", "", SECRET)
    assert not verify_webhook_signature("{}", "t=123", SECRET)
    assert not verify_webhook_signature("{}", "v1=abc", SECRET)

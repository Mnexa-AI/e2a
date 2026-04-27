"""Tests for e2a.v1.handler — MIME parsing, InboundEmail, value types."""

import base64
import json
from unittest.mock import MagicMock

import pytest

from e2a.v1.handler import (
    Attachment,
    AuthHeaders,
    InboundEmail,
    SendResult,
    build_inbound_email,
    parse_raw_email,
)


# ── Helpers ───────────────────────────────────────────────────────


def _make_raw_email(subject="Hello", text="Hi there", html=None):
    lines = [
        "From: alice@gmail.com",
        "To: bot@agent.example.com",
        f"Subject: {subject}",
        "MIME-Version: 1.0",
    ]
    if html:
        boundary = "boundary123"
        lines.append(f'Content-Type: multipart/alternative; boundary="{boundary}"')
        lines.append("")
        lines.append(f"--{boundary}")
        lines.append("Content-Type: text/plain; charset=utf-8")
        lines.append("")
        lines.append(text)
        lines.append(f"--{boundary}")
        lines.append("Content-Type: text/html; charset=utf-8")
        lines.append("")
        lines.append(html)
        lines.append(f"--{boundary}--")
    else:
        lines.append("Content-Type: text/plain; charset=utf-8")
        lines.append("")
        lines.append(text)
    return "\r\n".join(lines).encode()


def _make_raw_email_with_attachment(
    subject="Hello", text="See attached", filename="test.pdf",
    content_type="application/pdf", file_data=b"fake-pdf-data",
):
    boundary = "mixed_boundary"
    encoded = base64.b64encode(file_data).decode()
    lines = [
        "From: alice@gmail.com",
        "To: bot@agent.example.com",
        f"Subject: {subject}",
        "MIME-Version: 1.0",
        f'Content-Type: multipart/mixed; boundary="{boundary}"',
        "",
        f"--{boundary}",
        "Content-Type: text/plain; charset=utf-8",
        "",
        text,
        f"--{boundary}",
        f"Content-Type: {content_type}",
        f'Content-Disposition: attachment; filename="{filename}"',
        "Content-Transfer-Encoding: base64",
        "",
        encoded,
        f"--{boundary}--",
    ]
    return "\r\n".join(lines).encode()


def _make_webhook_data(raw_email, message_id="msg_123", conversation_id=None,
                       created_at=None, received_at=None):
    data = {
        "message_id": message_id,
        "from": "alice@gmail.com",
        "to": ["bot@agent.example.com"], "recipient": "bot@agent.example.com",
        "raw_message": base64.b64encode(raw_email).decode(),
        "auth_headers": {
            "X-E2A-Auth-Verified": "true",
            "X-E2A-Auth-Sender": "alice@gmail.com",
            "X-E2A-Auth-Entity-Type": "human",
            "X-E2A-Auth-Domain-Check": "spf=pass dkim=pass",
            "X-E2A-Auth-Delegation": "agent=ag_1;human=hu_1",
            "X-E2A-Auth-Signature": "sha256=abc",
            "X-E2A-Auth-Timestamp": "2026-03-30T10:00:00Z",
        },
    }
    if conversation_id:
        data["conversation_id"] = conversation_id
    if created_at:
        data["created_at"] = created_at
    if received_at:
        data["received_at"] = received_at
    return data


def _mock_client():
    return MagicMock()


# ── parse_raw_email ───────────────────────────────────────────────


def test_parse_plain_text():
    raw = _make_raw_email(subject="Test", text="Hello agent")
    subject, text, html, atts = parse_raw_email(raw)
    assert subject == "Test"
    assert text == "Hello agent"
    assert html is None
    assert atts == []


def test_parse_multipart_plain_html():
    raw = _make_raw_email(subject="Rich", text="Plain text", html="<p>HTML</p>")
    subject, text, html, atts = parse_raw_email(raw)
    assert text == "Plain text"
    assert html == "<p>HTML</p>"


def test_parse_single_attachment():
    file_data = b"fake-pdf-content"
    raw = _make_raw_email_with_attachment(
        text="See attached", filename="report.pdf",
        content_type="application/pdf", file_data=file_data,
    )
    subject, text, html, atts = parse_raw_email(raw)
    assert text == "See attached"
    assert len(atts) == 1
    assert atts[0].filename == "report.pdf"
    assert atts[0].content_type == "application/pdf"
    assert atts[0].data == file_data
    assert atts[0].size == len(file_data)


def test_parse_multiple_attachments():
    boundary = "multi_att"
    att1_data = base64.b64encode(b"file-one").decode()
    att2_data = base64.b64encode(b"file-two").decode()
    raw = "\r\n".join([
        "From: alice@gmail.com",
        "To: bot@agent.example.com",
        "Subject: Multi",
        "MIME-Version: 1.0",
        f'Content-Type: multipart/mixed; boundary="{boundary}"',
        "",
        f"--{boundary}",
        "Content-Type: text/plain; charset=utf-8",
        "",
        "Two files.",
        f"--{boundary}",
        "Content-Type: image/png",
        f'Content-Disposition: attachment; filename="photo.png"',
        "Content-Transfer-Encoding: base64",
        "",
        att1_data,
        f"--{boundary}",
        "Content-Type: application/zip",
        f'Content-Disposition: attachment; filename="archive.zip"',
        "Content-Transfer-Encoding: base64",
        "",
        att2_data,
        f"--{boundary}--",
    ]).encode()
    _, text, _, atts = parse_raw_email(raw)
    assert text == "Two files."
    assert len(atts) == 2
    assert atts[0].filename == "photo.png"
    assert atts[0].data == b"file-one"
    assert atts[1].filename == "archive.zip"
    assert atts[1].data == b"file-two"


def test_parse_empty_bytes():
    subject, text, html, atts = parse_raw_email(b"")
    assert subject == ""
    assert text == ""
    assert html is None
    assert atts == []


def test_build_inbound_email_uses_structured_to_cc():
    """The SDK trusts the server's structured `to`/`cc`/`recipient` fields
    rather than re-parsing the raw RFC 2822 headers."""
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    data["to"] = ["bot@agent.example.com", "other-bot@agent.example.com"]
    data["cc"] = ["watcher@example.com"]
    data["recipient"] = "bot@agent.example.com"
    email = build_inbound_email(data, _mock_client(), trusted=True)
    assert email.to == ["bot@agent.example.com", "other-bot@agent.example.com"]
    assert email.cc == ["watcher@example.com"]
    assert email.recipient == "bot@agent.example.com"


# ── build_inbound_email ──────────────────────────────────────────


def test_build_inbound_email_basic():
    raw = _make_raw_email(subject="Test", text="Hello")
    data = _make_webhook_data(raw)
    email = build_inbound_email(data, _mock_client(), trusted=True)

    assert email.message_id == "msg_123"
    assert email.sender == "alice@gmail.com"
    assert email.recipient == "bot@agent.example.com"
    assert email.subject == "Test"
    assert email.text_body == "Hello"
    assert email.html_body is None
    assert email.conversation_id is None
    assert email.is_verified is True
    assert email.auth.entity_type == "human"
    assert email.auth.delegation == "agent=ag_1;human=hu_1"
    assert email.auth.signature == "sha256=abc"
    assert email.auth.timestamp == "2026-03-30T10:00:00Z"


def test_build_inbound_email_with_conversation_id():
    raw = _make_raw_email()
    data = _make_webhook_data(raw, conversation_id="conv_abc")
    email = build_inbound_email(data, _mock_client(), trusted=True)
    assert email.conversation_id == "conv_abc"


def test_received_at_from_created_at():
    raw = _make_raw_email()
    data = _make_webhook_data(raw, created_at="2026-03-30T10:00:00Z")
    email = build_inbound_email(data, _mock_client(), trusted=True)
    assert email.received_at == "2026-03-30T10:00:00Z"


def test_received_at_from_received_at():
    raw = _make_raw_email()
    data = _make_webhook_data(raw, received_at="2026-03-30T11:00:00Z")
    email = build_inbound_email(data, _mock_client(), trusted=True)
    assert email.received_at == "2026-03-30T11:00:00Z"


def test_received_at_prefers_created_at():
    raw = _make_raw_email()
    data = _make_webhook_data(raw, created_at="2026-03-30T10:00:00Z", received_at="2026-03-30T11:00:00Z")
    email = build_inbound_email(data, _mock_client(), trusted=True)
    assert email.received_at == "2026-03-30T10:00:00Z"


def test_received_at_none_when_absent():
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    email = build_inbound_email(data, _mock_client(), trusted=True)
    assert email.received_at is None


# ── AuthHeaders ──────────────────────────────────────────────────


def test_auth_headers_parsing():
    headers = {
        "X-E2A-Auth-Verified": "true",
        "X-E2A-Auth-Sender": "alice@example.com",
        "X-E2A-Auth-Entity-Type": "human",
        "X-E2A-Auth-Domain-Check": "spf=pass",
        "X-E2A-Auth-Delegation": "agent=ag_1;human=hu_1",
        "X-E2A-Auth-Signature": "sha256=abc123",
        "X-E2A-Auth-Timestamp": "2026-03-30T10:00:00Z",
    }
    auth = AuthHeaders.from_headers(headers)
    assert auth.verified is True
    assert auth.sender == "alice@example.com"
    assert auth.entity_type == "human"
    assert auth.domain_check == "spf=pass"
    assert auth.delegation == "agent=ag_1;human=hu_1"
    assert auth.signature == "sha256=abc123"
    assert auth.timestamp == "2026-03-30T10:00:00Z"


def test_auth_headers_empty():
    auth = AuthHeaders.from_headers({})
    assert auth.verified is False
    assert auth.sender == ""


# ── verify_signature ─────────────────────────────────────────────


def _signed_email(*, secret: str, body: bytes = b"hello world",
                  message_id: str = "msg_abc",
                  sender: str = "alice@example.com",
                  domain_check: str = "spf=pass; dkim=pass",
                  ts: str | None = None):
    """Build an InboundEmail whose auth headers were signed with `secret`,
    bound to `body` and `message_id`. Returns (email, headers_dict).
    Used so tests can mutate fields and confirm verification fails.
    """
    import hmac as _hmac
    import hashlib as _hashlib
    from datetime import datetime, timezone

    if ts is None:
        ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    body_hash = _hashlib.sha256(body).hexdigest()
    canonical = "\n".join([
        "true", sender, "human", domain_check, "",
        ts, message_id, body_hash,
    ])
    sig = _hmac.new(secret.encode(), canonical.encode(), _hashlib.sha256).hexdigest()
    headers = {
        "X-E2A-Auth-Verified": "true",
        "X-E2A-Auth-Sender": sender,
        "X-E2A-Auth-Entity-Type": "human",
        "X-E2A-Auth-Domain-Check": domain_check,
        "X-E2A-Auth-Timestamp": ts,
        "X-E2A-Auth-Message-Id": message_id,
        "X-E2A-Auth-Body-Hash": body_hash,
        "X-E2A-Auth-Signature": sig,
    }
    data = {
        "message_id": message_id,
        "from": sender, "to": ["bot@agent.example.com"], "recipient": "bot@agent.example.com",
        "raw_message": base64.b64encode(body).decode(),
        "auth_headers": headers,
    }
    return build_inbound_email(data, _mock_client(), trusted=True), headers


def _email_with(headers, body=b"hello world"):
    data = {
        "message_id": "msg_abc",
        "from": "alice@example.com", "to": ["bot@agent.example.com"], "recipient": "bot@agent.example.com",
        "raw_message": base64.b64encode(body).decode(),
        "auth_headers": headers,
    }
    return build_inbound_email(data, _mock_client(), trusted=True)


def test_verify_signature_legit():
    email, _ = _signed_email(secret="x" * 32)
    assert email.verify_signature("x" * 32) is True


def test_verify_signature_wrong_secret():
    email, _ = _signed_email(secret="x" * 32)
    assert email.verify_signature("y" * 32) is False


def test_verify_signature_tampered_body_hash():
    _, headers = _signed_email(secret="x" * 32)
    headers["X-E2A-Auth-Body-Hash"] = "0" * 64
    assert _email_with(headers).verify_signature("x" * 32) is False


def test_verify_signature_tampered_sender():
    _, headers = _signed_email(secret="x" * 32)
    headers["X-E2A-Auth-Sender"] = "eve@evil.com"
    assert _email_with(headers).verify_signature("x" * 32) is False


def test_verify_signature_tampered_message_id():
    _, headers = _signed_email(secret="x" * 32)
    headers["X-E2A-Auth-Message-Id"] = "msg_attacker"
    assert _email_with(headers).verify_signature("x" * 32) is False


def test_verify_signature_modified_body():
    _, headers = _signed_email(secret="x" * 32, body=b"original")
    # body_hash header signed for "original" but bytes received are "forged"
    assert _email_with(headers, body=b"forged").verify_signature("x" * 32) is False


def test_verify_signature_expired_timestamp():
    email, _ = _signed_email(secret="x" * 32, ts="2020-01-01T00:00:00Z")
    assert email.verify_signature("x" * 32) is False


def test_verify_signature_missing_signature():
    _, headers = _signed_email(secret="x" * 32)
    headers["X-E2A-Auth-Signature"] = ""
    assert _email_with(headers).verify_signature("x" * 32) is False


# ── InboundEmail.reply ───────────────────────────────────────────


def test_inbound_email_reply_delegates():
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    mock_client = _mock_client()
    mock_client.reply.return_value = SendResult(status="sent", message_id="r1", method="smtp")

    email = build_inbound_email(data, mock_client, trusted=True)
    result = email.reply("Thanks!")

    mock_client.reply.assert_called_once_with(
        "msg_123", "Thanks!",
        html_body=None, reply_all=None, cc=None, bcc=None,
        conversation_id=None, attachments=None,
        agent_email="bot@agent.example.com",
    )
    assert result.status == "sent"


# ── repr ─────────────────────────────────────────────────────────


def test_inbound_email_repr():
    raw = _make_raw_email(subject="Hello")
    data = _make_webhook_data(raw, conversation_id="conv_1")
    email = build_inbound_email(data, _mock_client(), trusted=True)

    r = repr(email)
    assert "msg_123" in r
    assert "alice@gmail.com" in r
    assert "Hello" in r
    assert "conv_1" in r


# --- Strict-verify gate (PR D / SDK 2.0) ---

def test_unverified_email_raises_on_field_access():
    """Default state of parse() output: claim fields raise UnverifiedEmailError."""
    from e2a.v1 import UnverifiedEmailError
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    email = build_inbound_email(data, _mock_client())  # NOT trusted=True

    for attr in ["sender", "recipient", "to", "cc", "subject", "text_body", "html_body", "attachments", "message_id", "conversation_id", "received_at"]:
        with pytest.raises(UnverifiedEmailError):
            getattr(email, attr)


def test_unverified_email_allows_verify_inputs():
    """auth, raw_message, is_verified, verified, unverified_payload work without verify."""
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    email = build_inbound_email(data, _mock_client())

    # These must NOT raise — verify() needs auth + raw_message.
    assert email.auth is not None
    assert email.raw_message == raw
    assert email.is_verified is True  # the server's claim, not a check
    assert email.verified is False  # the cryptographic state — not yet verified

    # unverified_payload is the documented escape hatch.
    payload = email.unverified_payload
    assert payload["sender"] == "alice@gmail.com"
    assert payload["subject"] == "Hello"


def test_verify_signature_with_no_secret_and_no_env_raises(monkeypatch):
    """No secret + no env → ValueError (better than silent False)."""
    monkeypatch.delenv("E2A_HMAC_SECRET", raising=False)
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    email = build_inbound_email(data, _mock_client())

    with pytest.raises(ValueError, match="E2A_HMAC_SECRET"):
        email.verify_signature()


def test_verify_signature_reads_env_when_no_arg(monkeypatch):
    """Default secret comes from E2A_HMAC_SECRET when not passed."""
    monkeypatch.setenv("E2A_HMAC_SECRET", "wrong-secret-but-not-empty")
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    email = build_inbound_email(data, _mock_client())

    # The fixture isn't HMAC-signed with this secret, so verify returns
    # False (not raises) — the assertion here is that it CALLED verify
    # (didn't raise the no-secret ValueError).
    assert email.verify_signature() is False


def test_verify_signature_unlocks_field_access_on_success():
    """A successful verify_signature() flips _verified and unlocks fields."""
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    email = build_inbound_email(data, _mock_client())

    # Stub the verify primitive to simulate a successful verification.
    import e2a.v1.handler as h
    orig = h._verify_auth_headers
    h._verify_auth_headers = lambda *a, **kw: True
    try:
        ok = email.verify_signature("any-secret")
    finally:
        h._verify_auth_headers = orig

    assert ok is True
    assert email.verified is True
    # No longer raises:
    assert email.sender == "alice@gmail.com"


def test_verify_signature_failure_keeps_email_locked():
    """A failing verify must NOT flip _verified."""
    from e2a.v1 import UnverifiedEmailError
    raw = _make_raw_email()
    data = _make_webhook_data(raw)
    email = build_inbound_email(data, _mock_client())

    import e2a.v1.handler as h
    orig = h._verify_auth_headers
    h._verify_auth_headers = lambda *a, **kw: False
    try:
        ok = email.verify_signature("wrong-secret")
    finally:
        h._verify_auth_headers = orig

    assert ok is False
    assert email.verified is False
    with pytest.raises(UnverifiedEmailError):
        _ = email.sender

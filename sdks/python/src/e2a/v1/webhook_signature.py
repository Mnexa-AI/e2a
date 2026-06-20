"""Webhook signature verification for the /v1 webhooks resource.

e2a signs each subscriber delivery with HMAC-SHA256 keyed by the webhook's
per-webhook ``signing_secret`` (``whsec_...``), Stripe-style::

    X-E2A-Signature: t=<unix>,v1=<hex>[,v1=<hex>]

During the 24h rotation grace each delivery carries two ``v1=`` pairs (one per
active secret); accept if ANY matches. The timestamp guards replay — reject
anything older than the tolerance (default 5 min). The signed payload is
``f"{t}.{raw_body}"`` — pass the RAW request body bytes; re-stringifying parsed
JSON will not match (whitespace / key-order differ).
"""

from __future__ import annotations

import hashlib
import hmac
import json
import math
import time
from dataclasses import dataclass, field
from typing import Any, Mapping, Optional, Sequence, Union

from .errors import E2AWebhookSignatureError

__all__ = ["verify_webhook_signature", "construct_event", "WebhookEvent"]

Secret = Union[str, Sequence[str]]


def verify_webhook_signature(
    raw_body: Union[str, bytes],
    header: str,
    secret: Secret,
    *,
    tolerance_seconds: int = 300,
    now: Optional[float] = None,
) -> bool:
    """Verify an ``X-E2A-Signature`` header.

    Returns ``True`` on success and ``False`` on any failure (bad format,
    missing pair, signature mismatch, replay). Never raises.

    ``secret`` may be a single ``whsec_...`` string or a sequence of them (to
    verify one handler against several endpoints' secrets / a rotation grace).
    """
    # Guard a missing/non-string header so a missing X-E2A-Signature is a clean
    # False, never a raw AttributeError (WH-SIG-1).
    if not header or not isinstance(header, str):
        return False
    t = ""
    v1s: list[str] = []
    for part in header.split(","):
        trimmed = part.strip()
        if trimmed.startswith("t="):
            t = trimmed[2:]
        elif trimmed.startswith("v1="):
            v1s.append(trimmed[3:])
    if not t or not v1s:
        return False

    try:
        ts = float(t)
    except ValueError:
        return False
    # Reject non-finite timestamps: abs(now - nan) > tol is False, which would
    # silently disable the replay guard for a t=nan delivery.
    if not math.isfinite(ts):
        return False
    if now is None:
        now = time.time()
    if abs(now - ts) > tolerance_seconds:
        return False

    body_bytes = raw_body.encode("utf-8") if isinstance(raw_body, str) else raw_body
    signed_payload = t.encode("ascii") + b"." + body_bytes

    secrets = [secret] if isinstance(secret, str) else list(secret)
    for sec in secrets:
        expected = hmac.new(sec.encode("utf-8"), signed_payload, hashlib.sha256).hexdigest()
        for candidate in v1s:
            if len(candidate) != len(expected):
                continue
            if hmac.compare_digest(candidate, expected):
                return True
    return False


@dataclass(frozen=True)
class WebhookEvent:
    """A verified webhook event. ``data`` is the per-event payload (typed once
    the server emits per-type schemas — a tracked follow-up); narrow on
    ``type``."""

    type: str
    data: Any = None
    id: Optional[str] = None
    created_at: Optional[str] = None
    #: The full parsed envelope (all fields, for forward-compatibility).
    raw: Mapping[str, Any] = field(default_factory=dict)


def construct_event(
    raw_body: Union[str, bytes],
    header: str,
    secret: Secret,
    *,
    tolerance_seconds: int = 300,
    now: Optional[float] = None,
) -> WebhookEvent:
    """Verify a delivery and parse it into a :class:`WebhookEvent` in one call
    (Stripe's ``construct_event`` shape). Raises
    :class:`~e2a.v1.errors.E2AWebhookSignatureError` on a bad signature, a replay
    outside tolerance, or an unparseable body. Recommended path — call it from
    your webhook handler with the RAW request body.
    """
    if not verify_webhook_signature(
        raw_body, header, secret, tolerance_seconds=tolerance_seconds, now=now
    ):
        raise _sig_error("webhook_signature_invalid", "webhook signature verification failed")

    text = raw_body.decode("utf-8") if isinstance(raw_body, bytes) else raw_body
    try:
        parsed = json.loads(text)
    except (ValueError, TypeError):
        raise _sig_error("webhook_body_invalid", "webhook body is not valid JSON")
    if not isinstance(parsed, dict) or not isinstance(parsed.get("type"), str):
        raise _sig_error("webhook_body_invalid", "webhook event is missing a string `type`")

    return WebhookEvent(
        type=parsed["type"],
        data=parsed.get("data"),
        id=parsed.get("id"),
        created_at=parsed.get("created_at"),
        raw=parsed,
    )


def _sig_error(code: str, message: str) -> E2AWebhookSignatureError:
    return E2AWebhookSignatureError(code=code, message=message, status=0, retryable=False)

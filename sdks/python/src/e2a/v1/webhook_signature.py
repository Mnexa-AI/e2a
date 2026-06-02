"""Webhook signature verification for the top-level webhooks resource.

e2a signs each subscriber delivery with HMAC-SHA256 keyed by the
webhook's ``signing_secret``. The signature is sent on the
``X-E2A-Signature`` header in Stripe-style format::

    X-E2A-Signature: t=<unix>,v1=<hex>[,v1=<hex>]

During the 24h rotation grace window each delivery carries two
``v1=`` pairs (one per active secret). Receivers should accept the
request if ANY of the ``v1=`` signatures matches. The timestamp
guards against replay — reject anything older than the configured
tolerance (default 5 min).

The signed payload is ``f"{t}.{raw_body}"``. Pass the raw request
body bytes — re-serializing parsed JSON will not match because of
whitespace and key-order differences.
"""

from __future__ import annotations

import hashlib
import hmac
import time
from typing import Optional, Union


def verify_webhook_signature(
    raw_body: Union[str, bytes],
    header: str,
    secret: str,
    *,
    tolerance_seconds: int = 300,
    now: Optional[float] = None,
) -> bool:
    """Verify an ``X-E2A-Signature`` header.

    Returns ``True`` on success and ``False`` on any failure (bad
    format, missing pair, signature mismatch, replay). Never raises —
    designed for use directly in an HTTP handler's branch decision.

    Args:
        raw_body: Raw HTTP request body bytes (or text).
        header: Value of the ``X-E2A-Signature`` header.
        secret: Webhook signing secret (``whsec_...``).
        tolerance_seconds: Replay window. Defaults to 300 (5 min).
        now: Test-only clock override (unix seconds). Defaults to
            ``time.time()``.
    """
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
    if now is None:
        now = time.time()
    if abs(now - ts) > tolerance_seconds:
        return False

    if isinstance(raw_body, str):
        body_bytes = raw_body.encode("utf-8")
    else:
        body_bytes = raw_body

    signed_payload = t.encode("ascii") + b"." + body_bytes
    expected = hmac.new(
        secret.encode("utf-8"), signed_payload, hashlib.sha256
    ).hexdigest()
    for candidate in v1s:
        if len(candidate) != len(expected):
            continue
        if hmac.compare_digest(candidate, expected):
            return True
    return False

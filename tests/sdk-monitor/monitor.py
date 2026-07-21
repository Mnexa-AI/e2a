"""Continuous production monitor for the *published* e2a Python SDK.

Neither existing validator touches an SDK: `cmd/e2a-prober` speaks raw HTTP and
`tests/e2e-prod` uses zero-dependency `fetch` on purpose. So a broken publish is
invisible — the SDKs sat at 4.0.1 on PyPI/npm while `main` was at 5.2.0 and
nothing caught it. This service closes that gap by driving a real agent-to-agent
round trip through the SDK a user would actually `pip install` (pinned in
requirements.txt, never the workspace source).

Stateless and request-driven — Cloud Run scales to zero, so nothing may live in
memory between requests. Correlation state travels in the message subject:

    POST /tick      send A -> B, subject "probe <nonce>"; nonce embeds send time
    POST /webhook   email.received on B -> reply; email.received on A -> success
    GET  /health    liveness

Success is a structured log line (``sdk_monitor_ok``) carrying the round-trip
latency; ops builds a log-based metric plus a "no success in N minutes" alert on
it. There is no in-process aggregation to lose.
"""

from __future__ import annotations

import json
import os
import re
import secrets
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from e2a.v1 import (
    E2AClient,
    E2AWebhookSignatureError,
    construct_event,
    is_email_received,
)

# Subject carries the correlation state: a marker, the send time in epoch ms
# (so latency needs no storage), and randomness so concurrent probes and
# redelivered events can never be confused for one another.
NONCE_RE = re.compile(r"e2asdkmon\.(\d+)\.[0-9a-f]{16}")
SUBJECT_PREFIX = "probe"

API_KEY = os.environ.get("E2A_API_KEY", "")
BASE_URL = os.environ.get("E2A_BASE_URL", "https://api.e2a.dev")
AGENT_A = os.environ.get("E2A_MONITOR_AGENT_A", "")
AGENT_B = os.environ.get("E2A_MONITOR_AGENT_B", "")
WEBHOOK_SECRET = os.environ.get("E2A_MONITOR_WEBHOOK_SECRET", "")
# A reply older than this is a stale redelivery, not a fresh success — it must
# never refresh the alert's "last seen" clock.
MAX_AGE_MS = int(os.environ.get("E2A_MONITOR_MAX_AGE_MS", "900000"))
PORT = int(os.environ.get("PORT", "8080"))

_client: E2AClient | None = None


def log(event: str, **fields: object) -> None:
    """One JSON line per event on stdout. Never pass a secret in here."""
    print(json.dumps({"event": event, **fields}, default=str), flush=True)


def client() -> E2AClient:
    global _client
    if _client is None:
        _client = E2AClient(api_key=API_KEY, base_url=BASE_URL)
    return _client


def new_nonce() -> str:
    return f"e2asdkmon.{int(time.time() * 1000)}.{secrets.token_hex(8)}"


def handle_tick() -> tuple[int, dict]:
    """Send the outbound leg: agent A -> agent B, nonce in subject and body."""
    nonce = new_nonce()
    try:
        result = client().messages.send(
            AGENT_A,
            {"to": [AGENT_B], "subject": f"{SUBJECT_PREFIX} {nonce}", "text": nonce},
        )
    except Exception as exc:  # noqa: BLE001 - any SDK failure is a monitor signal
        log("sdk_monitor_error", stage="send", nonce=nonce, error=type(exc).__name__, detail=str(exc))
        return 500, {"ok": False, "stage": "send"}
    log(
        "sdk_monitor_tick",
        nonce=nonce,
        message_id=result.message_id,
        status=result.status,
        method=result.method,
    )
    return 202, {"ok": True, "nonce": nonce}


def handle_webhook(raw_body: bytes, signature: str) -> tuple[int, dict]:
    """Verify, hydrate, then act on which leg of the round trip this is."""
    # Fail closed: an unset secret means the deployment is misconfigured, and
    # accepting unverified deliveries would make the whole signal forgeable.
    if not WEBHOOK_SECRET:
        log("sdk_monitor_error", stage="config", detail="webhook secret not configured")
        return 401, {"error": "unauthorized"}
    try:
        event = construct_event(raw_body, signature, WEBHOOK_SECRET)
    except E2AWebhookSignatureError as exc:
        log("sdk_monitor_error", stage="signature", code=getattr(exc, "code", None))
        return 401, {"error": "unauthorized"}

    # An account-scoped subscription fans out email.sent, bounces, domain
    # events — everything that is not an inbound delivery is not our business.
    if not is_email_received(event):
        return 200, {"ok": True, "ignored": event.type}

    try:
        email = client().inbound.from_event(event)
    except Exception as exc:  # noqa: BLE001
        log("sdk_monitor_error", stage="hydrate", event_id=event.id, error=type(exc).__name__, detail=str(exc))
        # 5xx so e2a retries: a transient hydration failure shouldn't silently
        # drop a leg and manufacture a false alert.
        return 500, {"ok": False, "stage": "hydrate"}

    match = NONCE_RE.search(email.subject or "")
    if not match:
        return 200, {"ok": True, "ignored": "not a probe"}
    nonce, sent_ms = match.group(0), int(match.group(1))
    age_ms = int(time.time() * 1000) - sent_ms

    # Which leg this is comes from the recipient, not from a "Re:" prefix:
    # mailers rewrite subjects, but the delivered-to agent is authoritative.
    inbox = (email.inbox or "").lower()

    if inbox == AGENT_B.lower():
        if age_ms > MAX_AGE_MS:
            log("sdk_monitor_stale", leg="outbound", nonce=nonce, age_ms=age_ms)
            return 200, {"ok": True, "stale": True}
        try:
            # idempotency_key keyed on the event so a webhook redelivery can't
            # fan out into duplicate replies.
            email.reply({"text": nonce}, idempotency_key=f"sdkmon:{event.id}")
        except Exception as exc:  # noqa: BLE001
            log("sdk_monitor_error", stage="reply", nonce=nonce, error=type(exc).__name__, detail=str(exc))
            return 500, {"ok": False, "stage": "reply"}
        log("sdk_monitor_replied", nonce=nonce, age_ms=age_ms)
        return 200, {"ok": True, "leg": "outbound"}

    if inbox == AGENT_A.lower():
        if age_ms > MAX_AGE_MS:
            # Deliberately not sdk_monitor_ok: a stale reply must not reset the
            # freshness alert.
            log("sdk_monitor_stale", leg="reply", nonce=nonce, age_ms=age_ms)
            return 200, {"ok": True, "stale": True}
        log("sdk_monitor_ok", nonce=nonce, latency_ms=age_ms, message_id=email.id)
        return 200, {"ok": True, "leg": "reply", "latency_ms": age_ms}

    return 200, {"ok": True, "ignored": "unknown inbox"}


class Handler(BaseHTTPRequestHandler):
    server_version = "e2a-sdk-monitor"

    def _respond(self, status: int, payload: dict) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        if self.path.split("?")[0] == "/health":
            self._respond(200, {"status": "ok"})
        else:
            self._respond(404, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = self.path.split("?")[0]
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if path == "/tick":
            self._respond(*handle_tick())
        elif path == "/webhook":
            # Must be the RAW bytes — re-serialized JSON won't match the HMAC.
            self._respond(*handle_webhook(raw, self.headers.get("X-E2A-Signature") or ""))
        else:
            self._respond(404, {"error": "not found"})

    def log_message(self, fmt: str, *args: object) -> None:
        """Silence the default access log — it would interleave non-JSON lines
        into the structured stream the alert parses."""


def main() -> None:
    missing = [
        name
        for name, value in (
            ("E2A_API_KEY", API_KEY),
            ("E2A_MONITOR_AGENT_A", AGENT_A),
            ("E2A_MONITOR_AGENT_B", AGENT_B),
            ("E2A_MONITOR_WEBHOOK_SECRET", WEBHOOK_SECRET),
        )
        if not value
    ]
    if missing:
        # Names only — never the values.
        log("sdk_monitor_error", stage="config", detail=f"missing env: {', '.join(missing)}")
        sys.exit(1)
    log("sdk_monitor_start", port=PORT, base_url=BASE_URL, agent_a=AGENT_A, agent_b=AGENT_B)
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()

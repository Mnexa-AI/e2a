"""Offline checks for the webhook handler. No pytest, no network:

    E2A_API_KEY=x E2A_MONITOR_AGENT_A=a@x E2A_MONITOR_AGENT_B=b@x \
    E2A_MONITOR_WEBHOOK_SECRET=whsec_test python test_monitor.py

Covers the parts that must not regress silently: signature accept/reject,
fail-closed on an unset secret, non-inbound event types, and the stale-reply
guard. The round trip itself is only exercisable against a live deployment.
"""

import hashlib
import hmac
import json
import os
import sys
import time

os.environ.setdefault("E2A_API_KEY", "e2a_acct_test")
os.environ.setdefault("E2A_BASE_URL", "https://api.e2a.dev")
os.environ.setdefault("E2A_MONITOR_AGENT_A", "mon-a@agents.e2a.dev")
os.environ.setdefault("E2A_MONITOR_AGENT_B", "mon-b@agents.e2a.dev")
os.environ.setdefault("E2A_MONITOR_WEBHOOK_SECRET", "whsec_testsecret")

import monitor  # noqa: E402

SECRET = monitor.WEBHOOK_SECRET
failures = []


def check(name, got, want):
    if got != want:
        failures.append(f"{name}: got {got!r}, want {want!r}")
    print(("PASS " if got == want else "FAIL ") + name)


def sign(body: bytes, secret: str = SECRET, ts: int | None = None) -> str:
    ts = ts if ts is not None else int(time.time())
    mac = hmac.new(secret.encode(), f"{ts}".encode() + b"." + body, hashlib.sha256)
    return f"t={ts},v1={mac.hexdigest()}"


def envelope(event_type: str, data: dict) -> bytes:
    return json.dumps(
        {
            "id": "evt_test_1",
            "type": event_type,
            "schema_version": "1",
            "created_at": "2026-07-21T00:00:00Z",
            "data": data,
        }
    ).encode()


body = envelope("email.sent", {"message_id": "msg_1"})

status, _ = monitor.handle_webhook(body, sign(body))
check("valid signature accepted (non-inbound ignored)", status, 200)

status, _ = monitor.handle_webhook(body, sign(body, secret="whsec_wrong"))
check("bad signature rejected", status, 401)

status, _ = monitor.handle_webhook(body, "")
check("missing signature header rejected", status, 401)

status, _ = monitor.handle_webhook(body, sign(body, ts=int(time.time()) - 3600))
check("replayed timestamp rejected", status, 401)

saved, monitor.WEBHOOK_SECRET = monitor.WEBHOOK_SECRET, ""
status, _ = monitor.handle_webhook(body, sign(body, secret=saved))
check("unset secret fails closed", status, 401)
monitor.WEBHOOK_SECRET = saved

# Stale guard: a reply whose nonce timestamp is older than MAX_AGE_MS must not
# emit sdk_monitor_ok. Stub hydration so this stays offline.
old_ms = int(time.time() * 1000) - monitor.MAX_AGE_MS - 60_000


class FakeEmail:
    id = "msg_stale"
    inbox = monitor.AGENT_A
    subject = f"Re: probe e2asdkmon.{old_ms}.0123456789abcdef"


class FakeInbound:
    def from_event(self, event):
        return FakeEmail()


class FakeClient:
    inbound = FakeInbound()


monitor._client = FakeClient()
emitted = []
monitor.log = lambda event, **f: emitted.append(event)

inbound_body = envelope("email.received", {"message_id": "msg_stale", "delivered_to": monitor.AGENT_A})
status, _ = monitor.handle_webhook(inbound_body, sign(inbound_body))
check("stale reply returns 200", status, 200)
check("stale reply does not emit sdk_monitor_ok", "sdk_monitor_ok" in emitted, False)
check("stale reply emits sdk_monitor_stale", "sdk_monitor_stale" in emitted, True)

# Fresh reply on the same path emits the success marker with latency.
FakeEmail.subject = f"Re: probe e2asdkmon.{int(time.time() * 1000) - 4200}.0123456789abcdef"
emitted.clear()
status, payload = monitor.handle_webhook(inbound_body, sign(inbound_body))
check("fresh reply emits sdk_monitor_ok", "sdk_monitor_ok" in emitted, True)
check("fresh reply reports latency", payload["latency_ms"] >= 4200, True)

# A subject with no probe nonce is ignored, not counted.
FakeEmail.subject = "hello from a real human"
emitted.clear()
status, payload = monitor.handle_webhook(inbound_body, sign(inbound_body))
check("non-probe subject ignored", payload.get("ignored"), "not a probe")

print()
if failures:
    print(f"{len(failures)} failure(s):")
    for f in failures:
        print("  " + f)
    sys.exit(1)
print("all checks passed")

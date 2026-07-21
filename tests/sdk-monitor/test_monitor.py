"""Offline checks for the multi-interface conformance monitor. No pytest, no
network, no real subprocess to npm/node:

    E2A_API_KEY=x E2A_MONITOR_AGENT_A=a@x E2A_MONITOR_AGENT_B=b@x \
    E2A_MONITOR_WEBHOOK_SECRET=whsec_test python test_monitor.py

Covers the parts that must not regress silently: signature accept/reject,
fail-closed on an unset secret, non-inbound event types, the nonce <-> iface
encoding round trip, the stale-reply guard (both legs), the interface-strategy
dispatch, and the unknown-iface safety net. Every interface's actual send/reply
I/O is stubbed with fakes injected into monitor.STRATEGIES — no real network
call, no real subprocess. The live round trips are only exercisable against a
real deployment.
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


# ---------------------------------------------------------------------------
# Nonce <-> iface encoding round trip.
# ---------------------------------------------------------------------------

for _iface in monitor.IFACES:
    _nonce = monitor.new_nonce(_iface)
    _match = monitor.NONCE_RE.search(f"probe {_nonce}")
    check(f"nonce round-trips iface={_iface}", _match is not None, True)
    if _match:
        check(f"nonce iface group matches ({_iface})", _match.group(1), _iface)
        check(f"nonce timestamp group is numeric ({_iface})", _match.group(2).isdigit(), True)
        check(f"nonce full match is the nonce itself ({_iface})", _match.group(0), _nonce)

# A nonce naming an iface this build doesn't recognize still matches the
# generic regex (handled explicitly downstream — see the unknown-iface test
# below) rather than silently failing to parse.
_unknown_nonce = f"e2asdkmon.some_future_iface.{int(time.time() * 1000)}.0123456789abcdef"
_unknown_match = monitor.NONCE_RE.search(f"probe {_unknown_nonce}")
check("unrecognized-but-shaped iface still parses", _unknown_match is not None, True)
check("unrecognized iface group captured", _unknown_match.group(1) if _unknown_match else None, "some_future_iface")


# ---------------------------------------------------------------------------
# Webhook signature verification: fail-closed, replay, non-inbound ignore.
# ---------------------------------------------------------------------------

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


# ---------------------------------------------------------------------------
# Fakes for the interface-strategy dispatch + stale-guard + success-path
# tests. Every strategy's I/O is stubbed — no network, no subprocess.
# ---------------------------------------------------------------------------


class FakeStrategy:
    def __init__(self):
        self.send_calls = []
        self.reply_calls = []

    def available(self):
        return True

    def send(self, **kwargs):
        self.send_calls.append(kwargs)

    def reply(self, **kwargs):
        self.reply_calls.append(kwargs)


class FakeEmail:
    id = "msg_stale"
    inbox = monitor.AGENT_A
    subject = ""  # set per-test below


class FakeInbound:
    def from_event(self, event):
        return FakeEmail()


class FakeClient:
    inbound = FakeInbound()


monitor._client = FakeClient()
emitted = []
monitor.log = lambda event, **f: emitted.append((event, f))

fake_strategies = {iface: FakeStrategy() for iface in monitor.IFACES}
monitor.STRATEGIES = fake_strategies

inbound_body = envelope("email.received", {"message_id": "msg_stale", "delivered_to": monitor.AGENT_A})


def emitted_events():
    return [e for e, _ in emitted]


# Stale guard on the reply leg (inbox == A): a reply whose nonce timestamp is
# older than MAX_AGE_MS must not emit monitor_ok.
old_ms = int(time.time() * 1000) - monitor.MAX_AGE_MS - 60_000
FakeEmail.subject = f"Re: probe e2asdkmon.python_sdk.{old_ms}.0123456789abcdef"
emitted.clear()
status, _ = monitor.handle_webhook(inbound_body, sign(inbound_body))
check("stale reply returns 200", status, 200)
check("stale reply does not emit monitor_ok", "monitor_ok" in emitted_events(), False)
check("stale reply emits monitor_stale", "monitor_stale" in emitted_events(), True)

# Fresh reply on the same path emits the success marker with iface + latency,
# and does NOT dispatch to any strategy (inbox == A is the terminal leg).
FakeEmail.subject = f"Re: probe e2asdkmon.python_sdk.{int(time.time() * 1000) - 4200}.0123456789abcdef"
emitted.clear()
status, payload = monitor.handle_webhook(inbound_body, sign(inbound_body))
check("fresh reply emits monitor_ok", "monitor_ok" in emitted_events(), True)
check("fresh reply reports latency", payload["latency_ms"] >= 4200, True)
ok_fields = next(f for e, f in emitted if e == "monitor_ok")
check("fresh reply's monitor_ok carries iface", ok_fields.get("iface"), "python_sdk")
check("fresh reply's monitor_ok carries latency_ms", ok_fields.get("latency_ms", 0) >= 4200, True)

# A subject with no probe nonce is ignored, not counted.
FakeEmail.subject = "hello from a real human"
emitted.clear()
status, payload = monitor.handle_webhook(inbound_body, sign(inbound_body))
check("non-probe subject ignored", payload.get("ignored"), "not a probe")


# ---------------------------------------------------------------------------
# Interface-strategy dispatch: the outbound leg (inbox == B) replies via the
# EXACT strategy named in the nonce, and no other strategy is touched.
# ---------------------------------------------------------------------------


class FakeEmailB:
    id = "msg_fresh_b"
    inbox = monitor.AGENT_B
    subject = ""


class FakeInboundB:
    def from_event(self, event):
        return FakeEmailB()


class FakeClientB:
    inbound = FakeInboundB()


monitor._client = FakeClientB()

fresh_ms = int(time.time() * 1000) - 100
FakeEmailB.subject = f"probe e2asdkmon.ts_sdk.{fresh_ms}.0123456789abcdef"
for s in fake_strategies.values():
    s.reply_calls.clear()
emitted.clear()
inbound_body_b = envelope("email.received", {"message_id": "msg_fresh_b", "delivered_to": monitor.AGENT_B})
status, payload = monitor.handle_webhook(inbound_body_b, sign(inbound_body_b))
check("outbound leg (fresh) returns 200", status, 200)
check("outbound leg dispatches to the nonce's own iface (ts_sdk)", len(fake_strategies["ts_sdk"].reply_calls), 1)
check("outbound leg does not touch other ifaces", sum(len(s.reply_calls) for k, s in fake_strategies.items() if k != "ts_sdk"), 0)
replied_kwargs = fake_strategies["ts_sdk"].reply_calls[0]
check("dispatched reply targets inbox B", replied_kwargs.get("inbox"), monitor.AGENT_B)
check("dispatched reply carries the message id", replied_kwargs.get("message_id"), "msg_fresh_b")
check("dispatched reply carries an idempotency key scoped to the event", replied_kwargs.get("idempotency_key"), "sdkmon:evt_test_1")
check("monitor_replied logs the dispatched iface", any(e == "monitor_replied" and f.get("iface") == "ts_sdk" for e, f in emitted), True)

# Stale guard on the outbound leg (inbox == B) must ALSO block dispatch —
# never calls the strategy's reply().
FakeEmailB.subject = f"probe e2asdkmon.cli.{old_ms}.0123456789abcdef"
for s in fake_strategies.values():
    s.reply_calls.clear()
emitted.clear()
status, _ = monitor.handle_webhook(inbound_body_b, sign(inbound_body_b))
check("stale outbound leg does not dispatch a reply", len(fake_strategies["cli"].reply_calls), 0)
check("stale outbound leg emits monitor_stale", "monitor_stale" in emitted_events(), True)


# ---------------------------------------------------------------------------
# Unknown iface: parses but isn't in STRATEGIES — handled safely, no crash,
# no success/stale claim, no dispatch.
# ---------------------------------------------------------------------------

FakeEmailB.subject = f"probe e2asdkmon.some_future_iface.{fresh_ms}.0123456789abcdef"
for s in fake_strategies.values():
    s.reply_calls.clear()
emitted.clear()
status, payload = monitor.handle_webhook(inbound_body_b, sign(inbound_body_b))
check("unknown iface handled without crashing (200)", status, 200)
check("unknown iface does not dispatch any strategy", sum(len(s.reply_calls) for s in fake_strategies.values()), 0)
check("unknown iface logs a distinct error stage", any(e == "monitor_error" and f.get("stage") == "unknown_iface" for e, f in emitted), True)
check("unknown iface never emits monitor_ok", "monitor_ok" in emitted_events(), False)


# ---------------------------------------------------------------------------
# /tick fan-out: every interface is attempted; one failure doesn't abort the
# others; a strategy reporting unavailable is skipped, not attempted.
# ---------------------------------------------------------------------------


class SendOnlyStrategy(FakeStrategy):
    def __init__(self, should_fail=False):
        super().__init__()
        self.should_fail = should_fail

    def send(self, **kwargs):
        super().send(**kwargs)
        if self.should_fail:
            raise RuntimeError("boom")


tick_strategies = {iface: SendOnlyStrategy() for iface in monitor.IFACES}
tick_strategies["mcp"].should_fail = True  # simulate one interface's failure


class UnavailableStrategy(SendOnlyStrategy):
    def available(self):
        return False


tick_strategies["cli"] = UnavailableStrategy()
monitor.STRATEGIES = tick_strategies
emitted.clear()
status, payload = monitor.handle_tick()
check("tick returns 200", status, 200)
check("tick attempts every non-skipped iface", tick_strategies["api"].send_calls != [], True)
check("tick's overall failure is reflected", payload["ok"], False)
check("tick reports the failing iface as not ok", payload["results"]["mcp"]["ok"], False)
check("tick reports the succeeding ifaces as ok", payload["results"]["python_sdk"]["ok"], True)
check("tick does not call send on an unavailable iface", tick_strategies["cli"].send_calls, [])
check("tick marks an unavailable iface as skipped", payload["results"]["cli"]["skipped"], True)
check("tick logs monitor_skip for the unavailable iface", any(e == "monitor_skip" and f.get("iface") == "cli" for e, f in emitted), True)
check("tick logs monitor_error for the failing iface", any(e == "monitor_error" and f.get("iface") == "mcp" and f.get("stage") == "send" for e, f in emitted), True)


# ---------------------------------------------------------------------------
# McpStrategy response parsing: SSE framing, JSON-RPC error, tool isError, and
# the malformed-response guard (neither result nor error present must NOT be
# silently treated as success — found in adversarial review of this change).
# Stubs urllib.request.urlopen directly; no real network.
# ---------------------------------------------------------------------------

import urllib.request as _urllib_request  # noqa: E402


class _FakeHeaders:
    def __init__(self, content_type):
        self._content_type = content_type

    def get(self, name, default=None):
        return self._content_type if name == "Content-Type" else default


class _FakeResponse:
    def __init__(self, raw: bytes, content_type: str):
        self._raw = raw
        self.headers = _FakeHeaders(content_type)

    def read(self):
        return self._raw

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


def _stub_urlopen(sse_body: str):
    def _fake_urlopen(req, timeout=None):
        return _FakeResponse(sse_body.encode(), "text/event-stream")

    return _fake_urlopen


_saved_urlopen = _urllib_request.urlopen
_mcp = monitor.McpStrategy("https://mcp.example/mcp", "e2a_acct_test")

_urllib_request.urlopen = _stub_urlopen(
    'data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}\n\n'
)
try:
    _mcp.send(agent_a="a@x", agent_b="b@x", subject="probe x", body="x")
    _mcp_send_ok = True
except Exception:
    _mcp_send_ok = False
check("mcp strategy parses a well-formed SSE result", _mcp_send_ok, True)

_urllib_request.urlopen = _stub_urlopen(
    'data: {"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}\n\n'
)
try:
    _mcp.send(agent_a="a@x", agent_b="b@x", subject="probe x", body="x")
    _mcp_error_raised = False
except RuntimeError:
    _mcp_error_raised = True
check("mcp strategy raises on a JSON-RPC error envelope", _mcp_error_raised, True)

_urllib_request.urlopen = _stub_urlopen(
    'data: {"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"e2a error [invalid_request]: bad"}]}}\n\n'
)
try:
    _mcp.send(agent_a="a@x", agent_b="b@x", subject="probe x", body="x")
    _mcp_tool_error_raised = False
except RuntimeError:
    _mcp_tool_error_raised = True
check("mcp strategy raises on a tool-level isError result", _mcp_tool_error_raised, True)

# The malformed case: neither "result" nor "error" — must raise, not silently
# succeed (this was the adversarial-review finding this test guards).
_urllib_request.urlopen = _stub_urlopen('data: {"jsonrpc":"2.0","id":1}\n\n')
try:
    _mcp.send(agent_a="a@x", agent_b="b@x", subject="probe x", body="x")
    _mcp_malformed_raised = False
except RuntimeError:
    _mcp_malformed_raised = True
check("mcp strategy raises on a response with neither result nor error", _mcp_malformed_raised, True)

_urllib_request.urlopen = _saved_urlopen

check("mcp strategy reports unavailable when no URL is configured", monitor.McpStrategy("", "k").available(), False)
check("mcp strategy reports available when a URL is configured", monitor.McpStrategy("https://x/mcp", "k").available(), True)


print()
if failures:
    print(f"{len(failures)} failure(s):")
    for f in failures:
        print("  " + f)
    sys.exit(1)
print("all checks passed")

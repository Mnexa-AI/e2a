"""Tests for e2a.v1.websocket — WS URL building, event streaming, no ACKs.

The listener is intentionally lightweight: it yields :class:`WSEvent`
envelopes (the same versioned shape as a webhook delivery) with metadata-only
``data``, never fetching the full message via REST. Callers do that
explicitly when they want the body.
"""

import json
import sys
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from pydantic import ValidationError

from e2a.v1.generated.models import MessageLifecycleTransition
from e2a.v1.webhook_signature import is_email_received
from e2a.v1.websocket import WSEvent, _build_ws_url


CLOSE_CONTRACT = json.loads(
    (Path(__file__).resolve().parents[3] / "internal" / "ws" / "testdata" / "close-contract.json").read_text()
)


# ── _build_ws_url ────────────────────────────────────────────────


def test_build_ws_url_https():
    # Auth is the Authorization header now — the key must NOT appear in the URL.
    url = _build_ws_url("https://e2a.dev", "bot@agents.e2a.dev")
    assert url == "wss://e2a.dev/v1/agents/bot%40agents.e2a.dev/ws"


def test_build_ws_url_http():
    url = _build_ws_url("http://localhost:8080", "bot@agents.e2a.dev")
    assert url == "ws://localhost:8080/v1/agents/bot%40agents.e2a.dev/ws"


def test_build_ws_url_no_credential_in_url():
    url = _build_ws_url("https://e2a.dev", "bot@agents.e2a.dev")
    assert "token=" not in url and "?" not in url


def test_build_ws_url_encodes_email():
    url = _build_ws_url("https://e2a.dev", "bot+tag@agents.e2a.dev")
    assert "bot%2Btag%40agents.e2a.dev" in url


def test_build_ws_url_uses_v1_path():
    url = _build_ws_url("https://e2a.dev", "bot@agents.e2a.dev")
    assert "/v1/agents/" in url
    # Must NOT use legacy /api/agents/ path
    assert "/api/agents/" not in url.replace("/v1/agents/", "")


# ── WSEvent.from_payload ─────────────────────────────────────────


def _envelope(data: dict, type_: str = "email.received") -> dict:
    return {
        "type": type_,
        "id": "evt_abc",
        "schema_version": "1",
        "created_at": "2026-04-27T10:00:00Z",
        "data": data,
    }


def _lifecycle_transition(**overrides) -> dict:
    row = {
        "id": "mlt_1",
        "message_id": "msg_1",
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
    row.update(overrides)
    return row


def test_ws_event_from_payload():
    e = WSEvent.from_payload(_envelope({
        "message_id": "msg_1",
        "from": "alice@example.com",
        "delivered_to": "bot@agents.e2a.dev",
        "subject": "Hi",
        "received_at": "2026-04-27T10:00:00Z",
        "conversation_id": "conv_xyz",
    }))
    assert e.type == "email.received"
    assert e.id == "evt_abc"
    assert e.schema_version == "1"
    assert e.created_at == "2026-04-27T10:00:00Z"
    assert e.data["message_id"] == "msg_1"
    assert e.data["from"] == "alice@example.com"
    assert e.data["delivered_to"] == "bot@agents.e2a.dev"
    assert e.raw["type"] == "email.received"


@pytest.mark.parametrize(
    "event_type",
    [
        "email.received",
        "email.sent",
        "email.failed",
        "email.delivered",
        "email.bounced",
        "email.complained",
        "domain.suppression_added",
    ],
)
def test_ws_event_coerces_v1_stable_lifecycle_with_original_raw_preserved(event_type):
    payload = _envelope(
        {"lifecycle_transitions": [_lifecycle_transition()]}, type_=event_type
    )
    event = WSEvent.from_payload(payload)
    transition = event.data["lifecycle_transitions"][0]
    assert isinstance(transition, MessageLifecycleTransition)
    assert transition.recipient is None
    assert transition.evidence["future"] == {"nested": True}
    assert transition.correlation_ids["future_id"] == "future_1"
    assert transition.reconstructed is True
    assert isinstance(event.raw["data"]["lifecycle_transitions"][0], dict)
    assert is_email_received(event) is (event_type == "email.received")


@pytest.mark.parametrize("field", ["direction", "stage", "outcome", "reason_code"])
def test_ws_event_rejects_unknown_v1_lifecycle_values(field):
    with pytest.raises(ValidationError, match=field):
        WSEvent.from_payload(
            _envelope(
                {"lifecycle_transitions": [_lifecycle_transition(**{field: "future_value"})]}
            )
        )


def test_ws_event_without_lifecycle_keeps_original_data_object():
    payload = _envelope({"message_id": "msg_1"})
    event = WSEvent.from_payload(payload)
    assert event.data is payload["data"]


def test_ws_event_tolerates_unknown_type():
    """Future WS event kinds parse into the same envelope (forward-compat)."""
    e = WSEvent.from_payload(_envelope({"anything": True}, type_="email.future_kind"))
    assert e.type == "email.future_kind"
    assert e.data == {"anything": True}


@pytest.mark.parametrize("missing", ["type", "id", "schema_version", "created_at", "data"])
def test_ws_event_rejects_missing_required_envelope_field(missing):
    payload = _envelope({})
    del payload[missing]
    with pytest.raises(ValueError, match="missing required envelope fields"):
        WSEvent.from_payload(payload)


def test_ws_event_preserves_future_envelope_versions_and_fields():
    payload = _envelope(
        {
            "future": True,
            "lifecycle_transitions": [
                _lifecycle_transition(
                    direction="sideways",
                    stage="future_stage",
                    outcome="future_outcome",
                    reason_code="future.reason",
                )
            ],
        }
    )
    payload["schema_version"] = "2"
    payload["future_envelope_field"] = True
    event = WSEvent.from_payload(payload)
    assert event.schema_version == "2"
    assert event.raw["future_envelope_field"] is True
    assert event.data is payload["data"]
    assert event.data["lifecycle_transitions"][0]["stage"] == "future_stage"
    assert not is_email_received(event)


# ── _connect_and_stream ──────────────────────────────────────────


class FakeWebSocket:
    """Simulates a websockets connection that yields messages and tracks sends."""

    def __init__(self, messages):
        self._messages = list(messages)
        self.send = AsyncMock()

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        pass

    def __aiter__(self):
        return self

    async def __anext__(self):
        if not self._messages:
            raise StopAsyncIteration
        return self._messages.pop(0)


def _patch_websockets_connect(fake_ws):
    """Create a patch for websockets.connect inside the v1.websocket module."""
    mock_module = MagicMock()
    mock_module.connect = MagicMock(return_value=fake_ws)
    return patch.dict(sys.modules, {"websockets": mock_module})


@pytest.mark.anyio
async def test_connect_and_stream_yields_notifications_no_fetch():
    """Lightweight by design: yield WSEvent envelopes, never call get_message()."""
    from e2a.v1.websocket import _connect_and_stream

    payload = json.dumps(_envelope({
        "message_id": "msg_123",
        "from": "alice@example.com",
        "delivered_to": "bot@agents.e2a.dev",
        "subject": "Hi",
        "received_at": "2026-04-27T10:00:00Z",
        "conversation_id": "conv_xyz",
    }))
    fake_ws = FakeWebSocket([payload])

    with _patch_websockets_connect(fake_ws):
        results = []
        async for notif in _connect_and_stream("wss://e2a.dev/ws", "k", "bot@agents.e2a.dev"):
            results.append(notif)

    assert len(results) == 1
    e = results[0]
    assert isinstance(e, WSEvent)
    assert e.type == "email.received"
    assert e.schema_version == "1"
    assert e.data["message_id"] == "msg_123"
    assert e.data["from"] == "alice@example.com"
    assert e.data["delivered_to"] == "bot@agents.e2a.dev"
    assert e.data["subject"] == "Hi"
    assert e.data["conversation_id"] == "conv_xyz"


@pytest.mark.anyio
async def test_connect_and_stream_yields_unknown_event_types():
    """Unknown `type` values are yielded, not dropped (forward-compat)."""
    from e2a.v1.websocket import _connect_and_stream

    payload = json.dumps(_envelope({"x": 1}, type_="email.future_kind"))
    fake_ws = FakeWebSocket([payload])

    with _patch_websockets_connect(fake_ws):
        async for notif in _connect_and_stream("wss://e2a.dev/ws", "k", "bot@agents.e2a.dev"):
            assert notif.type == "email.future_kind"


@pytest.mark.anyio
async def test_connect_and_stream_no_ack_sent():
    """The WS client must NEVER send any frames (no ACK)."""
    from e2a.v1.websocket import _connect_and_stream

    payload = json.dumps(_envelope({"message_id": "msg_123", "from": "a@b.c", "delivered_to": "bot@agents.e2a.dev", "subject": "", "received_at": ""}))
    fake_ws = FakeWebSocket([payload])

    with _patch_websockets_connect(fake_ws):
        async for _ in _connect_and_stream("wss://e2a.dev/ws", "k", "bot@agents.e2a.dev"):
            pass

    fake_ws.send.assert_not_called()


@pytest.mark.anyio
async def test_connect_and_stream_skips_malformed():
    """Frames without a string `type` (not an envelope) are skipped, not crash."""
    from e2a.v1.websocket import _connect_and_stream

    messages_in = [
        json.dumps({"from": "alice@example.com"}),  # no type — not an envelope, drop
        json.dumps(_envelope({"message_id": "msg_456", "from": "bob@example.com", "delivered_to": "bot@agents.e2a.dev", "subject": "", "received_at": ""})),
    ]
    fake_ws = FakeWebSocket(messages_in)

    with _patch_websockets_connect(fake_ws):
        results = []
        async for notif in _connect_and_stream("wss://e2a.dev/ws", "k", "bot@agents.e2a.dev"):
            results.append(notif)

    assert len(results) == 1
    assert results[0].data["message_id"] == "msg_456"


# ── WSStream ─────────────────────────────────────────────────────


def test_wsstream_builds_v1_url():
    from e2a.v1.websocket import WSStream

    s = WSStream(api_key="k", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev")
    # Auth is the Authorization header now — the key must NOT be in the URL.
    assert s._url == "wss://e2a.dev/v1/agents/bot%40agents.e2a.dev/ws"
    assert "token=" not in s._url


@pytest.mark.anyio
async def test_ws_connect_sends_authorization_header():
    """The API key must be sent as the Authorization: Bearer handshake header
    (via websockets' additional_headers), never in the URL."""
    from e2a.v1.websocket import _connect_and_stream

    fake_ws = FakeWebSocket([
        json.dumps(_envelope({"message_id": "m1", "from": "a@b.c", "delivered_to": "bot@x.dev", "subject": "", "received_at": ""}))
    ])
    mock_module = MagicMock()
    mock_module.connect = MagicMock(return_value=fake_ws)
    with patch.dict(sys.modules, {"websockets": mock_module}):
        async for _ in _connect_and_stream("wss://e2a.dev/v1/agents/bot%40x.dev/ws", "secret_key", "bot@x.dev"):
            pass

    mock_module.connect.assert_called_once()
    _args, kwargs = mock_module.connect.call_args
    assert kwargs.get("additional_headers") == {"Authorization": "Bearer secret_key"}, kwargs
    url_arg = mock_module.connect.call_args.args[0]
    assert "secret_key" not in url_arg and "token=" not in url_arg


@pytest.mark.anyio
async def test_wsstream_missing_websockets():
    """Iterating raises ImportError with install guidance when websockets is missing."""
    from e2a.v1.websocket import WSStream

    s = WSStream(api_key="k", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev")
    with patch.dict(sys.modules, {"websockets": None}):
        with pytest.raises(ImportError, match="pip install e2a"):
            async for _ in s:
                pass


@pytest.mark.anyio
async def test_wsstream_no_reconnect_exits():
    """With reconnect=False, the stream exits after the first disconnect."""
    from e2a.v1.websocket import WSStream

    fake_notif = WSEvent(
        type="email.received",
        id="evt_1",
        schema_version="1",
        created_at="2026-04-27T10:00:00Z",
        data={
            "message_id": "msg_1",
            "from": "alice@example.com",
            "delivered_to": "bot@agents.e2a.dev",
            "subject": "Hi",
            "received_at": "2026-04-27T10:00:00Z",
        },
    )
    call_count = 0

    async def fake_connect_and_stream(*args, **kwargs):
        nonlocal call_count
        call_count += 1
        yield fake_notif
        return

    s = WSStream(
        api_key="k", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev", reconnect=False
    )
    with patch("e2a.v1.websocket._connect_and_stream", side_effect=fake_connect_and_stream), \
         patch.dict(sys.modules, {"websockets": MagicMock()}):
        results = [notif async for notif in s]

    assert len(results) == 1
    assert call_count == 1


# ── F6: fatal handshake failure surfaces a typed error, no infinite loop ──


class _FakeResponse:
    def __init__(self, status_code):
        self.status_code = status_code


class _FakeInvalidStatus(Exception):
    """Mimics websockets.InvalidStatus: status on .response.status_code."""

    def __init__(self, status_code):
        super().__init__(f"server rejected WebSocket connection: HTTP {status_code}")
        self.response = _FakeResponse(status_code)


class _FakeInvalidStatusCode(Exception):
    """Mimics the deprecated websockets.InvalidStatusCode: .status_code."""

    def __init__(self, status_code):
        super().__init__(f"HTTP {status_code}")
        self.status_code = status_code


def test_handshake_status_extracts_from_both_shapes():
    from e2a.v1.websocket import _handshake_status

    assert _handshake_status(_FakeInvalidStatus(401)) == 401
    assert _handshake_status(_FakeInvalidStatusCode(403)) == 403
    assert _handshake_status(OSError("connection refused")) is None


def test_fatal_error_for_status_maps_typed():
    from e2a.v1.errors import (
        E2AAuthError,
        E2AError,
        E2ANotFoundError,
        E2APermissionError,
    )
    from e2a.v1.websocket import _fatal_error_for_status

    assert isinstance(_fatal_error_for_status(401, ValueError()), E2AAuthError)
    assert isinstance(_fatal_error_for_status(403, ValueError()), E2APermissionError)
    # 404 -> typed not-found. The server returns 404 both when the agent doesn't
    # exist AND when it exists but isn't yours (cross-tenant), collapsing the two
    # so the handshake can't enumerate agents; the SDK surfaces one typed error.
    not_found = _fatal_error_for_status(404, ValueError())
    assert isinstance(not_found, E2ANotFoundError) and not_found.retryable is False
    # Other 4xx -> generic but still fatal E2AError.
    other = _fatal_error_for_status(422, ValueError())
    assert isinstance(other, E2AError) and other.retryable is False
    # 5xx is transient -> no fatal error (reconnect).
    assert _fatal_error_for_status(503, ValueError()) is None


@pytest.mark.anyio
async def test_wsstream_fatal_401_raises_typed_and_does_not_loop():
    """A 401 handshake rejection must raise E2AAuthError and STOP — not loop."""
    from e2a.v1.errors import E2AAuthError
    from e2a.v1.websocket import WSStream

    attempts = 0

    async def fake_connect_and_stream(*args, **kwargs):
        nonlocal attempts
        attempts += 1
        raise _FakeInvalidStatus(401)
        yield  # pragma: no cover - makes this an async generator

    # reconnect=True: the bug being fixed would loop forever here.
    s = WSStream(
        api_key="bad", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev",
        reconnect=True,
    )
    with patch("e2a.v1.websocket._connect_and_stream", side_effect=fake_connect_and_stream), \
         patch.dict(sys.modules, {"websockets": MagicMock()}):
        with pytest.raises(E2AAuthError) as ei:
            async for _ in s:
                pass

    assert ei.value.status == 401
    # Bounded: a single connect attempt, then it raised instead of reconnecting.
    assert attempts == 1


# ── close-code contract (docs/api.md "Connection lifecycle & close codes") ──


class _FakeCloseFrame:
    def __init__(self, code, reason=""):
        self.code = code
        self.reason = reason


class _FakeConnectionClosed(Exception):
    """Mimics websockets.ConnectionClosed: received frame on .rcvd."""

    def __init__(self, code, reason=""):
        super().__init__(f"received {code} ({reason})")
        self.rcvd = _FakeCloseFrame(code, reason)


class _FakeLegacyConnectionClosed(Exception):
    """Mimics the deprecated shape: .code / .reason attributes."""

    def __init__(self, code, reason=""):
        super().__init__(f"code = {code}")
        self.code = code
        self.reason = reason


def test_close_code_and_reason_extracts_from_both_shapes():
    from e2a.v1.websocket import _close_code_and_reason

    assert _close_code_and_reason(_FakeConnectionClosed(4000, "replaced")) == (4000, "replaced")
    assert _close_code_and_reason(_FakeLegacyConnectionClosed(1008, "nope")) == (1008, "nope")
    assert _close_code_and_reason(OSError("connection refused")) == (None, "")


def test_fatal_error_for_close_matrix():
    from e2a.v1.errors import (
        E2AConnectionReplacedError,
        E2AError,
        E2APermissionError,
    )
    from e2a.v1.websocket import WS_CLOSE_REPLACED, _fatal_error_for_close

    # 4000 "replaced" — a newer connection took over: typed, terminal.
    replaced = _fatal_error_for_close(WS_CLOSE_REPLACED, "replaced")
    assert isinstance(replaced, E2AConnectionReplacedError)
    assert replaced.code == "ws_replaced" and replaced.retryable is False

    # 1008 — genuine policy rejection: terminal.
    policy = _fatal_error_for_close(1008, "policy violation")
    assert isinstance(policy, E2APermissionError)
    assert policy.code == "ws_policy_violation" and policy.retryable is False

    # Unknown 4xxx application codes are fatal by contract (forward-compat).
    future = _fatal_error_for_close(4321, "future_condition")
    assert isinstance(future, E2AError) and future.retryable is False

    # Transient closes reconnect: shutdown/ping-timeout (1001), abnormal
    # (1006), internal error (1011).
    assert _fatal_error_for_close(1001, "shutting_down") is None
    assert _fatal_error_for_close(1001, "ping_timeout") is None
    assert _fatal_error_for_close(1006, "") is None
    assert _fatal_error_for_close(1011, "") is None


@pytest.mark.parametrize("case", CLOSE_CONTRACT)
def test_close_contract_shared_matrix(case):
    from e2a.v1.websocket import _close_disposition

    assert _close_disposition(case["code"], case["reason"]) == case["classification"]


@pytest.mark.anyio
async def test_wsstream_replaced_4000_raises_typed_and_does_not_reconnect():
    """A 4000 "replaced" close must raise E2AConnectionReplacedError and STOP —
    reconnecting would steal the socket back from our own replacement and loop
    (the pre-contract bug, when the server sent 1008 for this)."""
    from e2a.v1.errors import E2AConnectionReplacedError
    from e2a.v1.websocket import WSStream

    attempts = 0

    async def fake_connect_and_stream(*args, **kwargs):
        nonlocal attempts
        attempts += 1
        raise _FakeConnectionClosed(4000, "replaced")
        yield  # pragma: no cover - makes this an async generator

    # reconnect=True: the bug being fixed would loop (steal the socket back).
    s = WSStream(
        api_key="k", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev",
        reconnect=True,
    )
    with patch("e2a.v1.websocket._connect_and_stream", side_effect=fake_connect_and_stream), \
         patch.dict(sys.modules, {"websockets": MagicMock()}):
        with pytest.raises(E2AConnectionReplacedError) as ei:
            async for _ in s:
                pass

    assert ei.value.code == "ws_replaced"
    assert attempts == 1  # one connect, then stop — no takeover loop


@pytest.mark.anyio
async def test_wsstream_1008_policy_close_raises_typed_and_stops():
    from e2a.v1.errors import E2APermissionError
    from e2a.v1.websocket import WSStream

    attempts = 0

    async def fake_connect_and_stream(*args, **kwargs):
        nonlocal attempts
        attempts += 1
        raise _FakeConnectionClosed(1008, "policy violation")
        yield  # pragma: no cover

    s = WSStream(
        api_key="k", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev",
        reconnect=True,
    )
    with patch("e2a.v1.websocket._connect_and_stream", side_effect=fake_connect_and_stream), \
         patch.dict(sys.modules, {"websockets": MagicMock()}):
        with pytest.raises(E2APermissionError):
            async for _ in s:
                pass

    assert attempts == 1


@pytest.mark.anyio
async def test_wsstream_transient_close_code_reconnects():
    """A 1011 (internal error) close is transient — no raise, reconnect path."""
    from e2a.v1.websocket import WSStream

    attempts = 0

    async def fake_connect_and_stream(*args, **kwargs):
        nonlocal attempts
        attempts += 1
        raise _FakeConnectionClosed(1011, "")
        yield  # pragma: no cover

    # reconnect=False bounds the loop: the transient path returns cleanly
    # after one failure instead of raising.
    s = WSStream(
        api_key="k", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev",
        reconnect=False,
    )
    with patch("e2a.v1.websocket._connect_and_stream", side_effect=fake_connect_and_stream), \
         patch.dict(sys.modules, {"websockets": MagicMock()}):
        results = [notif async for notif in s]

    assert results == []  # ended cleanly — no typed raise for a transient close
    assert attempts == 1


@pytest.mark.anyio
async def test_wsstream_normal_close_stops_without_reconnecting():
    from e2a.v1.websocket import WSStream

    attempts = 0

    async def fake_connect_and_stream(*args, **kwargs):
        nonlocal attempts
        attempts += 1
        raise _FakeConnectionClosed(1000, "")
        yield  # pragma: no cover

    stream = WSStream(
        api_key="k", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev",
        reconnect=True,
    )
    with patch("e2a.v1.websocket._connect_and_stream", side_effect=fake_connect_and_stream), \
         patch.dict(sys.modules, {"websockets": MagicMock()}):
        results = [event async for event in stream]

    assert results == []
    assert attempts == 1


@pytest.mark.anyio
async def test_wsstream_transient_failure_reconnects():
    """A network failure (no handshake status) reconnects rather than raising."""
    from e2a.v1.websocket import WSStream

    attempts = 0

    async def fake_connect_and_stream(*args, **kwargs):
        nonlocal attempts
        attempts += 1
        raise OSError("connection refused")
        yield  # pragma: no cover

    # reconnect=False so the transient path returns after one failure (no raise).
    s = WSStream(
        api_key="k", agent_email="bot@agents.e2a.dev", base_url="https://e2a.dev",
        reconnect=False,
    )
    with patch("e2a.v1.websocket._connect_and_stream", side_effect=fake_connect_and_stream), \
         patch.dict(sys.modules, {"websockets": MagicMock()}):
        results = [notif async for notif in s]

    assert results == []  # no notifications, and crucially no raise
    assert attempts == 1

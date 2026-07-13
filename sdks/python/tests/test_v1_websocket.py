"""Tests for e2a.v1.websocket — WS URL building, event streaming, no ACKs.

The listener is intentionally lightweight: it yields :class:`WSEvent`
envelopes (the same versioned shape as a webhook delivery) with metadata-only
``data``, never fetching the full message via REST. Callers do that
explicitly when they want the body.
"""

import json
import sys
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from e2a.v1.websocket import WSEvent, _build_ws_url


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


def test_ws_event_tolerates_unknown_type():
    """Future WS event kinds parse into the same envelope (forward-compat)."""
    e = WSEvent.from_payload(_envelope({"anything": True}, type_="email.future_kind"))
    assert e.type == "email.future_kind"
    assert e.data == {"anything": True}


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

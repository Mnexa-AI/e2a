"""Tests for e2a.v1.websocket — WS URL building, notification streaming, no ACKs."""

import json
import sys
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from e2a.v1.websocket import _build_ws_url


# ── _build_ws_url ────────────────────────────────────────────────


def test_build_ws_url_https():
    url = _build_ws_url("https://e2a.dev", "bot@agents.e2a.dev", "e2a_key")
    assert url == "wss://e2a.dev/api/v1/agents/bot%40agents.e2a.dev/ws?token=e2a_key"


def test_build_ws_url_http():
    url = _build_ws_url("http://localhost:8080", "bot@agents.e2a.dev", "key")
    assert url == "ws://localhost:8080/api/v1/agents/bot%40agents.e2a.dev/ws?token=key"


def test_build_ws_url_encodes_email():
    url = _build_ws_url("https://e2a.dev", "bot+tag@agents.e2a.dev", "k")
    assert "bot%2Btag%40agents.e2a.dev" in url


def test_build_ws_url_uses_v1_path():
    url = _build_ws_url("https://e2a.dev", "bot@agents.e2a.dev", "k")
    assert "/api/v1/agents/" in url
    # Must NOT use legacy /api/agents/ path
    assert "/api/agents/" not in url.replace("/api/v1/agents/", "")


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
async def test_connect_and_stream_fetches_message():
    """On WS notification, should fetch full message via client.get_message()."""
    from e2a.v1.websocket import _connect_and_stream

    fake_email = MagicMock()
    fake_email.message_id = "msg_123"

    mock_client = AsyncMock()
    mock_client.get_message = AsyncMock(return_value=fake_email)

    notification = json.dumps({"message_id": "msg_123", "from": "alice@example.com"})
    fake_ws = FakeWebSocket([notification])

    with _patch_websockets_connect(fake_ws):
        messages = []
        async for msg in _connect_and_stream(mock_client, "wss://e2a.dev/ws", "bot@agents.e2a.dev"):
            messages.append(msg)

    assert len(messages) == 1
    mock_client.get_message.assert_called_once_with("msg_123", agent_email="bot@agents.e2a.dev")


@pytest.mark.anyio
async def test_connect_and_stream_no_ack_sent():
    """The WS client must NEVER send any frames (no ACK)."""
    from e2a.v1.websocket import _connect_and_stream

    fake_email = MagicMock()
    mock_client = AsyncMock()
    mock_client.get_message = AsyncMock(return_value=fake_email)

    notification = json.dumps({"message_id": "msg_123"})
    fake_ws = FakeWebSocket([notification])

    with _patch_websockets_connect(fake_ws):
        async for _ in _connect_and_stream(mock_client, "wss://e2a.dev/ws", "bot@agents.e2a.dev"):
            pass

    # Assert send was never called — no ACK frames
    fake_ws.send.assert_not_called()


@pytest.mark.anyio
async def test_connect_and_stream_skips_malformed():
    """Notifications without message_id should be skipped, not crash."""
    from e2a.v1.websocket import _connect_and_stream

    fake_email = MagicMock()
    mock_client = AsyncMock()
    mock_client.get_message = AsyncMock(return_value=fake_email)

    messages_in = [
        json.dumps({"from": "alice@example.com"}),  # no message_id
        json.dumps({"message_id": "msg_456", "from": "bob@example.com"}),
    ]
    fake_ws = FakeWebSocket(messages_in)

    with _patch_websockets_connect(fake_ws):
        results = []
        async for msg in _connect_and_stream(mock_client, "wss://e2a.dev/ws", "bot@agents.e2a.dev"):
            results.append(msg)

    assert len(results) == 1
    mock_client.get_message.assert_called_once_with("msg_456", agent_email="bot@agents.e2a.dev")


# ── listen() ─────────────────────────────────────────────────────


@pytest.mark.anyio
async def test_listen_missing_agent_email():
    from e2a.v1.websocket import listen

    mock_client = MagicMock()
    mock_client.agent_email = ""

    with pytest.raises(ValueError, match="agent_email is required"):
        async for _ in listen(mock_client):
            pass


@pytest.mark.anyio
async def test_listen_missing_websockets():
    """Should raise ImportError with install guidance when websockets is missing."""
    from e2a.v1.websocket import listen

    mock_client = MagicMock()
    mock_client.agent_email = "bot@agents.e2a.dev"

    # Temporarily remove websockets from sys.modules to simulate missing package
    with patch.dict(sys.modules, {"websockets": None}):
        with pytest.raises(ImportError, match="pip install e2a"):
            async for _ in listen(mock_client):
                pass


@pytest.mark.anyio
async def test_listen_no_reconnect_exits():
    """With reconnect=False, listen() should exit after first disconnect."""
    from e2a.v1.websocket import listen

    fake_email = MagicMock()
    mock_client = MagicMock()
    mock_client.agent_email = "bot@agents.e2a.dev"
    mock_client.api = MagicMock()
    mock_client.api.base_url = "https://e2a.dev"
    mock_client.api.api_key = "k"
    mock_client.get_message = AsyncMock(return_value=fake_email)

    call_count = 0

    async def fake_connect_and_stream(*args, **kwargs):
        nonlocal call_count
        call_count += 1
        if call_count == 1:
            yield fake_email
            return
        yield fake_email  # should never reach here

    mock_ws = MagicMock()
    with patch("e2a.v1.websocket._connect_and_stream", side_effect=fake_connect_and_stream), \
         patch.dict(sys.modules, {"websockets": mock_ws}):
        results = []
        async for msg in listen(mock_client, reconnect=False):
            results.append(msg)

    assert len(results) == 1
    assert call_count == 1

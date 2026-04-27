"""Notification-only WebSocket listener for e2a v1.

Connects to ``/api/v1/agents/{email}/ws`` and yields lightweight
:class:`WSNotification` objects — one per inbound message. The protocol
is server-to-client only; the client never sends application frames.

The notification carries metadata (message_id, sender, subject, etc.).
Callers fetch the full body via REST when they want it::

    async for notif in client.listen():
        if notif.subject.startswith("URGENT"):
            email = await client.get_message(notif.message_id)
            # ... act on the full email
        # else: drop the notification, no REST round-trip happened

This matches the server's design — the WS frame is intentionally small,
and the REST fetch (which marks the message read) stays explicit.

Requires ``websockets``::

    pip install e2a[ws]
"""

from __future__ import annotations

import asyncio
import json
import logging
from dataclasses import dataclass
from typing import TYPE_CHECKING, AsyncIterator, Optional
from urllib.parse import quote, urlencode, urlparse, urlunparse

if TYPE_CHECKING:
    from e2a.v1.async_client import AsyncE2AClient

logger = logging.getLogger("e2a.v1.websocket")


@dataclass
class WSNotification:
    """Lightweight notification pushed over the WebSocket on new inbound mail.

    Mirrors the wire shape sent by the server. ``from_`` is spelled with a
    trailing underscore to avoid Python's reserved word, matching the
    convention used elsewhere in the SDK (e.g. :attr:`MessageDetail.from_`).
    """

    message_id: str
    from_: str
    recipient: str
    subject: str
    received_at: str
    conversation_id: Optional[str] = None

    @classmethod
    def from_payload(cls, payload: dict) -> "WSNotification":
        """Build a notification from the raw dict pushed by the server.

        Tolerates older payload shapes that used ``to`` instead of ``recipient``.
        """
        return cls(
            message_id=payload.get("message_id", ""),
            from_=payload.get("from", ""),
            recipient=payload.get("recipient") or payload.get("to") or "",
            subject=payload.get("subject", ""),
            received_at=payload.get("received_at", ""),
            conversation_id=payload.get("conversation_id"),
        )


def _build_ws_url(base_url: str, agent_email: str, api_key: str) -> str:
    """Build the versioned WebSocket URL from the HTTP base URL."""
    parsed = urlparse(base_url)
    scheme = "wss" if parsed.scheme == "https" else "ws"
    path = f"/api/v1/agents/{quote(agent_email, safe='')}/ws"
    query = urlencode({"token": api_key})
    return urlunparse((scheme, parsed.netloc, path, "", query, ""))


async def listen(
    client: AsyncE2AClient,
    agent_email: Optional[str] = None,
    reconnect: bool = True,
    max_backoff: float = 30.0,
) -> AsyncIterator[WSNotification]:
    """Connect to e2a's v1 WebSocket and yield :class:`WSNotification` objects.

    Each notification is the lightweight metadata frame the server pushes —
    no body, no REST round-trip. Call ``await client.get_message(notif.message_id)``
    when you actually want the full email.

    Reconnects with exponential backoff (1s → ``max_backoff``) by default.
    The protocol is server-to-client only; no ACK frames are sent.
    """
    email = agent_email or client.agent_email
    if not email:
        raise ValueError(
            "agent_email is required. Pass it to listen() or set it on the client."
        )

    try:
        import websockets  # noqa: F401
    except ImportError:
        raise ImportError(
            "The 'websockets' package is required for listen(). "
            "Install it with: pip install e2a[ws]"
        )

    ws_url = _build_ws_url(client.api.base_url, email, client.api.api_key)
    backoff = 1.0

    while True:
        try:
            async for notif in _connect_and_stream(ws_url, email):
                yield notif
                backoff = 1.0  # Reset on successful message
        except Exception as exc:
            logger.warning("WebSocket disconnected: %s", exc)

        if not reconnect:
            break

        logger.info("Reconnecting in %.1fs...", backoff)
        await asyncio.sleep(backoff)
        backoff = min(backoff * 2, max_backoff)


async def _connect_and_stream(
    ws_url: str,
    agent_email: str,
) -> AsyncIterator[WSNotification]:
    """Connect once and yield notifications until disconnect.

    Does NOT send any frames (no ACK). Does NOT fetch the full message —
    that's the caller's call.
    """
    import websockets

    async with websockets.connect(ws_url) as ws:
        logger.info("Connected to WebSocket for %s", agent_email)

        async for raw in ws:
            try:
                payload = json.loads(raw)
                if not payload.get("message_id"):
                    logger.warning("WS notification missing message_id: %s", raw)
                    continue
                yield WSNotification.from_payload(payload)
            except Exception:
                logger.exception("Error processing WS notification")
                continue

"""Notification-only WebSocket listener for e2a v1.

Connects to ``/api/v1/agents/{email}/ws`` and yields full
``AsyncInboundEmail`` objects. The protocol is server-to-client only —
the client never sends application frames.

Requires ``websockets``::

    pip install e2a[ws]
"""

from __future__ import annotations

import asyncio
import json
import logging
from typing import TYPE_CHECKING, AsyncIterator, Optional
from urllib.parse import quote, urlencode, urlparse, urlunparse

if TYPE_CHECKING:
    from e2a.v1.async_client import AsyncE2AClient
    from e2a.v1.handler import AsyncInboundEmail

logger = logging.getLogger("e2a.v1.websocket")


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
) -> AsyncIterator[AsyncInboundEmail]:
    """Connect to e2a's v1 WebSocket and yield AsyncInboundEmail objects.

    On each notification (lightweight metadata), fetches the full message
    via REST (which marks it as read) and yields it.

    No ACK frames are sent — the protocol is server-to-client only.
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
            async for msg in _connect_and_stream(client, ws_url, email):
                yield msg
                backoff = 1.0  # Reset on successful message
        except Exception as exc:
            logger.warning("WebSocket disconnected: %s", exc)

        if not reconnect:
            break

        logger.info("Reconnecting in %.1fs...", backoff)
        await asyncio.sleep(backoff)
        backoff = min(backoff * 2, max_backoff)


async def _connect_and_stream(
    client: AsyncE2AClient,
    ws_url: str,
    agent_email: str,
) -> AsyncIterator[AsyncInboundEmail]:
    """Connect once and yield messages until disconnect.

    Does NOT send any frames (no ACK). The REST fetch marks messages read.
    """
    import websockets

    async with websockets.connect(ws_url) as ws:
        logger.info("Connected to WebSocket for %s", agent_email)

        async for raw in ws:
            try:
                notification = json.loads(raw)
                message_id = notification.get("message_id")
                if not message_id:
                    logger.warning("WS notification missing message_id: %s", raw)
                    continue

                # Fetch full message via REST (marks it as read)
                email = await client.get_message(message_id, agent_email=agent_email)
                yield email

            except Exception:
                logger.exception("Error processing WS notification")
                continue

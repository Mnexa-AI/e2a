"""Notification-only WebSocket stream for e2a /v1.

Connects to ``/v1/agents/{address}/ws`` and yields lightweight
:class:`WSNotification` objects — one per inbound message. The protocol is
server-to-client only; the client never sends application frames.

The notification carries metadata (message_id, sender, subject, …). Fetch the
full body via REST when you want it::

    async for notif in client.listen("bot@agents.e2a.dev"):
        email = await client.messages.get(notif.recipient, notif.message_id)

Requires ``websockets`` (``pip install e2a[ws]``).
"""

from __future__ import annotations

import asyncio
import json
import logging
from dataclasses import dataclass
from typing import AsyncIterator, Optional
from urllib.parse import quote, urlencode, urlparse, urlunparse

from .errors import E2AAuthError, E2AError, E2APermissionError

__all__ = ["WSNotification", "WSStream"]

logger = logging.getLogger("e2a.v1.websocket")


def _handshake_status(exc: BaseException) -> Optional[int]:
    """Return the HTTP status of a rejected WebSocket handshake, if any.

    The ``websockets`` library rejects a bad handshake with ``InvalidStatus``
    (modern; status on ``exc.response.status_code``) or the deprecated
    ``InvalidStatusCode`` (status on ``exc.status_code``). We probe both so this
    works across library versions without importing version-specific symbols.
    """
    resp = getattr(exc, "response", None)
    status = getattr(resp, "status_code", None)
    if isinstance(status, int):
        return status
    status = getattr(exc, "status_code", None)  # deprecated InvalidStatusCode
    if isinstance(status, int):
        return status
    return None


def _fatal_error_for_status(status: int, exc: BaseException) -> Optional[E2AError]:
    """Map a fatal (non-retryable) handshake status to a typed E2AError.

    Auth/permission and other 4xx handshake rejections are caller/credential
    bugs that will never succeed on retry, so we surface them instead of
    looping. 5xx and everything else stays transient.
    """
    if status == 401:
        return E2AAuthError(
            code="unauthorized",
            message=f"WebSocket handshake rejected: HTTP {status}",
            status=status,
            retryable=False,
        )
    if status == 403:
        return E2APermissionError(
            code="forbidden",
            message=f"WebSocket handshake rejected: HTTP {status}",
            status=status,
            retryable=False,
        )
    if 400 <= status < 500:
        return E2AError(
            code="ws_handshake_rejected",
            message=f"WebSocket handshake rejected: HTTP {status}",
            status=status,
            retryable=False,
        )
    return None


@dataclass
class WSNotification:
    """Lightweight notification pushed over the WebSocket on new inbound mail.

    ``from_`` is spelled with a trailing underscore to avoid Python's reserved
    word, matching the convention used elsewhere in the SDK.
    """

    message_id: str
    from_: str
    recipient: str
    subject: str
    received_at: str
    conversation_id: Optional[str] = None

    @classmethod
    def from_payload(cls, payload: dict) -> "WSNotification":
        return cls(
            message_id=payload.get("message_id", ""),
            from_=payload.get("from", ""),
            recipient=payload.get("recipient") or payload.get("to") or "",
            subject=payload.get("subject", ""),
            received_at=payload.get("received_at", ""),
            conversation_id=payload.get("conversation_id"),
        )


def _build_ws_url(base_url: str, agent_email: str, api_key: str) -> str:
    """Build the versioned WebSocket URL from the HTTP base URL.

    Note: auth is passed as a ``?token=`` query parameter, which can land in
    server/proxy access logs (a logged-credential limitation). A header- or
    ticket-based handshake is planned to replace it.
    """
    parsed = urlparse(base_url)
    scheme = "wss" if parsed.scheme == "https" else "ws"
    path = f"/v1/agents/{quote(agent_email, safe='')}/ws"
    query = urlencode({"token": api_key})
    return urlunparse((scheme, parsed.netloc, path, "", query, ""))


class WSStream:
    """Async-iterable notification stream returned by ``client.listen(address)``.

    Iterate it directly::

        async for notif in client.listen("bot@agents.e2a.dev"):
            ...

    Reconnects with exponential backoff (1s → ``max_backoff``) by default.
    """

    def __init__(
        self,
        *,
        api_key: str,
        agent_email: str,
        base_url: str,
        reconnect: bool = True,
        max_backoff: float = 30.0,
    ) -> None:
        self._url = _build_ws_url(base_url, agent_email, api_key)
        self._email = agent_email
        self._reconnect = reconnect
        self._max_backoff = max_backoff

    def __aiter__(self) -> AsyncIterator[WSNotification]:
        return self._stream()

    async def _stream(self) -> AsyncIterator[WSNotification]:
        try:
            import websockets  # noqa: F401
        except ImportError:  # pragma: no cover - optional dep
            raise ImportError(
                "The 'websockets' package is required for listen(). "
                "Install it with: pip install e2a[ws]"
            )

        backoff = 1.0
        while True:
            try:
                async for notif in _connect_and_stream(self._url, self._email):
                    yield notif
                    backoff = 1.0  # reset on a successful message
            except E2AError:
                # Already-typed fatal errors (e.g. raised below) propagate.
                raise
            except Exception as exc:  # noqa: BLE001
                # A fatal handshake rejection (auth/permission/4xx) will never
                # succeed on retry — surface it and stop instead of looping.
                status = _handshake_status(exc)
                if status is not None:
                    fatal = _fatal_error_for_status(status, exc)
                    if fatal is not None:
                        fatal.__cause__ = exc
                        raise fatal
                # Transient/network failure — log and reconnect.
                logger.warning("WebSocket disconnected: %s", exc)

            if not self._reconnect:
                return
            logger.info("Reconnecting in %.1fs...", backoff)
            await asyncio.sleep(backoff)
            backoff = min(backoff * 2, self._max_backoff)


async def _connect_and_stream(ws_url: str, agent_email: str) -> AsyncIterator[WSNotification]:
    """Connect once and yield notifications until disconnect. Sends no frames."""
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
            except Exception:  # noqa: BLE001
                logger.exception("Error processing WS notification")
                continue

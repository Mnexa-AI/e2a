"""In-process webhook event state for the single-worker tutorial app.

Production deployments should replace this with a durable store keyed by the
e2a event id. The small abstraction keeps the example's duplicate handling
explicit without coupling it to a particular database.
"""

from __future__ import annotations

import asyncio
from collections import deque
from typing import Literal

ClaimResult = Literal["new", "processing", "processed"]


def conversation_id_for(event_id: str, existing: str | None) -> str:
    """Return the upstream thread id or a retry-stable first-contact anchor."""
    if existing:
        return existing
    suffix = event_id.removeprefix("evt_")[:12]
    return f"conv_{suffix}"


class EventDeduper:
    """Track event claims so at-least-once webhook delivery is side-effect safe."""

    def __init__(self, *, max_processed: int = 10_000) -> None:
        if max_processed <= 0:
            raise ValueError("max_processed must be positive")
        self._lock = asyncio.Lock()
        self._max_processed = max_processed
        self._processing: set[str] = set()
        self._processed: set[str] = set()
        self._processed_order: deque[str] = deque()

    async def claim(self, event_id: str) -> ClaimResult:
        async with self._lock:
            if event_id in self._processed:
                return "processed"
            if event_id in self._processing:
                return "processing"
            self._processing.add(event_id)
            return "new"

    async def complete(self, event_id: str) -> None:
        async with self._lock:
            self._processing.discard(event_id)
            if event_id not in self._processed:
                self._processed.add(event_id)
                self._processed_order.append(event_id)
            while len(self._processed_order) > self._max_processed:
                expired = self._processed_order.popleft()
                self._processed.discard(expired)

    async def release(self, event_id: str) -> None:
        async with self._lock:
            self._processing.discard(event_id)

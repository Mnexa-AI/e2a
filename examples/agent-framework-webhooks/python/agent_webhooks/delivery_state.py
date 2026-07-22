"""Bounded in-process state for at-least-once webhook delivery."""

from __future__ import annotations

import asyncio
import hashlib
import re
from collections import deque
from typing import Literal

ClaimResult = Literal["new", "processing", "processed"]
_SAFE_CONVERSATION_SUFFIX = re.compile(r"^[A-Za-z0-9_-]+$")
_MAX_CONVERSATION_ID_LENGTH = 200


def conversation_id_for(event_id: str, existing: str | None) -> str:
    """Return the upstream thread id or a retry-stable first-contact anchor."""
    if existing is not None and existing.strip():
        return existing
    suffix = event_id.removeprefix("evt_")
    candidate = f"conv_{suffix}"
    if (
        suffix
        and len(candidate) <= _MAX_CONVERSATION_ID_LENGTH
        and _SAFE_CONVERSATION_SUFFIX.fullmatch(suffix)
    ):
        return candidate
    digest = hashlib.sha256(event_id.encode("utf-8")).hexdigest()
    return f"conv_{digest}"


class EventDeduper:
    """Track event claims so duplicate deliveries are side-effect safe."""

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

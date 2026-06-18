"""Async auto-pagination for the e2a Python SDK (Slice 8c).

The /v1 list endpoints return ``{items, next_cursor}``. :class:`AutoPager`
turns a page-fetch coroutine into an async iterable, so a caller writes
``async for m in client.messages.list(addr): ...`` and the cursor is threaded
for them — with guards against a non-advancing or ever-advancing cursor (either
would otherwise loop forever).
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import (
    AsyncIterator,
    Awaitable,
    Callable,
    Generic,
    List,
    Optional,
    Sequence,
    TypeVar,
)

T = TypeVar("T")

__all__ = ["Page", "AutoPager"]


@dataclass
class Page(Generic[T]):
    items: Sequence[T]
    #: None / "" → no more pages.
    next_cursor: Optional[str] = None


FetchPage = Callable[[Optional[str]], Awaitable["Page[T]"]]


class AutoPager(Generic[T]):
    """Async-iterable over cursor-paginated results."""

    def __init__(self, fetch_page: "FetchPage[T]", *, max_pages: int = 10_000) -> None:
        self._fetch_page = fetch_page
        #: Hard ceiling on pages fetched, to bound a server that returns an
        #: ever-advancing (never-repeating, never-null) cursor.
        self._max_pages = max_pages

    def __aiter__(self) -> AsyncIterator[T]:
        return self._iterate()

    async def _iterate(self) -> AsyncIterator[T]:
        cursor: Optional[str] = None
        seen: set[str] = set()  # every cursor already requested → cycle guard
        pages = 0
        while True:
            if pages >= self._max_pages:
                raise RuntimeError(
                    f"e2a pagination: exceeded {self._max_pages} pages; "
                    "aborting (cursor never terminated)"
                )
            page = await self._fetch_page(cursor)
            pages += 1
            for item in page.items or []:
                yield item

            nxt = page.next_cursor or None
            if not nxt:
                return  # null / empty → the last page
            if nxt == cursor or nxt in seen:
                raise RuntimeError(
                    "e2a pagination: next_cursor did not advance; "
                    "aborting to avoid an infinite loop"
                )
            if cursor is not None:
                seen.add(cursor)
            cursor = nxt

    async def to_list(self, *, limit: int) -> List[T]:
        """Collect up to ``limit`` items. The limit is required — it caps memory
        for an inbox that could page indefinitely."""
        if limit <= 0:
            raise ValueError("e2a pagination: to_list requires a positive limit")
        out: List[T] = []
        async for item in self:
            out.append(item)
            if len(out) >= limit:
                break
        return out

    async def for_each(self, fn: Callable[[T], Awaitable[Optional[bool]]]) -> None:
        """Invoke ``fn`` per item; return ``False`` from ``fn`` to stop early.
        ``fn`` may be a coroutine or a plain callable returning a bool/None."""
        async for item in self:
            result = fn(item)
            if hasattr(result, "__await__"):
                result = await result  # type: ignore[assignment]
            if result is False:
                return

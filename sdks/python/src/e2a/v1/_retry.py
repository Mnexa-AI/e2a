"""Retry + idempotency for the e2a Python SDK (Slice 8c).

The TypeScript SDK wraps the generated HTTP library to retry the *same* request
(idempotency key minted once, bytes reused). The generated Python (httpx) base
exposes no such seam — its ``ApiClient`` builds its own client — so we retry at
the hand-written resource layer instead:

* Mint an ``Idempotency-Key`` ONCE per write and pass it (via ``_headers``) on
  every attempt, so the server dedupes a retried send/reply/forward/approve.
* Retry only the SAFE subset on a retryable failure: reads, the server-deduped
  keyed writes, and HTTP-idempotent writes (PUT/PATCH/DELETE). A bare,
  non-idempotent POST (create/reject/redeliver/test) is NOT retried on an
  ambiguous failure — re-sending could double-create.
"""

from __future__ import annotations

import asyncio
import random as _random
import time as _time
import uuid
from typing import Any, Awaitable, Callable, Optional

import httpx

from .errors import (
    E2AError,
    connection_error,
    from_api_exception,
    is_retryable_status,
)
from .generated.exceptions import ApiException

# make_coro receives the per-attempt extra headers (the minted Idempotency-Key,
# or None for reads) and returns the awaitable generated call.
MakeCoro = Callable[[Optional["dict[str, str]"]], Awaitable[Any]]

IDEMPOTENCY_HEADER = "Idempotency-Key"


class RetryConfig:
    """Tunables (and test seams) for :func:`request_with_retry`."""

    def __init__(
        self,
        *,
        max_retries: int = 2,
        base_delay_ms: float = 200.0,
        max_delay_ms: float = 8_000.0,
        max_retry_after_ms: float = 60_000.0,
        max_elapsed_ms: Optional[float] = None,
        sleep: Callable[[float], Awaitable[None]] = asyncio.sleep,
        rand: Callable[[], float] = _random.random,
        now: Callable[[], float] = _time.monotonic,
        gen_idempotency_key: Callable[[], str] = lambda: uuid.uuid4().hex,
    ) -> None:
        self.max_retries = max_retries
        self.base_delay_ms = base_delay_ms
        self.max_delay_ms = max_delay_ms
        self.max_retry_after_ms = max_retry_after_ms
        self.max_elapsed_ms = max_elapsed_ms
        self.sleep = sleep
        self.rand = rand
        self.now = now
        self.gen_idempotency_key = gen_idempotency_key


def _backoff_ms(cfg: RetryConfig, attempt: int, exc: Optional[ApiException]) -> float:
    # Honor Retry-After (seconds) up to max_retry_after_ms; else exp + jitter.
    if exc is not None:
        retry_after = _retry_after_ms(exc)
        if retry_after is not None:
            return min(retry_after, cfg.max_retry_after_ms)
    exp = min(cfg.base_delay_ms * (2 ** attempt), cfg.max_delay_ms)
    return exp * (0.5 + 0.5 * cfg.rand())  # full jitter over [0.5, 1.0]


def _retry_after_ms(exc: ApiException) -> Optional[float]:
    headers = getattr(exc, "headers", None)
    if not headers:
        return None
    try:
        items = headers.items()
    except AttributeError:
        return None
    for k, v in items:
        if k.lower() == "retry-after":
            try:
                secs = float(v)
                if secs >= 0:
                    return secs * 1000.0
            except (TypeError, ValueError):
                return None
    return None


async def request_with_retry(
    make_coro: MakeCoro,
    *,
    cfg: RetryConfig,
    retryable: bool,
    idempotency: bool,
    idempotency_key: Optional[str] = None,
) -> Any:
    """Execute a generated call with retry + idempotency.

    :param retryable: whether a retryable failure (429/5xx/connection) should be
        retried. False for non-idempotent POSTs.
    :param idempotency: whether to mint/forward an Idempotency-Key header.
    :param idempotency_key: caller-supplied stable key; minted if omitted.
    """
    headers: Optional["dict[str, str]"] = None
    if idempotency:
        key = idempotency_key or cfg.gen_idempotency_key()
        headers = {IDEMPOTENCY_HEADER: key}

    start = cfg.now()
    attempt = 0
    while True:
        exc: Optional[BaseException] = None
        api_exc: Optional[ApiException] = None
        try:
            return await make_coro(headers)
        except ApiException as e:
            api_exc = e
            exc = e
            can_retry = retryable and is_retryable_status(int(getattr(e, "status", 0) or 0))
        except httpx.TransportError as e:  # connection-level: no HTTP response
            exc = e
            can_retry = retryable
        except E2AError:
            raise  # already typed (e.g. a nested helper) — pass through
        except httpx.HTTPError as e:
            # Non-transport httpx error (InvalidURL, etc.) — not retryable, but
            # surface it as a typed E2AError rather than a raw httpx exception.
            raise connection_error(str(e), cause=e)

        if not can_retry or attempt >= cfg.max_retries:
            raise _as_e2a_error(exc, api_exc)

        delay_ms = _backoff_ms(cfg, attempt, api_exc)
        if cfg.max_elapsed_ms is not None and (cfg.now() - start) * 1000.0 + delay_ms > cfg.max_elapsed_ms:
            raise _as_e2a_error(exc, api_exc)
        await cfg.sleep(delay_ms / 1000.0)
        attempt += 1


def _as_e2a_error(exc: Optional[BaseException], api_exc: Optional[ApiException]) -> E2AError:
    if api_exc is not None:
        return from_api_exception(api_exc)
    message = str(exc) if exc is not None else "connection error"
    return connection_error(message, cause=exc)

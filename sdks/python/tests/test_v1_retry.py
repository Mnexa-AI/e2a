"""Unit tests for retry + idempotency (Slice 8c-1)."""

import json

import httpx
import pytest

from e2a.v1._retry import RetryConfig, request_with_retry
from e2a.v1.errors import (
    E2AConflictError,
    E2AConnectionError,
    E2AIdempotencyError,
    E2ANotFoundError,
    E2AServerError,
)
from e2a.v1.generated.exceptions import ApiException


class Script:
    """Replays a list of steps; records the headers each attempt saw."""

    def __init__(self, steps):
        self.steps = list(steps)
        self.seen_headers = []
        self.calls = 0

    async def make(self, headers):
        self.calls += 1
        self.seen_headers.append(headers)
        step = self.steps.pop(0)
        if isinstance(step, BaseException):
            raise step
        return step


def _api_exc(status, headers=None, body="{}"):
    e = ApiException(status=status, body=body)
    e.headers = headers
    return e


def _conflict_exc(code, headers=None):
    """A 409 carrying `error.code` in its envelope body — the shape the retry
    layer must parse to tell idempotency_in_flight (retry-safe) apart from
    any unrelated 409 code (never retry-safe)."""
    return _api_exc(409, headers=headers, body=json.dumps({"error": {"code": code, "message": "x"}}))


def cfg(**kw):
    async def no_sleep(_s):
        return None

    kw.setdefault("sleep", no_sleep)
    return RetryConfig(**kw)


@pytest.mark.anyio
async def test_retries_500_then_returns_200():
    s = Script([_api_exc(500), "ok"])
    out = await request_with_retry(s.make, cfg=cfg(), retryable=True, idempotency=False)
    assert out == "ok"
    assert s.calls == 2


@pytest.mark.anyio
async def test_retries_connection_error_then_succeeds():
    s = Script([httpx.ConnectError("refused"), "ok"])
    out = await request_with_retry(s.make, cfg=cfg(), retryable=True, idempotency=True)
    assert out == "ok"
    assert s.calls == 2


@pytest.mark.anyio
async def test_no_retry_on_404():
    s = Script([_api_exc(404)])
    with pytest.raises(E2ANotFoundError):
        await request_with_retry(s.make, cfg=cfg(), retryable=True, idempotency=False)
    assert s.calls == 1


@pytest.mark.anyio
async def test_stops_after_max_retries():
    s = Script([_api_exc(503), _api_exc(503), _api_exc(503)])
    with pytest.raises(E2AServerError):
        await request_with_retry(s.make, cfg=cfg(max_retries=2), retryable=True, idempotency=False)
    assert s.calls == 3  # 1 + 2 retries


@pytest.mark.anyio
async def test_unsafe_write_is_not_retried():
    # A non-idempotent POST (retryable=False) must not re-send on an ambiguous
    # failure even though the status is retryable — avoids double-create.
    s = Script([_api_exc(503)])
    with pytest.raises(E2AServerError):
        await request_with_retry(s.make, cfg=cfg(), retryable=False, idempotency=True)
    assert s.calls == 1


@pytest.mark.anyio
async def test_connection_error_not_retried_when_unsafe():
    s = Script([httpx.ConnectError("refused")])
    with pytest.raises(E2AConnectionError):
        await request_with_retry(s.make, cfg=cfg(), retryable=False, idempotency=True)
    assert s.calls == 1


@pytest.mark.anyio
async def test_mints_idempotency_key_once_and_reuses_it():
    s = Script([_api_exc(500), "ok"])
    n = {"v": 0}

    def gen():
        n["v"] += 1
        return f"k-{n['v']}"

    await request_with_retry(
        s.make, cfg=cfg(gen_idempotency_key=gen), retryable=True, idempotency=True
    )
    assert s.seen_headers[0] == {"Idempotency-Key": "k-1"}
    assert s.seen_headers[1] == {"Idempotency-Key": "k-1"}  # same key on retry
    assert n["v"] == 1  # generated exactly once


@pytest.mark.anyio
async def test_caller_supplied_key_wins():
    s = Script([_api_exc(500), "ok"])
    await request_with_retry(
        s.make,
        cfg=cfg(gen_idempotency_key=lambda: "should-not-be-used"),
        retryable=True,
        idempotency=True,
        idempotency_key="caller-key",
    )
    assert s.seen_headers == [{"Idempotency-Key": "caller-key"}, {"Idempotency-Key": "caller-key"}]


@pytest.mark.anyio
async def test_reads_carry_no_idempotency_header():
    s = Script(["ok"])
    await request_with_retry(s.make, cfg=cfg(), retryable=True, idempotency=False)
    assert s.seen_headers == [None]


@pytest.mark.anyio
async def test_retry_after_header_drives_backoff():
    delays = []

    async def rec_sleep(secs):
        delays.append(secs)

    s = Script([_api_exc(429, headers={"Retry-After": "12"}), "ok"])
    await request_with_retry(
        s.make, cfg=cfg(sleep=rec_sleep, max_retries=1), retryable=True, idempotency=False
    )
    assert delays == [12.0]


@pytest.mark.anyio
async def test_retry_after_clamped_to_ceiling():
    delays = []

    async def rec_sleep(secs):
        delays.append(secs)

    s = Script([_api_exc(503, headers={"Retry-After": "99999"}), "ok"])
    await request_with_retry(
        s.make,
        cfg=cfg(sleep=rec_sleep, max_retries=1, max_retry_after_ms=5000),
        retryable=True,
        idempotency=False,
    )
    assert delays == [5.0]  # 5000ms, not ~99999s


@pytest.mark.anyio
async def test_non_transport_httpx_error_wrapped():
    # Regression: a non-TransportError httpx error must surface as a typed
    # E2AError, not a raw httpx exception. Not retried.
    async def make(_headers):
        raise httpx.HTTPError("boom")

    with pytest.raises(E2AConnectionError):
        await request_with_retry(make, cfg=cfg(), retryable=True, idempotency=False)


@pytest.mark.anyio
async def test_total_deadline_stops_before_retry():
    s = Script([_api_exc(503), _api_exc(503)])
    # Frozen clock + max jitter => backoff(0)=200ms > max_elapsed_ms 100.
    clock = {"t": 1000.0}
    with pytest.raises(E2AServerError):
        await request_with_retry(
            s.make,
            cfg=cfg(rand=lambda: 1.0, now=lambda: clock["t"], max_elapsed_ms=100, max_retries=5),
            retryable=True,
            idempotency=False,
        )
    assert s.calls == 1  # deadline blocked the first retry


# 409 is NOT in is_retryable_status (a bare 409 is never retried), but the
# server's idempotency_in_flight code on a server-deduped keyed write IS
# retry-safe (same key, still committing). Other 409 codes are not retried;
# idempotency_key_reuse is the separate frozen 422 caller-error contract.
@pytest.mark.anyio
async def test_retries_409_idempotency_in_flight_then_succeeds():
    s = Script([_conflict_exc("idempotency_in_flight"), "ok"])
    out = await request_with_retry(s.make, cfg=cfg(), retryable=True, idempotency=True)
    assert out == "ok"
    assert s.calls == 2


@pytest.mark.anyio
async def test_no_retry_on_422_idempotency_key_reuse():
    s = Script([_api_exc(422, body='{"error":{"code":"idempotency_key_reuse","message":"x"}}')])
    with pytest.raises(E2AIdempotencyError):
        await request_with_retry(s.make, cfg=cfg(), retryable=True, idempotency=True)
    assert s.calls == 1  # caller bug, not transient — never retried


@pytest.mark.anyio
async def test_no_retry_on_bare_409_with_unrelated_code():
    s = Script([_conflict_exc("conflict")])
    with pytest.raises(E2AConflictError):
        await request_with_retry(s.make, cfg=cfg(), retryable=True, idempotency=True)
    assert s.calls == 1


@pytest.mark.anyio
async def test_no_retry_on_409_idempotency_in_flight_without_idempotency_gate():
    # retryable=True alone (e.g. a plain read, `_read`'s gating) is not enough:
    # this response shape can't legitimately occur without an Idempotency-Key,
    # and the body-peek must not fire for a request that never carried one.
    s = Script([_conflict_exc("idempotency_in_flight")])
    with pytest.raises(E2AIdempotencyError):
        await request_with_retry(s.make, cfg=cfg(), retryable=True, idempotency=False)
    assert s.calls == 1


@pytest.mark.anyio
async def test_409_idempotency_in_flight_retry_honors_retry_after_backoff():
    delays = []

    async def rec_sleep(secs):
        delays.append(secs)

    s = Script([_conflict_exc("idempotency_in_flight", headers={"Retry-After": "3"}), "ok"])
    await request_with_retry(
        s.make, cfg=cfg(sleep=rec_sleep, max_retries=1), retryable=True, idempotency=True
    )
    assert delays == [3.0]

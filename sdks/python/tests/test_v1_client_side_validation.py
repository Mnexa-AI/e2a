"""Regression tests: client-side ``@validate_call`` failures must be typed.

The generated ``*Api`` methods carry pydantic parameter constraints from the
OpenAPI schema (e.g. ``limit`` ``Field(le=100, ge=1)``), so an out-of-range
argument raises BEFORE any HTTP request is sent. That raw
``pydantic_core.ValidationError`` used to leak to callers, breaking the
documented "catch ``E2AError``" contract (and the parity with the TS SDK,
where the server's 422 maps to ``E2AValidationError``). The retry layer now
maps it to :class:`E2AValidationError` — these tests pin that on both the
async client and the sync facade.

No HTTP mock responses are registered on purpose: the whole point is that the
failure happens pre-flight. ``httpx_mock`` is still requested so an accidental
real request could never escape to the network.
"""

import pydantic
import pytest

from e2a.v1 import AsyncE2AClient, E2AClient
from e2a.v1.errors import E2AError, E2AValidationError

BASE = "http://test.local"


@pytest.fixture(autouse=True)
def _clear_env(monkeypatch):
    for v in ("E2A_API_KEY", "E2A_API_URL", "E2A_BASE_URL", "E2A_AGENT_EMAIL"):
        monkeypatch.delenv(v, raising=False)


def _assert_typed(err: E2AValidationError) -> None:
    # The SDK contract: a typed E2AError, never a raw pydantic error.
    assert isinstance(err, E2AError)
    assert not isinstance(err, pydantic.ValidationError)
    assert err.code == "invalid_request"
    assert err.status == 0  # pre-flight: no HTTP round-trip happened
    assert err.request_id is None
    assert err.retryable is False
    # The pydantic detail is preserved (offending param + constraint) ...
    assert "limit" in str(err.details)
    assert "less_than_equal" in str(err.details)
    # ... and the original ValidationError is chained for debugging.
    assert isinstance(err.__cause__, pydantic.ValidationError)


@pytest.mark.anyio
async def test_async_out_of_range_limit_raises_e2a_validation_error(httpx_mock):
    async with AsyncE2AClient(api_key="e2a_test", base_url=BASE) as c:
        with pytest.raises(E2AValidationError) as ei:
            await c.messages.list("bot@test.dev", limit=99999).page()
    _assert_typed(ei.value)


@pytest.mark.anyio
async def test_async_out_of_range_limit_caught_by_except_e2a_error(httpx_mock):
    # The documented contract verbatim: `except E2AError:` catches everything
    # the SDK raises — including pre-flight validation failures.
    async with AsyncE2AClient(api_key="e2a_test", base_url=BASE) as c:
        try:
            await c.messages.list("bot@test.dev", limit=99999).page()
        except E2AError as e:
            assert isinstance(e, E2AValidationError)
        else:  # pragma: no cover - fails the regression
            pytest.fail("expected E2AError")


def test_sync_out_of_range_limit_raises_e2a_validation_error(httpx_mock):
    with E2AClient(api_key="e2a_test", base_url=BASE) as c:
        with pytest.raises(E2AValidationError) as ei:
            c.messages.list("bot@test.dev", limit=99999).page()
    _assert_typed(ei.value)

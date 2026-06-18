"""Unit tests for the typed error hierarchy (Slice 8c-1)."""

from e2a.v1.errors import (
    E2AAuthError,
    E2AConflictError,
    E2AConnectionError,
    E2AError,
    E2AIdempotencyError,
    E2ANotFoundError,
    E2APermissionError,
    E2ARateLimitError,
    E2AServerError,
    E2AValidationError,
    connection_error,
    from_api_exception,
    is_retryable_status,
)
from e2a.v1.oag.exceptions import ApiException


def _exc(status, body=None, headers=None):
    e = ApiException(status=status, body=body)
    e.headers = headers
    return e


def test_status_to_class_mapping():
    cases = {
        401: E2AAuthError,
        403: E2APermissionError,
        404: E2ANotFoundError,
        409: E2AConflictError,
        422: E2AValidationError,
        429: E2ARateLimitError,
        500: E2AServerError,
        503: E2AServerError,
    }
    for status, klass in cases.items():
        err = from_api_exception(_exc(status, body="{}"))
        assert isinstance(err, klass), f"{status} -> {type(err).__name__}"
        assert err.status == status


def test_retryable_flags():
    assert from_api_exception(_exc(429, body="{}")).retryable is True
    assert from_api_exception(_exc(500, body="{}")).retryable is True
    assert from_api_exception(_exc(404, body="{}")).retryable is False
    assert from_api_exception(_exc(422, body="{}")).retryable is False


def test_idempotency_code_wins_over_status():
    in_flight = from_api_exception(
        _exc(409, body='{"error":{"code":"idempotency_in_flight","message":"wait"}}')
    )
    assert isinstance(in_flight, E2AIdempotencyError)
    assert in_flight.retryable is True

    reuse = from_api_exception(
        _exc(422, body='{"error":{"code":"idempotency_key_reuse","message":"bad"}}')
    )
    assert isinstance(reuse, E2AIdempotencyError)
    assert reuse.retryable is False


def test_envelope_fields_surfaced():
    err = from_api_exception(
        _exc(
            404,
            body='{"error":{"code":"agent_not_found","message":"no such agent","details":{"x":1}}}',
            headers={"X-Request-Id": "req_abc", "Content-Type": "application/json"},
        )
    )
    assert err.code == "agent_not_found"
    assert err.message == "no such agent"
    assert err.details == {"x": 1}
    assert err.request_id == "req_abc"


def test_non_json_body_falls_back_to_status_bucket():
    err = from_api_exception(_exc(503, body="<html>502 bad gateway</html>"))
    assert isinstance(err, E2AServerError)
    assert err.code == "internal_error"


def test_retry_after_numeric_and_http_date():
    numeric = from_api_exception(_exc(429, body="{}", headers={"Retry-After": "12"}))
    assert numeric.retry_after_seconds == 12

    # An HTTP-date in the past clamps to >= 0.
    dated = from_api_exception(
        _exc(503, body="{}", headers={"Retry-After": "Wed, 21 Oct 2015 07:28:00 GMT"})
    )
    assert dated.retry_after_seconds is not None
    assert dated.retry_after_seconds >= 0


def test_missing_envelope_does_not_crash():
    for body in (None, "", "null", "[]", '"a string"', "12"):
        err = from_api_exception(_exc(500, body=body))
        assert isinstance(err, E2AError)
        assert err.status == 500


def test_list_valued_retry_after_does_not_crash():
    # Regression: a non-string Retry-After must not raise TypeError out of the mapper.
    err = from_api_exception(_exc(503, body="{}", headers={"retry-after": ["12", "34"]}))
    assert isinstance(err, E2AServerError)
    assert err.retry_after_seconds is None


def test_connection_error_helper():
    err = connection_error("refused", cause=OSError("ECONNREFUSED"))
    assert isinstance(err, E2AConnectionError)
    assert err.status == 0
    assert err.retryable is True
    assert err.code == "connection_error"


def test_is_retryable_status():
    assert is_retryable_status(429) is True
    assert is_retryable_status(408) is True
    assert is_retryable_status(500) is True
    assert is_retryable_status(599) is True
    assert is_retryable_status(404) is False
    assert is_retryable_status(200) is False

"""Typed error hierarchy for the e2a Python SDK (Slice 8c).

Mirrors the TypeScript SDK's error model. The /v1 surface returns a uniform
envelope ``{"error": {"code", "message", "request_id", "details"}}``
(internal/httpapi/errors.go). We map it to typed exceptions so a caller can
branch with ``except E2ANotFoundError`` and read ``.code`` / ``.status`` /
``.request_id`` / ``.retryable`` without parsing bodies.

Class selection is genuinely code-first: a known, stable ``error.code`` (see
``_CODE_MAP`` and the ``*_not_found`` / ``*_exists`` suffix conventions) maps to
its typed class *regardless of the HTTP status it arrives on*, so a code carried
on an unexpected status no longer degrades to the bare base error. Unknown or
empty codes fall back to the HTTP status bucket, which preserves every
status→class outcome (401→auth, 403→permission, 404→not-found, 409→conflict,
422→validation, 429→rate-limit, 5xx/408→server). The two idempotency codes are
the most specific and win over both.
"""

from __future__ import annotations

import json
from email.utils import parsedate_to_datetime
from typing import Any, Mapping, Optional

from .generated.exceptions import ApiException

__all__ = [
    "E2AError",
    "E2AAuthError",
    "E2APermissionError",
    "E2ANotFoundError",
    "E2AConflictError",
    "E2AValidationError",
    "E2AIdempotencyError",
    "E2ARateLimitError",
    "E2AServerError",
    "E2AConnectionError",
    "E2AWebhookSignatureError",
    "is_retryable_status",
    "from_api_exception",
    "connection_error",
]


class E2AError(Exception):
    """Base class for every error the SDK raises."""

    def __init__(
        self,
        *,
        code: str,
        message: str,
        status: int,
        retryable: bool,
        request_id: Optional[str] = None,
        details: Any = None,
        retry_after_seconds: Optional[float] = None,
    ) -> None:
        super().__init__(message)
        #: Stable machine code from the envelope (e.g. ``"domain_not_verified"``).
        self.code = code
        self.message = message
        #: HTTP status; ``0`` for a connection-level failure with no response.
        self.status = status
        #: ``X-Request-Id`` echoed by the server — quote it in support requests.
        self.request_id = request_id
        #: Structured field-level detail (envelope ``error.details``).
        self.details = details
        #: True when retrying could plausibly succeed (429 / 5xx / connection).
        self.retryable = retryable
        #: Seconds from a ``Retry-After`` header, when present.
        self.retry_after_seconds = retry_after_seconds


class E2AAuthError(E2AError):
    """401 — missing/invalid credentials."""


class E2APermissionError(E2AError):
    """403 — authenticated but not allowed (also: existence-hiding on get)."""


class E2ANotFoundError(E2AError):
    """404 — no such resource."""


class E2AConflictError(E2AError):
    """409 — state conflict (already exists, etc.)."""


class E2AValidationError(E2AError):
    """422 — input validation failure."""


class E2AIdempotencyError(E2AError):
    """idempotency_in_flight / idempotency_key_reuse."""


class E2ARateLimitError(E2AError):
    """429 — rate limited."""


class E2AServerError(E2AError):
    """5xx / 408 — server-side or timeout."""


class E2AConnectionError(E2AError):
    """No HTTP response (DNS, refused, reset, timeout)."""


class E2AWebhookSignatureError(E2AError):
    """Local webhook signature verification failure."""


def is_retryable_status(status: int) -> bool:
    """Status codes where a retry could plausibly help (excludes connection —
    that path is handled separately, since there's no status to inspect)."""
    return status == 408 or status == 429 or (500 <= status <= 599)


# The two idempotency-conflict codes from internal/httpapi/idempotency.go.
# in_flight is safe to retry (same key, server dedupes); key_reuse is a caller
# bug (same key, different body) and must not be retried.
_IDEMPOTENCY_RETRYABLE = "idempotency_in_flight"
_IDEMPOTENCY_CODES = {_IDEMPOTENCY_RETRYABLE, "idempotency_key_reuse"}

_DEFAULT_CODE = {
    400: "invalid_request",
    401: "unauthorized",
    403: "forbidden",
    404: "not_found",
    409: "conflict",
    422: "unprocessable_entity",
    429: "rate_limited",
}

# Code-first map: a known, stable server `code` selects the typed class
# regardless of the HTTP status it arrives on, so a code carried on an
# unexpected status no longer degrades to the bare E2AError. Seeded from the
# codes the /v1 server emits (internal/httpapi/errors.go defaultCodeForStatus
# + the NewError(...) call sites in internal/httpapi/*.go). Unknown codes fall
# through to the status bucket below, preserving every status→class outcome.
_CODE_MAP: "dict[str, tuple[type[E2AError], bool]]" = {
    # 401 family
    "unauthorized": (E2AAuthError, False),
    # 403 family
    "forbidden": (E2APermissionError, False),
    # 404 family — also covers *_not_found via the suffix check in _resolve.
    "not_found": (E2ANotFoundError, False),
    "gone": (E2ANotFoundError, False),
    # 409 family — also covers *_exists via the suffix check in _resolve.
    "conflict": (E2AConflictError, False),
    "webhook_cooldown": (E2AConflictError, False),
    "webhook_disabled": (E2AConflictError, False),
    # 4xx validation / bad-request family
    "domain_not_verified": (E2AValidationError, False),
    "invalid_request": (E2AValidationError, False),
    "bad_request": (E2AValidationError, False),
    "unprocessable_entity": (E2AValidationError, False),
    "invalid_cursor": (E2AValidationError, False),
    # 429
    "rate_limited": (E2ARateLimitError, True),
}


def _resolve(status: int, code: str) -> "tuple[type[E2AError], bool]":
    # 1. Idempotency codes are the most specific — they win over everything.
    if code in _IDEMPOTENCY_CODES:
        return E2AIdempotencyError, (code == _IDEMPOTENCY_RETRYABLE)
    # 2. Code-first: a known stable code selects the class regardless of status.
    if code:
        if code in _CODE_MAP:
            return _CODE_MAP[code]
        # Conventional suffixes the server uses across many resources.
        if code.endswith("_not_found"):
            return E2ANotFoundError, False
        if code.endswith("_exists"):
            return E2AConflictError, False
    # 3. Fall back to the HTTP status bucket for unknown/empty codes.
    by_status: "dict[int, tuple[type[E2AError], bool]]" = {
        401: (E2AAuthError, False),
        403: (E2APermissionError, False),
        404: (E2ANotFoundError, False),
        409: (E2AConflictError, False),
        422: (E2AValidationError, False),
        429: (E2ARateLimitError, True),
    }
    if status in by_status:
        return by_status[status]
    if is_retryable_status(status):
        return E2AServerError, True
    return E2AError, False


def _header_get(headers: Optional[Mapping[str, str]], name: str) -> Optional[str]:
    if not headers:
        return None
    lower = name.lower()
    for k, v in headers.items():
        if k.lower() == lower:
            return v
    return None


def _parse_retry_after(headers: Optional[Mapping[str, str]]) -> Optional[float]:
    v = _header_get(headers, "retry-after")
    if not v or not isinstance(v, str):
        return None
    try:
        secs = float(v)
        if secs >= 0:
            return secs
    except (TypeError, ValueError):
        pass
    # RFC 9110 §10.2.3 also allows an HTTP-date (common behind CDNs).
    try:
        dt = parsedate_to_datetime(v)
    except (TypeError, ValueError):
        return None
    if dt is None:
        return None
    import time

    return max(0.0, dt.timestamp() - time.time())


def _retry_after_from_details(details: Any) -> Optional[float]:
    """Read ``details.retry_after_seconds`` (a number) when present.

    Used as a fallback when the ``Retry-After`` header is absent — the send-path
    429 carries its retry hint in the body, not the header.
    """
    if not isinstance(details, Mapping):
        return None
    v = details.get("retry_after_seconds")
    if isinstance(v, bool):  # bool is a subclass of int — reject it.
        return None
    if isinstance(v, (int, float)):
        return max(0.0, float(v))
    return None


def to_e2a_error(
    *,
    status: int,
    code: str = "",
    message: Optional[str] = None,
    request_id: Optional[str] = None,
    details: Any = None,
    headers: Optional[Mapping[str, str]] = None,
    cause: Optional[BaseException] = None,
) -> E2AError:
    """Build a typed error from status + the parsed envelope fields."""
    resolved_code = code or _DEFAULT_CODE.get(status) or (
        "internal_error" if status >= 500 else "error"
    )
    klass, retryable = _resolve(status, code)
    # Prefer the Retry-After header; else fall back to the error body's
    # details.retry_after_seconds. The send-path 429 carries its hint in the
    # body (not the header) because of a Huma constraint, so we honor both.
    retry_after = _parse_retry_after(headers)
    if retry_after is None:
        retry_after = _retry_after_from_details(details)
    err = klass(
        code=resolved_code,
        message=message or f"e2a API error ({status})",
        status=status,
        request_id=request_id,
        details=details,
        retryable=retryable,
        retry_after_seconds=retry_after,
    )
    if cause is not None:
        err.__cause__ = cause
    return err


def from_api_exception(exc: ApiException) -> E2AError:
    """Map a generated ``ApiException`` (raised by the generated ``*Api`` classes on a
    non-2xx response) to a typed :class:`E2AError`."""
    headers = _normalize_headers(getattr(exc, "headers", None))
    request_id = _header_get(headers, "x-request-id")
    status = int(getattr(exc, "status", 0) or 0)

    code = ""
    message = getattr(exc, "reason", None) or str(exc)
    details: Any = None
    env = _parse_envelope(getattr(exc, "data", None), getattr(exc, "body", None))
    if isinstance(env, dict):
        error = env.get("error")
        if isinstance(error, dict):
            code = error.get("code") or ""
            message = error.get("message") or message
            details = error.get("details")

    return to_e2a_error(
        status=status,
        code=code,
        message=message,
        request_id=request_id,
        details=details,
        headers=headers,
        cause=exc,
    )


def _normalize_headers(headers: Any) -> Optional[Mapping[str, str]]:
    if headers is None:
        return None
    if isinstance(headers, Mapping):
        return headers
    # httpx.Headers / list of pairs both iterate as items().
    try:
        return dict(headers.items())  # type: ignore[no-any-return]
    except AttributeError:
        try:
            return dict(headers)
        except (TypeError, ValueError):
            return None


def _parse_envelope(data: Any, body: Any) -> Any:
    # The generated layer may hand us a deserialized model, a dict, or a raw
    # string body. Prefer a dict-shaped value; fall back to JSON-parsing body.
    if isinstance(data, dict):
        return data
    if hasattr(data, "to_dict"):
        try:
            return data.to_dict()
        except Exception:  # pragma: no cover - defensive
            pass
    if isinstance(body, (str, bytes)):
        try:
            return json.loads(body)
        except (ValueError, TypeError):
            return None
    return None


def connection_error(message: str, cause: Optional[BaseException] = None) -> E2AConnectionError:
    """A connection-level failure with no HTTP response."""
    err = E2AConnectionError(
        code="connection_error",
        message=message,
        status=0,
        retryable=True,
    )
    if cause is not None:
        err.__cause__ = cause
    return err

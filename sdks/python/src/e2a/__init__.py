# Top-level convenience alias — points to the current stable API version (v1).
#
# The pinned contract path is `e2a.v1`:
#   from e2a.v1 import AsyncE2AClient
#
# The top-level `e2a` package re-exports that surface for convenience.
#
# `E2AClient` is deliberately NOT exported — the name is reserved for a future
# synchronous client. Importing it raises a guided ImportError (see __getattr__).

from e2a.v1 import (  # noqa: F401
    AsyncE2AClient,
    AutoPager,
    E2AAuthError,
    E2AConflictError,
    E2AConnectionError,
    E2AError,
    E2AIdempotencyError,
    E2ALimitExceededError,
    E2ANotFoundError,
    E2APermissionError,
    E2ARateLimitError,
    E2AServerError,
    E2AValidationError,
    E2AWebhookSignatureError,
    Page,
    WebhookEvent,
    WSNotification,
    WSStream,
    construct_event,
    models,
    verify_webhook_signature,
)

def __getattr__(name: str):
    if name == "E2AClient":
        raise ImportError(
            "E2AClient was renamed to AsyncE2AClient in v5; "
            "E2AClient is reserved for a future synchronous client"
        )
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")


__all__ = [
    "AsyncE2AClient",
    "E2AError",
    "E2AAuthError",
    "E2APermissionError",
    "E2ANotFoundError",
    "E2AConflictError",
    "E2AValidationError",
    "E2AIdempotencyError",
    "E2ALimitExceededError",
    "E2ARateLimitError",
    "E2AServerError",
    "E2AConnectionError",
    "E2AWebhookSignatureError",
    "AutoPager",
    "Page",
    "verify_webhook_signature",
    "construct_event",
    "WebhookEvent",
    "WSNotification",
    "WSStream",
    "models",
]

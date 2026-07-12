# Top-level convenience alias — points to the current stable API version (v1).
#
# The pinned contract path is `e2a.v1`:
#   from e2a.v1 import E2AClient
#
# The top-level `e2a` package re-exports that surface for convenience.

from e2a.v1 import (  # noqa: F401
    AutoPager,
    E2AAuthError,
    E2AClient,
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

__all__ = [
    "E2AClient",
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

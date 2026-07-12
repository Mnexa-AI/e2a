# Top-level convenience alias — points to the current stable API version (v1).
#
# The pinned contract path is `e2a.v1`:
#   from e2a.v1 import E2AClient        # sync
#   from e2a.v1 import AsyncE2AClient   # async
#
# The top-level `e2a` package re-exports that surface for convenience.

from e2a.v1 import (  # noqa: F401
    AsyncE2AClient,
    AutoPager,
    E2AClient,
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
    SyncAutoPager,
    SyncStream,
    WebhookEvent,
    WSNotification,
    WSStream,
    construct_event,
    models,
    verify_webhook_signature,
)

__all__ = [
    "E2AClient",
    "AsyncE2AClient",
    "SyncAutoPager",
    "SyncStream",
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

"""Public surface of the e2a v1 SDK (async-only).

The canonical request/response types are the OpenAPI-Generator ``generated``
models; the hand-written ergonomic layer (:class:`E2AClient` + resources, the
typed error hierarchy, retry/pagination, webhook verification, WS) wraps them.
The legacy flat/sync ``api`` / ``client`` / ``handler`` surface and the old
swag-generated Pydantic types have been retired in favour of this.
"""

# Generated request/response models (the canonical types).
from e2a.v1.generated import models  # noqa: F401
from e2a.v1.generated.models import *  # noqa: F401,F403

# High-level async client.
from e2a.v1.client import E2AClient  # noqa: F401

# Typed error hierarchy.
from e2a.v1.errors import (  # noqa: F401
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
)

# Auto-pagination.
from e2a.v1.pagination import AutoPager, Page  # noqa: F401

# Webhook signature verification.
from e2a.v1.webhook_signature import (  # noqa: F401
    WebhookEvent,
    construct_event,
    verify_webhook_signature,
)

# Real-time WebSocket stream.
from e2a.v1.websocket import WSNotification, WSStream  # noqa: F401

__all__ = [
    "E2AClient",
    # Errors
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
    # Pagination
    "AutoPager",
    "Page",
    # Webhooks
    "verify_webhook_signature",
    "construct_event",
    "WebhookEvent",
    # WebSocket
    "WSNotification",
    "WSStream",
    # Generated models namespace
    "models",
]

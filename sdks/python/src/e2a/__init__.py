# Top-level convenience aliases — point to the current stable API version (v1).
#
# The pinned contract path is `e2a.v1`:
#   from e2a.v1 import E2AClient, AsyncE2AClient
#
# The top-level `e2a` package remains a convenience alias for that version.

from e2a.v1 import (
    E2AApi,
    AsyncE2AApi,
    E2AApiError,
    E2AClient,
    AsyncE2AClient,
    InboundEmail,
    AsyncInboundEmail,
    Attachment,
    AuthHeaders,
    MessageList,
    MessageSummary,
    SendResult,
    WSNotification,
    fetch_info,
    fetch_info_async,
)

__all__ = [
    "E2AApi",
    "AsyncE2AApi",
    "E2AApiError",
    "E2AClient",
    "AsyncE2AClient",
    "InboundEmail",
    "AsyncInboundEmail",
    "Attachment",
    "AuthHeaders",
    "MessageList",
    "MessageSummary",
    "SendResult",
    "WSNotification",
    "fetch_info",
    "fetch_info_async",
]

# Versioned v1 API surface — the stable, pinned contract path.
# Do not edit generated/ files directly; run `make generate` to regenerate.
from e2a.v1.generated import *  # noqa: F401,F403

from e2a.v1.api import E2AApi, E2AApiError, fetch_info  # noqa: F401
from e2a.v1.async_client import AsyncE2AApi, AsyncE2AClient  # noqa: F401
from e2a.v1.async_client import fetch_info as fetch_info_async  # noqa: F401
from e2a.v1.websocket import WSNotification  # noqa: F401
from e2a.v1.client import E2AClient  # noqa: F401
from e2a.v1.handler import (  # noqa: F401
    AsyncInboundEmail,
    Attachment,
    AuthHeaders,
    InboundEmail,
    MessageList,
    MessageSummary,
    SendResult,
)

__all__ = [
    # Raw API layers
    "E2AApi",
    "AsyncE2AApi",
    "E2AApiError",
    # High-level clients
    "E2AClient",
    "AsyncE2AClient",
    # Email types
    "InboundEmail",
    "AsyncInboundEmail",
    # Value types
    "Attachment",
    "AuthHeaders",
    "MessageList",
    "MessageSummary",
    "SendResult",
    # Discovery (mirror of TS E2AApi.fetchInfo)
    "fetch_info",
    "fetch_info_async",
    # WebSocket
    "WSNotification",
]

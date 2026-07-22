"""FastAPI host for authenticated e2a webhook deliveries."""

from __future__ import annotations

import os
from collections.abc import AsyncIterator, Callable
from contextlib import AsyncExitStack, asynccontextmanager
from typing import Any

from dotenv import load_dotenv
from e2a import AsyncE2AClient, E2AWebhookSignatureError
from fastapi import FastAPI, HTTPException, Request

from .agent import OpenAIReplyAgent
from .contracts import ReplyAgent
from .delivery_state import EventDeduper
from .handler import DeliveryInProgress, handle_delivery

MAX_WEBHOOK_BODY_BYTES = 1024 * 1024
AgentFactory = Callable[[], ReplyAgent]
ClientFactory = Callable[..., Any]
DeduperFactory = Callable[[], EventDeduper]


def _require_env(name: str, explicit: str | None = None) -> str:
    value = explicit or os.getenv(name)
    if not value:
        raise ValueError(f"{name} is required")
    return value


async def _read_limited_body(request: Request) -> bytes:
    """Read the exact request bytes while enforcing the limit on streamed input."""

    declared = request.headers.get("content-length")
    if declared is not None:
        try:
            if int(declared) > MAX_WEBHOOK_BODY_BYTES:
                raise HTTPException(status_code=413, detail="webhook body too large")
        except ValueError:
            pass

    body = bytearray()
    async for chunk in request.stream():
        if len(body) + len(chunk) > MAX_WEBHOOK_BODY_BYTES:
            raise HTTPException(status_code=413, detail="webhook body too large")
        body.extend(chunk)
    return bytes(body)


def create_app(
    *,
    api_key: str | None = None,
    webhook_secret: str | None = None,
    base_url: str | None = None,
    client_factory: ClientFactory = AsyncE2AClient,
    deduper_factory: DeduperFactory = EventDeduper,
    agent_factory: AgentFactory | None = None,
) -> FastAPI:
    """Create an app; environment and clients are resolved only at startup."""

    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        load_dotenv()
        resolved_key = _require_env("E2A_API_KEY", api_key)
        resolved_secret = _require_env("E2A_WEBHOOK_SECRET", webhook_secret)
        _require_env("OPENAI_API_KEY")
        async with AsyncExitStack() as stack:
            agent = (
                OpenAIReplyAgent.from_env()
                if agent_factory is None
                else agent_factory()
            )
            agent_close = getattr(agent, "aclose", None)
            if callable(agent_close):
                stack.push_async_callback(agent_close)
            client = client_factory(api_key=resolved_key, base_url=base_url)
            stack.push_async_callback(client.aclose)
            app.state.client = client
            app.state.webhook_secret = resolved_secret
            app.state.agent = agent
            app.state.deduper = deduper_factory()
            yield

    app = FastAPI(lifespan=lifespan)

    @app.get("/health")
    async def health() -> dict[str, str]:
        return {"status": "ok"}

    @app.post("/webhook")
    async def webhook(request: Request) -> dict[str, str]:
        body = await _read_limited_body(request)
        signature = request.headers.get("X-E2A-Signature", "")
        try:
            return await handle_delivery(
                body,
                signature,
                request.app.state.webhook_secret,
                request.app.state.client.inbound,
                request.app.state.agent,
                request.app.state.deduper,
            )
        except E2AWebhookSignatureError as error:
            raise HTTPException(status_code=401, detail="invalid signature") from error
        except DeliveryInProgress as error:
            raise HTTPException(status_code=503, detail="delivery in progress") from error

    return app


def run() -> None:
    """Run the example server using its factory, without import-time secrets."""

    import uvicorn

    uvicorn.run("agent_webhooks.app:create_app", factory=True, host="0.0.0.0", port=8000)


if __name__ == "__main__":
    run()

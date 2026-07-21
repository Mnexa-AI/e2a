"""FastAPI host for authenticated e2a webhook deliveries."""

from __future__ import annotations

import os
from collections.abc import AsyncIterator, Callable, Mapping
from contextlib import asynccontextmanager
from typing import Any

from dotenv import load_dotenv
from e2a import AsyncE2AClient, E2AWebhookSignatureError
from fastapi import FastAPI, HTTPException, Request

from .adapters import (
    ADKReplyAgent,
    AnthropicReplyAgent,
    FakeReplyAgent,
    LangChainReplyAgent,
    OpenAIReplyAgent,
)
from .contracts import ReplyAgent
from .delivery_state import EventDeduper
from .handler import DeliveryInProgress, handle_delivery

SUPPORTED_FRAMEWORKS = ("openai", "anthropic", "langchain", "adk", "fake")
AgentFactory = Callable[[], ReplyAgent]
ClientFactory = Callable[..., Any]
DeduperFactory = Callable[[], EventDeduper]


def _require_env(name: str, explicit: str | None = None) -> str:
    value = explicit or os.getenv(name)
    if not value:
        raise ValueError(f"{name} is required")
    return value


def _agent_factories() -> dict[str, AgentFactory]:
    return {
        "openai": OpenAIReplyAgent.from_env,
        "anthropic": AnthropicReplyAgent.from_env,
        "langchain": LangChainReplyAgent.from_env,
        "adk": ADKReplyAgent.from_env,
        "fake": FakeReplyAgent,
    }


def select_agent(
    framework: str,
    *,
    factories: Mapping[str, Callable[[], Any]] | None = None,
) -> Any:
    """Build the adapter for one exact, supported framework name."""

    available = factories or _agent_factories()
    if framework not in SUPPORTED_FRAMEWORKS:
        choices = ", ".join(SUPPORTED_FRAMEWORKS)
        raise ValueError(f"AGENT_FRAMEWORK must be one of: {choices}; got {framework!r}")
    return available[framework]()


def create_app(
    *,
    api_key: str | None = None,
    webhook_secret: str | None = None,
    base_url: str | None = None,
    framework: str | None = None,
    client_factory: ClientFactory = AsyncE2AClient,
    deduper_factory: DeduperFactory = EventDeduper,
) -> FastAPI:
    """Create an app; environment and clients are resolved only at startup."""

    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        load_dotenv()
        resolved_key = _require_env("E2A_API_KEY", api_key)
        resolved_secret = _require_env("E2A_WEBHOOK_SECRET", webhook_secret)
        selected = framework if framework is not None else os.getenv(
            "AGENT_FRAMEWORK", "fake"
        )
        agent = select_agent(selected)
        client = client_factory(api_key=resolved_key, base_url=base_url)
        app.state.client = client
        app.state.webhook_secret = resolved_secret
        app.state.agent = agent
        app.state.deduper = deduper_factory()
        try:
            yield
        finally:
            await client.aclose()

    app = FastAPI(lifespan=lifespan)

    @app.get("/health")
    async def health() -> dict[str, str]:
        return {"status": "ok"}

    @app.post("/webhook")
    async def webhook(request: Request) -> dict[str, str]:
        body = await request.body()
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

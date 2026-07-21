"""FastAPI host for authenticated e2a webhook deliveries."""

from __future__ import annotations

import os
from collections.abc import AsyncIterator, Callable, Mapping
from contextlib import AsyncExitStack, asynccontextmanager
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
MAX_WEBHOOK_BODY_BYTES = 1024 * 1024
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

    available = _agent_factories() if factories is None else factories
    if framework not in SUPPORTED_FRAMEWORKS:
        choices = ", ".join(SUPPORTED_FRAMEWORKS)
        raise ValueError(f"AGENT_FRAMEWORK must be one of: {choices}; got {framework!r}")
    return available[framework]()


def _validate_provider_config(framework: str) -> None:
    """Fail startup before allocating clients when provider config is incomplete."""

    if framework == "fake":
        return
    if framework == "openai":
        _require_env("OPENAI_API_KEY")
        return
    if framework == "anthropic":
        _require_env("ANTHROPIC_API_KEY")
        return
    if framework == "langchain":
        model = os.getenv("LANGCHAIN_MODEL", "openai:gpt-5.5")
        if not model.startswith("openai:"):
            raise ValueError(
                "LANGCHAIN_MODEL must use the installed openai: provider prefix"
            )
        _require_env("OPENAI_API_KEY")
        return
    if framework == "adk":
        if os.getenv("GEMINI_API_KEY") or os.getenv("GOOGLE_API_KEY"):
            return
        if os.getenv("GOOGLE_GENAI_USE_VERTEXAI", "").casefold() == "true":
            missing = [
                name
                for name in ("GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION")
                if not os.getenv(name)
            ]
            if not missing:
                return
            raise ValueError(
                "ADK Vertex mode requires " + " and ".join(missing)
            )
        raise ValueError(
            "ADK requires GEMINI_API_KEY or GOOGLE_API_KEY, or complete Vertex config"
        )


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
    framework: str | None = None,
    client_factory: ClientFactory = AsyncE2AClient,
    deduper_factory: DeduperFactory = EventDeduper,
    agent_factories: Mapping[str, Callable[[], Any]] | None = None,
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
        if selected not in SUPPORTED_FRAMEWORKS:
            select_agent(selected, factories=agent_factories)
        _validate_provider_config(selected)
        async with AsyncExitStack() as stack:
            agent = select_agent(selected, factories=agent_factories)
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

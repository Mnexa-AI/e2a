"""FastAPI webhook that turns e2a inbound emails into ADK agent turns.

Pipeline per request:

    POST /webhook (raw e2a payload)
        |
        v
    parse_webhook (parse + verify HMAC)  <-- rejects unsigned/replayed payloads
        |
        v
    map (sender, conversation_id) -> ADK (user_id, session_id)
        |                               first contact: conv_id is None;
        |                               we mint one and use it as session_id
        v
    Runner.run_async(...)        <-- multi-turn memory via ADK sessions
        |
        v
    email.reply(text, conversation_id=...)  <-- echoes our id back so the
                                                next inbound matches

The conversation_id <-> session_id binding is what lets ADK accumulate
context across email turns even though SMTP itself is stateless.
"""

from __future__ import annotations

import logging
import os
import uuid
from contextlib import asynccontextmanager
from typing import AsyncIterator

from dotenv import load_dotenv
from fastapi import FastAPI, HTTPException, Request, status
from google.adk.runners import Runner
from google.adk.sessions import InMemorySessionService
from google.genai import types

from e2a.v1 import E2AClient, InboundEmail

from agent import APP_NAME, agent

load_dotenv()
log = logging.getLogger("adk-webhook")
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")


def _require_env(name: str) -> str:
    val = os.environ.get(name)
    if not val:
        raise RuntimeError(
            f"Missing required env var {name}. "
            f"Copy .env.example to .env and fill it in."
        )
    return val


# Both E2A_API_KEY (for E2AClient) and E2A_HMAC_SECRET (for parse_webhook)
# are consumed implicitly by the SDK. We just assert presence here so a
# missing secret fails fast at startup rather than on the first request.
_require_env("E2A_API_KEY")
_require_env("E2A_HMAC_SECRET")
_require_env("GOOGLE_API_KEY")


@asynccontextmanager
async def lifespan(_: FastAPI) -> AsyncIterator[None]:
    # Lazy-init shared resources once per process.
    app.state.e2a = E2AClient()
    app.state.sessions = InMemorySessionService()
    app.state.runner = Runner(
        agent=agent, app_name=APP_NAME, session_service=app.state.sessions
    )
    yield


app = FastAPI(title="ADK + e2a webhook", lifespan=lifespan)


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/webhook")
async def webhook(request: Request) -> dict[str, str]:
    body = await request.body()
    # parse_webhook does parse + HMAC-verify in one call. Reads
    # E2A_HMAC_SECRET from the env, raises PermissionError on bad signature.
    # Anyone can reach a public webhook URL; this is what proves the payload
    # came from your e2a relay.
    try:
        email: InboundEmail = request.app.state.e2a.parse_webhook(body)
    except PermissionError:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="bad signature")

    # First contact has no conversation_id — mint one so this thread has an
    # ID we can echo back on every reply. The same id becomes the ADK
    # session_id, keying multi-turn memory.
    conversation_id = email.conversation_id or f"conv_{uuid.uuid4().hex[:12]}"

    # user_id scopes a session to a particular human counterpart. Different
    # senders get isolated session histories even on the same agent.
    #
    # Safety note: email.sender is only trustworthy *because* parse_webhook
    # verified the HMAC — that signature binds to the sender claim e2a
    # verified via SPF/DKIM. The SDK enforces this by raising
    # UnverifiedEmailError on field access until verification succeeds, so
    # accidentally swapping in client.parse(body) here would surface as a
    # runtime error rather than a silent session-poisoning vector.
    user_id = email.sender

    sessions = request.app.state.sessions
    session = await sessions.get_session(
        app_name=APP_NAME, user_id=user_id, session_id=conversation_id
    )
    if session is None:
        session = await sessions.create_session(
            app_name=APP_NAME, user_id=user_id, session_id=conversation_id
        )

    msg = types.Content(
        role="user",
        parts=[types.Part(text=_format_email_for_agent(email))],
    )

    runner: Runner = request.app.state.runner
    reply_text = ""
    async for event in runner.run_async(
        user_id=user_id, session_id=conversation_id, new_message=msg
    ):
        if event.is_final_response() and event.content and event.content.parts:
            reply_text = event.content.parts[0].text or ""

    if not reply_text:
        # Agent produced no response (rare — usually means a tool call without
        # a final text turn). Don't reply with an empty email; log so the
        # operator can investigate why the run finished without text.
        log.warning(
            "agent produced no reply text user=%s conv=%s msg=%s",
            user_id, conversation_id, email.message_id,
        )
        return {"status": "no_reply", "conversation_id": conversation_id}

    log.info(
        "replying user=%s conv=%s msg=%s reply_chars=%d",
        user_id, conversation_id, email.message_id, len(reply_text),
    )
    email.reply(reply_text, conversation_id=conversation_id)
    return {"status": "replied", "conversation_id": conversation_id}


def _format_email_for_agent(email: InboundEmail) -> str:
    """Flatten the email into a single text block for the agent.

    A more sophisticated agent could be given the headers as a separate
    structured tool call. For a tutorial, plain prose is enough.
    """
    return (
        f"From: {email.sender}\n"
        f"Subject: {email.subject}\n"
        f"\n"
        f"{email.text_body}"
    )

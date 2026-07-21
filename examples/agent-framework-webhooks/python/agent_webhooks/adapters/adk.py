"""Google Agent Development Kit reply adapter."""

import hashlib
import os
from collections.abc import AsyncIterable, Callable
from email.utils import parseaddr
from typing import Any

from e2a import AsyncInboundEmail

from agent_webhooks.prompt import REPLY_INSTRUCTIONS, email_prompt

APP_NAME = "e2a_email_assistant"
ADKRun = Callable[[AsyncInboundEmail, str, str], AsyncIterable[Any]]


def _user_id(email: AsyncInboundEmail) -> str:
    sender = parseaddr(email.from_ or "")[1].strip().casefold() or "missing-sender"
    inbox = email.inbox.strip().casefold() or "missing-inbox"
    namespace = f"{inbox}\0{sender}"
    digest = hashlib.sha256(namespace.encode("utf-8")).hexdigest()[:20]
    return f"sender-{digest}"


class ADKReplyAgent:
    def __init__(self, run: ADKRun) -> None:
        self._run = run

    @classmethod
    def from_env(cls) -> "ADKReplyAgent":
        from google.adk.agents import LlmAgent
        from google.adk.runners import Runner
        from google.adk.sessions import InMemorySessionService
        from google.genai import types

        agent = LlmAgent(
            name="email_assistant",
            model=os.getenv("ADK_MODEL", "gemini-flash-latest"),
            instruction=REPLY_INSTRUCTIONS,
        )
        sessions = InMemorySessionService()
        runner = Runner(agent=agent, app_name=APP_NAME, session_service=sessions)

        async def run(
            email: AsyncInboundEmail, prompt: str, conversation_id: str
        ) -> AsyncIterable[Any]:
            user_id = _user_id(email)
            session_id = conversation_id
            session = await sessions.get_session(
                app_name=APP_NAME,
                user_id=user_id,
                session_id=session_id,
            )
            if session is None:
                await sessions.create_session(
                    app_name=APP_NAME,
                    user_id=user_id,
                    session_id=session_id,
                )
            content = types.Content(
                role="user",
                parts=[types.Part(text=prompt)],
            )
            async for event in runner.run_async(
                user_id=user_id,
                session_id=session_id,
                new_message=content,
            ):
                yield event

        return cls(run)

    async def reply(
        self, email: AsyncInboundEmail, conversation_id: str
    ) -> str:
        final_text = ""
        async for event in self._run(email, email_prompt(email), conversation_id):
            if not event.is_final_response():
                continue
            content = getattr(event, "content", None)
            parts = getattr(content, "parts", []) if content is not None else []
            final_text = "\n".join(
                part.text
                for part in parts
                if isinstance(getattr(part, "text", None), str)
            )
        return final_text

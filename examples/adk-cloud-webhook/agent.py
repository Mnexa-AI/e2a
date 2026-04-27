"""ADK agent definition.

Kept deliberately minimal so the focus stays on the e2a integration in
webhook.py. Swap in your own instruction, tools, or sub-agents as needed.
"""

from google.adk.agents import LlmAgent

APP_NAME = "e2a-adk-demo"

agent = LlmAgent(
    name="email_assistant",
    model="gemini-flash-latest",
    instruction=(
        "You are a friendly assistant who replies to emails. "
        "Be concise — 1-3 short paragraphs. "
        "Don't include 'Subject:' or quoted-reply blocks; the email "
        "wrapper handles that. Just write the body."
    ),
)

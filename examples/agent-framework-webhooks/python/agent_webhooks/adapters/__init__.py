"""Thin, dependency-lazy adapters for supported agent frameworks."""

from .openai import OpenAIReplyAgent
from .adk import ADKReplyAgent
from .anthropic import AnthropicReplyAgent
from .fake import FakeReplyAgent
from .langchain import LangChainReplyAgent

__all__ = [
    "ADKReplyAgent",
    "AnthropicReplyAgent",
    "FakeReplyAgent",
    "LangChainReplyAgent",
    "OpenAIReplyAgent",
]

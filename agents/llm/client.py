"""Provider-agnostic LLM entry point.

The agent imports `stream_chat` / `create_message` from THIS module, not
from a provider-specific one. We pick the adapter from an explicit
`DHUNTER_LLM_PROVIDER` (anthropic | openai) or auto-detect from the base
URL. Both adapters emit the same Anthropic-shaped StreamEvent contract, so
the agent loop drives "most models on the market" unchanged.

Detection rules:
  * base URL contains "/anthropic"          -> anthropic protocol
  * base URL contains known OpenAI-compat
    hosts/words (openai, deepseek, qwen,
    dashscope, moonshot, zhipu, glm, oneapi) -> openai protocol
  * otherwise                               -> anthropic (safe default)
"""

from __future__ import annotations

import os
from typing import Any, AsyncIterator

from llm.anthropic_client import StreamEvent, create_message as _anthropic_message  # noqa: F401
from llm.anthropic_client import stream_chat as _anthropic_stream
from llm.openai_client import OpenAIAdapter

_OPENAI_HINTS = (
    "openai", "deepseek", "qwen", "dashscope", "moonshot",
    "zhipu", "glm", "oneapi", "aihubmix", "siliconflow", "ollama",
)


def _env(name: str, default: str) -> str:
    val = os.environ.get(name)
    return val if val not in (None, "") else default


def detect_provider(base_url: str | None) -> str:
    explicit = os.environ.get("DHUNTER_LLM_PROVIDER", "").strip().lower()
    if explicit in ("anthropic", "openai"):
        return explicit
    url = (base_url or _env("DHUNTER_LLM_BASE_URL", "")).lower()
    if "/anthropic" in url:
        return "anthropic"
    for hint in _OPENAI_HINTS:
        if hint in url:
            return "openai"
    return "anthropic"


async def stream_chat(
    system: str,
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None = None,
    *,
    api_key: str | None = None,
    base_url: str | None = None,
    model: str | None = None,
    max_tokens: int | None = None,
    provider: str | None = None,
    extra_body: dict[str, Any] | None = None,
) -> AsyncIterator[StreamEvent]:
    provider = provider or detect_provider(base_url)
    if provider == "openai":
        adapter = OpenAIAdapter(
            api_key=api_key if api_key is not None else _env("DHUNTER_LLM_KEY", ""),
            base_url=base_url if base_url is not None else _env("DHUNTER_LLM_BASE_URL", "https://api.openai.com/v1"),
            model=model if model is not None else _env("DHUNTER_LLM_MODEL", "gpt-4o-mini"),
            max_tokens=max_tokens if max_tokens is not None else int(_env("DHUNTER_LLM_MAX_TOKENS", "32768")),
        )
        async for ev in adapter.stream_chat(system, messages, tools, extra_body=extra_body):
            yield ev
        return
    # anthropic protocol
    async for ev in _anthropic_stream(
        system, messages, tools,
        api_key=api_key, base_url=base_url, model=model, max_tokens=max_tokens,
        extra_body=extra_body,
    ):
        yield ev


async def create_message(
    system: str,
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None = None,
    **kwargs: Any,
) -> dict[str, Any]:
    """Non-streaming single-shot (used by health checks). For OpenAI we
    fall back to the anthropic adapter only if tools is empty; tool-using
    non-streaming calls are rare and the streaming path is canonical."""
    provider = kwargs.pop("provider", None) or detect_provider(kwargs.get("base_url"))
    if provider == "openai" and not tools:
        # one-shot text: reuse anthropic adapter against OpenAI-compatible
        # message format is not possible, so we stream and join instead.
        chunks: list[str] = []
        async for ev in stream_chat(system, messages, tools, **kwargs):
            if ev.type == "content_block_delta":
                d = ev.data.get("delta") or {}
                if d.get("type") == "text_delta":
                    chunks.append(d.get("text", ""))
        return {"content": [{"type": "text", "text": "".join(chunks)}], "role": "assistant"}
    return await _anthropic_message(system, messages, tools, **kwargs)

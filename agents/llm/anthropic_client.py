"""Anthropic Messages API streaming client.

Speaks the Anthropic SSE protocol (`message_start` -> `content_block_*` ->
`message_delta` -> `message_stop`). The base URL is parameterised so the
Dhunter stack can route through a third-party Anthropic-compatible
provider (e.g. https://api.minimaxi.com/anthropic).

Configuration is read from the environment so the Go sidecar can inject
credentials without code changes:

    DHUNTER_LLM_KEY         API key
    DHUNTER_LLM_BASE_URL    default: https://api.minimaxi.com/anthropic
    DHUNTER_LLM_MODEL       default: claude-sonnet-4-5
    DHUNTER_LLM_MAX_TOKENS  default: 32768

Token usage accounting
----------------------
The Anthropic `message_delta` event carries a `usage` block with
`input_tokens` / `output_tokens` / `cache_creation_input_tokens` /
`cache_read_input_tokens`. We accumulate these per stream and expose
the running totals via the `usage` property on the iterator.
"""

from __future__ import annotations

import json
import logging
import os
from dataclasses import dataclass, field
from typing import Any, AsyncIterator

import httpx

log = logging.getLogger(__name__)

DEFAULT_BASE_URL = "https://api.minimaxi.com/anthropic"
DEFAULT_MODEL = "claude-sonnet-4-5"
DEFAULT_MAX_TOKENS = 32768
DEFAULT_VERSION = "2023-06-01"


@dataclass
class TokenUsage:
    """Cumulative token usage for one LLM stream.

    The Anthropic `message_delta` event reports `usage` only on the
    final delta (with `output_tokens`); `message_start` carries
    `input_tokens` / `cache_*`. We sum across the stream so callers
    can sample usage mid-flight.
    """
    input_tokens: int = 0
    output_tokens: int = 0
    cache_creation_input_tokens: int = 0
    cache_read_input_tokens: int = 0

    @property
    def total(self) -> int:
        return (
            self.input_tokens
            + self.output_tokens
            + self.cache_creation_input_tokens
            + self.cache_read_input_tokens
        )

    def add(self, other: "TokenUsage") -> None:
        self.input_tokens += other.input_tokens
        self.output_tokens += other.output_tokens
        self.cache_creation_input_tokens += other.cache_creation_input_tokens
        self.cache_read_input_tokens += other.cache_read_input_tokens

    def to_dict(self) -> dict[str, int]:
        return {
            "input_tokens": self.input_tokens,
            "output_tokens": self.output_tokens,
            "cache_creation_input_tokens": self.cache_creation_input_tokens,
            "cache_read_input_tokens": self.cache_read_input_tokens,
            "total": self.total,
        }


@dataclass
class StreamEvent:
    """One parsed SSE event from the Messages API.

    `type` is the SSE event name (e.g. `content_block_delta`).
    `data` is the parsed JSON payload, with at least a `type` key for
    content blocks.
    """

    type: str
    data: dict[str, Any] = field(default_factory=dict)


def _env(name: str, default: str) -> str:
    val = os.environ.get(name)
    return val if val not in (None, "") else default


async def stream_chat(
    system: str,
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None = None,
    *,
    api_key: str | None = None,
    base_url: str | None = None,
    model: str | None = None,
    max_tokens: int | None = None,
    extra_body: dict[str, Any] | None = None,
) -> AsyncIterator[StreamEvent]:
    """Stream a Messages API call and yield parsed events.

    Args:
        system: System prompt string.
        messages: Anthropic-format messages list (already excludes system).
        tools: Anthropic-format tool definitions (with `input_schema`).
        api_key / base_url / model / max_tokens: overrides; default to env.
        extra_body: Merged into the request body (e.g. `thinking` config).
    """
    api_key = api_key if api_key is not None else _env("DHUNTER_LLM_KEY", "")
    base_url = (base_url if base_url is not None else _env("DHUNTER_LLM_BASE_URL", DEFAULT_BASE_URL)).rstrip("/")
    model = model if model is not None else _env("DHUNTER_LLM_MODEL", DEFAULT_MODEL)
    max_tokens = max_tokens if max_tokens is not None else int(_env("DHUNTER_LLM_MAX_TOKENS", str(DEFAULT_MAX_TOKENS)))

    payload: dict[str, Any] = {
        "model": model,
        "max_tokens": max_tokens,
        "system": system,
        "messages": messages,
        "stream": True,
    }
    if tools:
        payload["tools"] = tools
    if extra_body:
        payload.update(extra_body)

    headers = {
        "x-api-key": api_key,
        # Many Anthropic-compatible gateways (DeepSeek, one-api proxies)
        # accept Authorization instead of / in addition to x-api-key.
        # Sending both keeps real Anthropic + compat layers working.
        "authorization": f"Bearer {api_key}",
        "anthropic-version": _env("DHUNTER_LLM_VERSION", DEFAULT_VERSION),
        "content-type": "application/json",
        "accept": "text/event-stream",
    }

    url = f"{base_url}/v1/messages"
    log.debug("LLM request: POST %s model=%s msgs=%d tools=%d", url, model, len(messages), len(tools or []))

    timeout = httpx.Timeout(connect=10.0, read=120.0, write=10.0, pool=10.0)
    async with httpx.AsyncClient(trust_env=False, timeout=timeout) as client:
        async with client.stream("POST", url, json=payload, headers=headers) as resp:
            if resp.status_code >= 400:
                # Drain body for the error message, then raise
                body = await resp.aread()
                raise httpx.HTTPStatusError(
                    f"LLM {resp.status_code}: {body.decode('utf-8', errors='replace')[:500]}",
                    request=resp.request,
                    response=resp,
                )
            # Track cumulative token usage; emit a synthetic "usage" event
            # at the end of the stream so the agent can persist it.
            usage = TokenUsage()
            async for ev in _iter_sse(resp):
                # Capture usage from message_start / message_delta.
                usage_data = ev.data.get("usage") if isinstance(ev.data, dict) else None
                if usage_data and isinstance(usage_data, dict):
                    inc = TokenUsage(
                        input_tokens=int(usage_data.get("input_tokens", 0) or 0),
                        output_tokens=int(usage_data.get("output_tokens", 0) or 0),
                        cache_creation_input_tokens=int(usage_data.get("cache_creation_input_tokens", 0) or 0),
                        cache_read_input_tokens=int(usage_data.get("cache_read_input_tokens", 0) or 0),
                    )
                    usage.add(inc)
                yield ev
            # Final usage event.
            yield StreamEvent(type="usage", data=usage.to_dict())


async def _iter_sse(resp: httpx.Response) -> AsyncIterator[StreamEvent]:
    """Parse an httpx SSE response into StreamEvent objects.

    Tolerant to:
      - blank lines as dispatch separators
      - `event:` and `data:` lines in either order
      - commented lines starting with `:`
      - data split across multiple lines (joined by newline)
    """
    event_type: str | None = None
    data_buf: list[str] = []
    async for raw in resp.aiter_lines():
        line = raw  # aiter_lines already strips trailing newline
        if line == "":
            if data_buf:
                joined = "\n".join(data_buf)
                data_buf = []
                try:
                    data = json.loads(joined)
                except json.JSONDecodeError:
                    data = {"_raw": joined}
                yield StreamEvent(type=event_type or "message", data=data)
            event_type = None
            continue
        if line.startswith(":"):
            # SSE comment / keepalive
            continue
        if line.startswith("event:"):
            event_type = line[len("event:"):].strip()
            continue
        if line.startswith("data:"):
            payload = line[len("data:"):]
            # The space after `data:` is optional but conventional; strip one.
            if payload.startswith(" "):
                payload = payload[1:]
            data_buf.append(payload)


# --- Synchronous (non-streaming) helper --------------------------------


async def create_message(
    system: str,
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None = None,
    **kwargs: Any,
) -> dict[str, Any]:
    """Non-streaming single-shot call. Used by tests / health checks."""
    api_key = kwargs.pop("api_key", None) or _env("DHUNTER_LLM_KEY", "")
    base_url = (kwargs.pop("base_url", None) or _env("DHUNTER_LLM_BASE_URL", DEFAULT_BASE_URL)).rstrip("/")
    model = kwargs.pop("model", None) or _env("DHUNTER_LLM_MODEL", DEFAULT_MODEL)
    max_tokens = kwargs.pop("max_tokens", None) or int(_env("DHUNTER_LLM_MAX_TOKENS", str(DEFAULT_MAX_TOKENS)))

    payload: dict[str, Any] = {
        "model": model,
        "max_tokens": max_tokens,
        "system": system,
        "messages": messages,
    }
    if tools:
        payload["tools"] = tools
    if kwargs:
        payload.update(kwargs)

    headers = {
        "x-api-key": api_key,
        "authorization": f"Bearer {api_key}",
        "anthropic-version": _env("DHUNTER_LLM_VERSION", DEFAULT_VERSION),
        "content-type": "application/json",
        "accept": "application/json",
    }

    url = f"{base_url}/v1/messages"
    timeout = httpx.Timeout(connect=10.0, read=120.0, write=10.0, pool=10.0)
    async with httpx.AsyncClient(trust_env=False, timeout=timeout) as client:
        resp = await client.post(url, json=payload, headers=headers)
        resp.raise_for_status()
        return resp.json()

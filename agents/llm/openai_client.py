"""OpenAI-compatible streaming client, normalised to the agent's event shape.

Speaks the OpenAI Chat Completions SSE protocol (the one DeepSeek, Qwen,
Moonshot, Zhipu, GLM, one-api proxies etc. all speak) and re-emits every
event as the SAME Anthropic-shaped StreamEvent the agent loop already
consumes. This is what lets one engine drive "most models on the market":

  * text            -> content_block_start(text) + text_delta
  * reasoning       -> thinking_delta (from `reasoning_content`, used by
                       DeepSeek / Qwen / GLM reasoning models)
  * tool calls      -> accumulated by index, emitted as tool_use blocks
                       with input_json_delta, then message_delta(stop)
  * usage           -> a trailing `usage` event (token accounting)

Env:
    DHUNTER_LLM_KEY
    DHUNTER_LLM_BASE_URL      e.g. https://api.openai.com/v1 or a proxy
    DHUNTER_LLM_MODEL
    DHUNTER_LLM_MAX_TOKENS
    DHUNTER_LLM_OPENAI_AUTH   "bearer" (default) | "apikey"
"""

from __future__ import annotations

import json
import logging
import os
from typing import Any, AsyncIterator

import httpx

log = logging.getLogger(__name__)

DEFAULT_MAX_TOKENS = 32768


def _env(name: str, default: str) -> str:
    val = os.environ.get(name)
    return val if val not in (None, "") else default


def _int(v: Any) -> int:
    try:
        return int(v or 0)
    except (TypeError, ValueError):
        return 0


def _normalize_openai_usage(usage: dict[str, Any]) -> dict[str, int]:
    """Normalize OpenAI-compatible usage into the canonical token buckets.

    Handles the cache-token shapes different backends report (ported from
    Hermes' normalize_usage):
      * OpenAI / Qwen / GLM   -> nested `prompt_tokens_details.cached_tokens`
      * DeepSeek native API   -> top-level `prompt_cache_hit_tokens` /
                                 `prompt_cache_miss_tokens`
      * Anthropic-style via a
        proxy (OpenRouter etc.) -> top-level `cache_read_input_tokens` /
                                    `cache_creation_input_tokens`
    OpenAI-style `prompt_tokens` INCLUDES cached tokens, so plain input is
    derived by subtracting them out.
    """
    details = usage.get("prompt_tokens_details") or {}
    prompt_total = _int(usage.get("prompt_tokens"))
    output_total = _int(usage.get("completion_tokens"))

    cache_read = _int(details.get("cached_tokens"))
    if not cache_read:
        cache_read = _int(usage.get("cache_read_input_tokens"))
    if not cache_read:
        cache_read = _int(usage.get("prompt_cache_hit_tokens"))

    cache_write = _int(details.get("cache_write_tokens"))
    if not cache_write:
        cache_write = _int(usage.get("cache_creation_input_tokens"))
    if not cache_write:
        # DeepSeek native: miss tokens are the complementary cache write bucket.
        cache_write = _int(usage.get("prompt_cache_miss_tokens"))

    input_tokens = max(0, prompt_total - cache_read - cache_write)

    # Reasoning / hidden-thinking tokens — dominates output on reasoning
    # models (deepseek-v4-flash, etc.) and is invisible in completion_tokens.
    out_details = usage.get("completion_tokens_details") or {}
    reasoning = _int(out_details.get("reasoning_tokens"))
    if not reasoning:
        reasoning = _int(usage.get("reasoning_tokens"))

    return {
        "input_tokens": input_tokens,
        "output_tokens": output_total,
        "cache_creation_input_tokens": cache_write,
        "cache_read_input_tokens": cache_read,
        "reasoning_tokens": reasoning,
        "total": _int(usage.get("total_tokens")) or (input_tokens + output_total),
    }


# Reuse the StreamEvent type from the anthropic client so the agent's
# event loop doesn't care which protocol produced the stream.
from llm.anthropic_client import StreamEvent  # noqa: E402


# Tool def conversion: Anthropic {name, description, input_schema} ->
# OpenAI {type: function, function: {name, description, parameters}}.
def _to_openai_tools(tools: list[dict[str, Any]]) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for t in tools:
        schema = t.get("input_schema") or t.get("inputSchema") or {"type": "object", "properties": {}}
        out.append({
            "type": "function",
            "function": {
                "name": t.get("name") or "",
                "description": t.get("description") or "",
                "parameters": schema,
            },
        })
    return out


def _to_openai_messages(system: str, messages: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Convert the agent's Anthropic-format message list to OpenAI format.

    Handles:
      - tool_use content blocks -> assistant `tool_calls`
      - tool_result blocks      -> the `tool` role messages
      - plain text              -> assistant/user content strings
    """
    out: list[dict[str, Any]] = []
    if system:
        out.append({"role": "system", "content": system})

    for msg in messages:
        role = msg.get("role", "user")
        content = msg.get("content")

        # Tool results: each block becomes a `tool` message.
        if role == "user" and isinstance(content, list):
            tool_msgs = []
            text_parts = []
            for block in content:
                if isinstance(block, dict) and block.get("type") == "tool_result":
                    tid = block.get("tool_use_id") or ""
                    body = block.get("content")
                    if not isinstance(body, str):
                        body = json.dumps(body, ensure_ascii=False) if body is not None else ""
                    tool_msgs.append({"role": "tool", "tool_call_id": tid, "content": body})
                elif isinstance(block, dict):
                    text_parts.append(str(block.get("text", "")))
            if text_parts:
                out.append({"role": "user", "content": "\n".join(text_parts)})
            out.extend(tool_msgs)
            continue

        # Assistant: text + tool_calls.
        if role == "assistant" and isinstance(content, list):
            text_parts = []
            tool_calls = []
            for block in content:
                if not isinstance(block, dict):
                    continue
                btype = block.get("type")
                if btype == "text":
                    text_parts.append(str(block.get("text", "")))
                elif btype == "tool_use":
                    args = block.get("input") or {}
                    tool_calls.append({
                        "id": block.get("id") or "",
                        "type": "function",
                        "function": {
                            "name": block.get("name") or "",
                            "arguments": json.dumps(args, ensure_ascii=False),
                        },
                    })
            om: dict[str, Any] = {"role": "assistant"}
            if text_parts:
                om["content"] = "\n".join(text_parts)
            else:
                om["content"] = None
            if tool_calls:
                om["tool_calls"] = tool_calls
            out.append(om)
            continue

        if isinstance(content, list):
            content = "\n".join(str(b.get("text", "")) for b in content if isinstance(b, dict))
        out.append({"role": role, "content": content if content is not None else ""})

    return out


def _stop_reason(finish: str | None) -> str | None:
    if finish == "tool_calls":
        return "tool_use"
    if finish == "length":
        return "max_tokens"
    if finish == "stop":
        return "end_turn"
    return finish


class OpenAIAdapter:
    def __init__(self, api_key: str, base_url: str, model: str, max_tokens: int):
        self.api_key = api_key
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.max_tokens = max_tokens
        self.auth = _env("DHUNTER_LLM_OPENAI_AUTH", "bearer")

    def _headers(self) -> dict[str, str]:
        h = {
            "content-type": "application/json",
            "accept": "text/event-stream",
        }
        if self.auth == "apikey":
            h["x-api-key"] = self.api_key
        else:
            h["authorization"] = f"Bearer {self.api_key}"
        return h

    async def stream_chat(
        self,
        system: str,
        messages: list[dict[str, Any]],
        tools: list[dict[str, Any]] | None = None,
        *,
        extra_body: dict[str, Any] | None = None,
    ) -> AsyncIterator[StreamEvent]:
        payload: dict[str, Any] = {
            "model": self.model,
            "max_tokens": self.max_tokens,
            "messages": _to_openai_messages(system, messages),
            "stream": True,
            "stream_options": {"include_usage": True},
        }
        if tools:
            payload["tools"] = _to_openai_tools(tools)
        if extra_body:
            payload.update(extra_body)

        url = f"{self.base_url}/chat/completions"
        timeout = httpx.Timeout(connect=10.0, read=120.0, write=10.0, pool=10.0)
        async with httpx.AsyncClient(trust_env=False, timeout=timeout) as client:
            async with client.stream("POST", url, json=payload, headers=self._headers()) as resp:
                if resp.status_code >= 400:
                    body = await resp.aread()
                    raise httpx.HTTPStatusError(
                        f"LLM {resp.status_code}: {body.decode('utf-8', errors='replace')[:500]}",
                        request=resp.request, response=resp,
                    )

                text_started = False
                thinking_started = False
                tool_calls: dict[int, dict[str, Any]] = {}
                finish_reason: str | None = None
                usage: dict[str, int] = {}

                async for raw in resp.aiter_lines():
                    line = raw.strip()
                    if not line.startswith("data:"):
                        continue
                    data = line[len("data:"):].strip()
                    if data == "[DONE]":
                        break
                    try:
                        chunk = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    if chunk.get("usage"):
                        usage = chunk["usage"]
                    choices = chunk.get("choices") or []
                    if not choices:
                        continue
                    choice = choices[0]
                    delta = choice.get("delta") or {}
                    finish = choice.get("finish_reason")
                    if finish:
                        finish_reason = finish

                    # reasoning content (DeepSeek / Qwen / GLM)
                    if delta.get("reasoning_content"):
                        if not thinking_started:
                            thinking_started = True
                            yield StreamEvent(type="content_block_start", data={"type": "content_block_start", "index": 0, "content_block": {"type": "thinking", "thinking": "", "signature": ""}})
                        yield StreamEvent(type="content_block_delta", data={"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": delta["reasoning_content"]}})

                    # content
                    text = delta.get("content")
                    if text:
                        if not text_started:
                            text_started = True
                            yield StreamEvent(type="content_block_start", data={"type": "content_block_start", "index": 1, "content_block": {"type": "text", "text": ""}})
                        yield StreamEvent(type="content_block_delta", data={"type": "content_block_delta", "index": 1, "delta": {"type": "text_delta", "text": text}})

                    # tool call deltas (accumulate by index)
                    for tc in delta.get("tool_calls") or []:
                        idx = tc.get("index", 0)
                        slot = tool_calls.setdefault(idx, {"id": "", "name": "", "arguments": ""})
                        if tc.get("id"):
                            slot["id"] = tc["id"]
                        fn = tc.get("function") or {}
                        if fn.get("name"):
                            slot["name"] = fn["name"]
                        if fn.get("arguments"):
                            slot["arguments"] += fn["arguments"]

                # emit assembled tool_use blocks
                if tool_calls:
                    for idx in sorted(tool_calls):
                        tc = tool_calls[idx]
                        yield StreamEvent(type="content_block_start", data={"type": "content_block_start", "index": idx + 2, "content_block": {"id": tc["id"], "type": "tool_use", "name": tc["name"], "input": {}}})
                        args = tc["arguments"]
                        mid = max(1, len(args) // 2)
                        if args:
                            yield StreamEvent(type="content_block_delta", data={"type": "content_block_delta", "index": idx + 2, "delta": {"type": "input_json_delta", "partial_json": args[:mid]}})
                            yield StreamEvent(type="content_block_delta", data={"type": "content_block_delta", "index": idx + 2, "delta": {"type": "input_json_delta", "partial_json": args[mid:]}})
                        yield StreamEvent(type="content_block_stop", data={"type": "content_block_stop", "index": idx + 2})

                if thinking_started:
                    yield StreamEvent(type="content_block_stop", data={"type": "content_block_stop", "index": 0})
                if text_started:
                    yield StreamEvent(type="content_block_stop", data={"type": "content_block_stop", "index": 1})

                yield StreamEvent(type="message_delta", data={"type": "message_delta", "delta": {"stop_reason": _stop_reason(finish_reason), "stop_sequence": None}})
                yield StreamEvent(type="message_stop", data={"type": "message_stop"})
                norm = _normalize_openai_usage(usage)
                yield StreamEvent(type="usage", data=norm)

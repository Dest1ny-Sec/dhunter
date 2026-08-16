"""Prompt-cache contract tests: the system block must be sent as an
Anthropic-style content array with cache_control so native Anthropic
endpoints can reuse it across turns and parallel workers (DeepSeek and
other compat gateways ignore the field — automatic prefix caching)."""

from __future__ import annotations

import asyncio
import json

import httpx
import pytest

from llm import anthropic_client as ac

SSE_END = b'event: message_delta\ndata: {"delta":{"stop_reason":"end_turn"}}\n\n'


def _capture_body(monkeypatch) -> list[dict]:
    captured: list[dict] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured.append(json.loads(request.content))
        return httpx.Response(200, headers={"content-type": "text/event-stream"}, content=SSE_END)

    transport = httpx.MockTransport(handler)
    original = httpx.AsyncClient  # capture BEFORE patching (avoid recursion)

    def fake_client(*args, **kwargs):
        kwargs["transport"] = transport
        return original(*args, **kwargs)

    monkeypatch.setattr(ac.httpx, "AsyncClient", fake_client)
    return captured


async def _stream(system: str = "sys prompt"):
    async for _ in ac.stream_chat(
        system=system,
        messages=[{"role": "user", "content": "hi"}],
        api_key="k", base_url="http://llm.test", model="m",
    ):
        pass


def test_system_sent_with_cache_control(monkeypatch):
    captured = _capture_body(monkeypatch)
    asyncio.run(_stream())
    body = captured[0]
    assert body["system"] == [
        {"type": "text", "text": "sys prompt", "cache_control": {"type": "ephemeral"}},
    ], body["system"]


def test_cache_control_disabled_by_env(monkeypatch):
    monkeypatch.setenv("DHUNTER_LLM_CACHE_SYSTEM", "0")
    captured = _capture_body(monkeypatch)
    asyncio.run(_stream())
    # Plain string form for gateways that reject array-form system.
    assert captured[0]["system"] == "sys prompt"


def test_empty_system_stays_string(monkeypatch):
    captured = _capture_body(monkeypatch)
    asyncio.run(_stream(system=""))
    assert captured[0]["system"] == ""

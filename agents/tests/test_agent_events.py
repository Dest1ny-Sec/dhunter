"""Agent event-stream contract tests: tool_call / tool_result must carry the
same call_id (the LLM's tool_use id) so the frontend can pair them into one
invocation, and the streamed events must be classifyable by `type`."""

from __future__ import annotations

import asyncio
from typing import Any

import core.agent as agent_mod
from core.agent import AgentRun, run_tool_loop
from llm.anthropic_client import StreamEvent


class FakeRegistry:
    """Minimal ToolRegistry stand-in: exposes all_tools() and call()."""

    def __init__(self, result: str = "HTTP 200 ok"):
        self.result = result
        self.calls: list[tuple[str, dict[str, Any]]] = []

    def all_tools(self) -> list[dict[str, Any]]:
        return [{"name": "http_request", "description": "x", "input_schema": {"type": "object"}}]

    async def call(self, name: str, arguments: dict[str, Any] | None = None, **kwargs) -> dict[str, Any]:
        self.calls.append((name, arguments or {}))
        return {"content": self.result, "is_error": False}


class ToolCallingLLM:
    """stream_chat that asks for one tool use (with a call_id), then — on
    the second turn, after the tool_result — produces a final text."""

    def __init__(self, call_id: str = "call_abc123"):
        self.call_id = call_id
        self.turns = 0

    async def stream_chat(self, system, messages, tools=None, **kwargs):
        self.turns += 1
        if self.turns == 1:
            yield StreamEvent(type="content_block_start", data={"content_block": {
                "type": "tool_use", "id": self.call_id, "name": "http_request", "input": {"url": "https://x"}}})
            yield StreamEvent(type="content_block_stop", data={})
            yield StreamEvent(type="message_delta", data={"delta": {"stop_reason": "tool_use"}})
            yield StreamEvent(type="usage", data={"input_tokens": 5, "output_tokens": 1,
                                                 "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0})
        else:
            yield StreamEvent(type="content_block_delta", data={"delta": {"type": "text_delta", "text": "found nothing"}})
            yield StreamEvent(type="content_block_stop", data={})
            yield StreamEvent(type="message_delta", data={"delta": {"stop_reason": "end_turn"}})
            yield StreamEvent(type="usage", data={"input_tokens": 5, "output_tokens": 1,
                                                 "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0})


async def drain(queue: asyncio.Queue) -> list[dict[str, Any]]:
    out = []
    while not queue.empty():
        out.append(await queue.get())
    return out


def test_tool_call_and_result_share_call_id(monkeypatch):
    queue: asyncio.Queue = asyncio.Queue()
    run = AgentRun(run_id="r-callid", target="https://example.com", objective="x", queue=queue)
    llm = ToolCallingLLM(call_id="call_abc123")
    monkeypatch.setattr(agent_mod, "stream_chat", llm.stream_chat)
    reg = FakeRegistry()

    asyncio.run(run_tool_loop(run, reg, system="sys", user_content="go"))

    events = asyncio.run(drain(queue))
    call = next(e for e in events if e["event"] == "tool_call")
    result = next(e for e in events if e["event"] == "tool_result")
    assert call["data"]["call_id"] == "call_abc123", call
    assert result["data"]["call_id"] == "call_abc123", result
    # The tool was actually executed (input args are empty here because the
    # mock omits input_json_delta deltas; the call itself is what matters).
    assert reg.calls and reg.calls[0][0] == "http_request", reg.calls

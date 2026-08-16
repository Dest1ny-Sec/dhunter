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


def test_compact_messages_collapses_old_rounds():
    """Beyond the round threshold, the oldest turns collapse into a compact
    summary merged into the initial user message; roles stay alternating and
    the LATEST turn is preserved fully."""
    from core.agent import _compact_messages

    msgs = [{"role": "user", "content": "start"}]
    for i in range(5):
        msgs.append({"role": "assistant", "content": [
            {"type": "tool_use", "id": f"call_{i}", "name": "http_request", "input": {"url": f"https://x/{i}"}},
        ]})
        msgs.append({"role": "user", "content": [
            {"type": "tool_result", "tool_use_id": f"call_{i}", "content": f"resp {i}", "is_error": False},
        ]})

    out = _compact_messages(msgs, max_rounds=2, keep_rounds=1)
    # initial user (with merged summary) + 1 kept round = 3 entries
    assert len(out) == 3, [m["role"] for m in out]
    assert "已压缩" in out[0]["content"]
    assert "http_request" in out[0]["content"]  # summary mentions the tool
    assert "→ ok" in out[0]["content"]
    # roles must stay user/assistant alternating (Anthropic constraint)
    assert [m["role"] for m in out] == ["user", "assistant", "user"], [m["role"] for m in out]
    # the kept round is the LATEST one, intact
    kept_result = out[2]["content"][0]
    assert kept_result["content"] == "resp 4", kept_result


def test_compact_messages_noop_below_threshold():
    """Ordinary runs (under the threshold) are untouched — prompt cache intact."""
    from core.agent import _compact_messages

    msgs = [{"role": "user", "content": "start"}]
    for i in range(2):
        msgs.append({"role": "assistant", "content": [{"type": "text", "text": f"t{i}"}]})
        msgs.append({"role": "user", "content": [{"type": "tool_result", "tool_use_id": f"c{i}", "content": f"r{i}", "is_error": False}]})
    out = _compact_messages(msgs, max_rounds=5, keep_rounds=1)
    assert out == msgs


def test_render_graph_summary_incremental_facts():
    """The planner sees only NEW facts once known_fact_ids is provided —
    known facts stop being re-paid every reason turn."""
    from core.agent import render_graph_summary

    graph = {
        "facts": [{"id": "f1", "description": "known endpoint"}, {"id": "f2", "description": "fresh finding"}],
        "intents": [],
        "hints": [],
    }
    full = render_graph_summary(graph)
    assert "2)" in full  # total count shown without a known set

    inc = render_graph_summary(graph, known_fact_ids={"f1"})
    assert "1 new since last planning" in inc, inc
    assert "f1" not in inc  # known fact omitted
    assert "f2" in inc  # new fact included


def test_cancelled_worker_releases_intent(monkeypatch):
    """Pause/cancel must hand a claimed intent BACK to the board, so a later
    resume can re-claim it — otherwise the direction is silently dropped."""
    import pytest
    import core.agent as agent_mod
    from core.worker import run_explore_worker
    from fakes import FakeBoard

    async def scenario():
        board = FakeBoard()
        await board.create_intent("r-cancel", [], "deep SSRF on openfile.jsp", creator="reason")
        intent = board.intents[0]

        # LLM that hangs forever → the worker stays in its tool loop.
        # (async generator: `async for` over stream_chat must be iterable)
        async def hang(system, messages, tools=None, **kwargs):
            await asyncio.Event().wait()
            yield  # pragma: no cover

        monkeypatch.setattr(agent_mod, "stream_chat", hang)
        run = AgentRun(run_id="r-cancel", target="https://x", objective="x", queue=asyncio.Queue())
        task = asyncio.create_task(run_explore_worker(run, board, FakeRegistry(), "sys", intent, "w1"))
        try:
            # Give the worker a moment to claim the intent.
            for _ in range(50):
                if board.intents[0]["status"] == "claimed":
                    break
                await asyncio.sleep(0.01)
            assert board.intents[0]["status"] == "claimed", board.intents[0]
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task
            # The intent must be open again (resumable), not stuck claimed.
            assert board.intents[0]["status"] == "open", board.intents[0]
            assert board.intents[0]["worker"] is None
        finally:
            if not task.done():
                task.cancel()

    asyncio.run(scenario())

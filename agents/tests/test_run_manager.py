"""End-to-end dispatcher tests with a scripted LLM and an in-memory board.

These prove the cairn-style loop works without touching a backend or a
real model: seed origin/goal -> reason proposes intents -> worker claims
and concludes -> dedup -> reason noop -> converged.
"""

from __future__ import annotations

import asyncio

import core.agent as agent_mod
from core.agent import AgentRun
from core.board import BoardClient  # noqa: F401  (interface reference)
from core.run_manager import RunManager
from llm.anthropic_client import StreamEvent
from tools.registry import ToolRegistry
from fakes import FakeBoard, ScriptedLLM


def _make_run(run_id: str = "test-1", target: str = "https://example.com") -> AgentRun:
    return AgentRun(run_id=run_id, target=target, objective="find exploitable vulns", queue=asyncio.Queue())


def _patch_llm(reason_resp: str, explore_resp: str):
    llm = ScriptedLLM(reason_resp, explore_resp)
    agent_mod.stream_chat = llm.stream_chat  # patch module-global used by callers


def test_run_manager_converges_after_explore():
    _patch_llm(
        reason_resp='{"kind": "intents", "intents": [{"from": [], "description": "probe /login for sqli"}]}',
        explore_resp="Confirmed /api/admin returns the app config without authentication.",
    )

    async def scenario():
        run = _make_run()
        board = FakeBoard()
        mgr = RunManager(run, board, ToolRegistry(), system_prompt="sys")
        await mgr.execute()
        return run, board

    run, board = asyncio.run(scenario())

    assert run.status == "success"
    # origin + goal + one concluded fact
    sources = {f["source"] for f in board.facts}
    assert "origin" in sources and "goal" in sources
    assert len(board.facts) >= 3

    intents = board.intents
    assert len(intents) == 1
    assert intents[0]["status"] == "concluded"
    assert intents[0]["description"] == "probe /login for sqli"
    assert run.finished_at


def test_dead_end_fails_intent_and_converges():
    _patch_llm(
        reason_resp='{"kind": "intents", "intents": [{"from": [], "description": "fuzz the search endpoint"}]}',
        explore_resp="Probed the endpoint; no vulnerability found, dead end.",
    )

    async def scenario():
        run = _make_run()
        board = FakeBoard()
        mgr = RunManager(run, board, ToolRegistry(), system_prompt="sys")
        await mgr.execute()
        return run, board

    run, board = asyncio.run(scenario())

    assert run.status == "success"
    intents = board.intents
    assert len(intents) == 1
    assert intents[0]["status"] == "failed"
    # no conclusion fact was added beyond origin/goal
    assert len(board.facts) == 2


def test_reason_complete_converges():
    _patch_llm(
        reason_resp='{"kind": "complete", "summary": "Found 2 critical vulns; objective met."}',
        explore_resp="unused",
    )

    async def scenario():
        run = _make_run()
        board = FakeBoard()
        mgr = RunManager(run, board, ToolRegistry(), system_prompt="sys")
        await mgr.execute()
        return run, board

    run, board = asyncio.run(scenario())

    assert run.status == "success"
    assert "objective met" in run.summary
    assert board.intents == []


def test_cancel_run_sets_cancelled_status():
    """Cancelling the manager task must cancel workers and end the run
    with status=cancelled (and a run_done event)."""

    class SlowLLM(ScriptedLLM):
        async def stream_chat(self, system, messages, tools=None, **kwargs):
            first = ""
            if messages:
                c = messages[0].get("content")
                first = c if isinstance(c, str) else ""
            text = self.explore_response if "Current intent" in first else self.reason_response
            await asyncio.sleep(0.05)  # give the dispatcher time to see the worker
            yield StreamEvent(type="content_block_delta",
                              data={"delta": {"type": "text_delta", "text": text}})
            yield StreamEvent(type="usage",
                              data={"input_tokens": 1, "output_tokens": 1,
                                    "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0})

    llm = SlowLLM(
        reason_response='{"kind": "intents", "intents": [{"from": [], "description": "long task"}]}',
        explore_response="Still exploring, will take a while.",
    )
    agent_mod.stream_chat = llm.stream_chat

    async def scenario():
        run = _make_run(run_id="test-cancel")
        board = FakeBoard()
        mgr = RunManager(run, board, ToolRegistry(), system_prompt="sys")
        task = asyncio.create_task(mgr.execute())
        await asyncio.sleep(0.15)
        task.cancel()
        try:
            await task
        except asyncio.CancelledError:
            pass
        return run

    run = asyncio.run(scenario())
    assert run.status == "cancelled"


def test_parallel_workers_claim_distinct_intents():
    """Two open intents must be claimed by different workers (CAS)."""
    _patch_llm(
        reason_resp='{"kind": "intents", "intents": ['
                   '{"from": [], "description": "check /login"},'
                   '{"from": [], "description": "check /register"}]}',
        explore_resp="Explored the endpoint and confirmed the behavior.",
    )

    async def scenario():
        run = _make_run(run_id="test-parallel")
        board = FakeBoard()
        mgr = RunManager(run, board, ToolRegistry(), system_prompt="sys")
        await mgr.execute()
        return run, board

    run, board = asyncio.run(scenario())

    assert run.status == "success"
    concluded = [i for i in board.intents if i["status"] == "concluded"]
    assert len(concluded) == 2
    workers = {i["worker"] for i in concluded}
    assert len(workers) == 2, f"expected 2 distinct workers, got {workers}"

"""SRC acceptance gate tests: config noise is dismissed, real exploits
survive, and severity is calibrated."""

from __future__ import annotations

import asyncio

import core.agent as agent_mod
from core.agent import AgentRun
from core.verifier import run_verifier
from fakes import FakeBoard
from llm.anthropic_client import StreamEvent


def _run(run_id: str = "test-vrf") -> AgentRun:
    return AgentRun(run_id=run_id, target="https://example.com", objective="find vulns", queue=asyncio.Queue())


def _patch_verify_llm(confirm: str):
    async def stream_chat(system, messages, tools=None, **kwargs):
        first = ""
        if messages:
            c = messages[0].get("content")
            first = c if isinstance(c, str) else ""
        text = confirm if ("triage reviewer" in first or "verification reviewer" in first) else '{"kind": "noop"}'
        yield StreamEvent(type="content_block_delta", data={"delta": {"type": "text_delta", "text": text}})
        yield StreamEvent(type="usage", data={"input_tokens": 1, "output_tokens": 1,
                                             "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0})
    agent_mod.stream_chat = stream_chat


def _board_with(run_id: str, findings: list[dict]) -> FakeBoard:
    b = FakeBoard()
    for f in findings:
        b.vulns.append({"id": f"v{len(b.vulns)+1}", "run_id": run_id, **f})
    return b


def test_config_noise_is_dismissed_without_llm():
    """CORS-only finding (no exploit marker) is auto-dismissed pre-filter."""
    board = _board_with("r1", [{
        "title": "CORS misconfiguration: preflight reflects any Origin",
        "severity": "high", "target": "https://example.com",
        "evidence": "Preflight OPTIONS returns Access-Control-Allow-Origin: <reflected>",
        "status": "pending",
    }])

    async def scenario():
        await run_verifier(_run("r1"), board, "sys")
        return board

    board = asyncio.run(scenario())
    assert board.vulns[0]["status"] == "dismissed", board.vulns[0]


def test_real_exploit_is_confirmed_and_severity_kept():
    """SQLi finding with demonstrated data access survives the gate."""
    _patch_verify_llm('{"confirm": true, "reason": "payload pulled admin session", "severity": "high"}')
    board = _board_with("r2", [{
        "title": "SQL injection in POST /api/login",
        "severity": "high", "target": "https://example.com/api/login",
        "evidence": "UNION SELECT returned the admin session token and role=admin",
        "status": "pending",
    }])

    async def scenario():
        await run_verifier(_run("r2"), board, "sys")
        return board

    board = asyncio.run(scenario())
    v = board.vulns[0]
    assert v["status"] == "confirmed", v
    assert v["severity"] == "high", v


def test_config_finding_severity_is_capped_to_low():
    """A config-ish finding with a real exploit angle passes the pre-filter
    but its severity is capped to low by SRC calibration."""
    _patch_verify_llm('{"confirm": true, "reason": "CORS read a token", "severity": "high"}')
    board = _board_with("r3", [{
        "title": "CORS misconfiguration reflects any Origin",
        "severity": "high", "target": "https://example.com",
        "evidence": "CORS allowed a malicious origin to access another user's session token (accessed user data)",
        "status": "pending",
    }])

    async def scenario():
        await run_verifier(_run("r3"), board, "sys")
        return board

    board = asyncio.run(scenario())
    v = board.vulns[0]
    assert v["status"] == "confirmed", v
    assert v["severity"] == "low", v

"""SRC acceptance gate tests: config noise is dismissed, real exploits
survive, and severity is calibrated."""

from __future__ import annotations

import asyncio

import core.agent as agent_mod
import core.verifier as verifier_mod
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


def test_account_enumeration_finding_is_dismissed_without_llm():
    """A bare account-existence oracle (isNeed:true means the account exists)
    is auto-dismissed by the enumeration hard filter — SRC treats it as noise
    unless chained to an exploit."""
    board = _board_with("r4", [{
        "title": "未鉴权用户名枚举 oracle (checkNeedCaptcha 泄露账号存在性)",
        "severity": "medium",
        "target": "https://sso.example.com/authserver/checkNeedCaptcha.htl",
        "evidence": "对候选用户名逐一遍历, 返回 isNeed:true 的视为已存在账号; 端点未鉴权可批量枚举",
        "status": "pending",
    }])

    async def scenario():
        await run_verifier(_run("r4"), board, "sys")
        return board

    board = asyncio.run(scenario())
    assert board.vulns[0]["status"] == "dismissed", board.vulns[0]


def test_enumeration_with_admin_username_still_dismissed():
    """'admin' inside a probed username (sysadmin / networkadmin) must NOT count
    as an exploit marker — a bare account probe is not privileged access."""
    board = _board_with("r7", [{
        "title": "未鉴权用户枚举 oracle: 探测 sysadmin/networkadmin 等高权限账号",
        "severity": "medium",
        "target": "https://sso.example.com/authserver/checkNeedCaptcha.htl",
        "evidence": "username=sysadmin 返回 isNeed:true, username=networkadmin 返回 isNeed:true, 证明账号存在",
        "status": "pending",
    }])

    async def scenario():
        await run_verifier(_run("r7"), board, "sys")
        return board

    board = asyncio.run(scenario())
    assert board.vulns[0]["status"] == "dismissed", board.vulns[0]


def test_unstable_boolean_sqli_oracle_is_dismissed_by_replay():
    """A boolean-SQLi finding whose differential flips between two identical
    requests is auto-dismissed by the mechanical replay — no LLM judgment call
    is wasted on a signal that does not reproduce."""
    original = verifier_mod._replay

    async def flaky_replay(v):
        return {
            "ok": True, "status": 200, "url": v.get("target", ""), "method": "GET",
            "stable": False,
            "requests": [{
                "method": "GET", "url": v.get("target", ""),
                "hits": [(200, '{"isNeed":true}'), (200, '{"isNeed":false}')],
            }],
        }

    verifier_mod._replay = flaky_replay
    try:
        board = _board_with("r5", [{
            "title": "LIKE 子句 boolean-based SQL 注入 (checkNeedCaptcha username)",
            "severity": "high",
            "target": "https://sso.example.com/authserver/checkNeedCaptcha.htl",
            "evidence": "admin' OR 'a'='a 返回 isNeed:true, 对照组返回 isNeed:false, 证明可控 SQL",
            "reproduction": "curl -i 'https://sso.example.com/authserver/checkNeedCaptcha.htl?username=admin%27%20OR%20%27a%27%3D%27a'",
            "status": "pending",
        }])

        async def scenario():
            await run_verifier(_run("r5"), board, "sys")
            return board

        board = asyncio.run(scenario())
    finally:
        verifier_mod._replay = original

    assert board.vulns[0]["status"] == "dismissed", board.vulns[0]


def test_stable_replay_survives_the_gate():
    """A finding whose PoC replays identically passes the mechanical gate and
    is judged by the LLM as before."""
    _patch_verify_llm('{"confirm": true, "reason": "stable replay, payload pulled admin session", "severity": "high"}')
    original = verifier_mod._replay

    async def stable_replay(v):
        return {
            "ok": True, "status": 200, "url": v.get("target", ""), "method": "POST",
            "stable": True,
            "requests": [{
                "method": "POST", "url": v.get("target", ""),
                "hits": [(200, '{"token":"abc"}'), (200, '{"token":"abc"}'), (200, '{"token":"abc"}')],
            }],
        }

    verifier_mod._replay = stable_replay
    try:
        board = _board_with("r6", [{
            "title": "SQL injection in POST /api/login",
            "severity": "high",
            "target": "https://example.com/api/login",
            "evidence": "UNION SELECT returned the admin session token and role=admin",
            "reproduction": "curl -X POST 'https://example.com/api/login' --data 'email=admin%27--'",
            "status": "pending",
        }])

        async def scenario():
            await run_verifier(_run("r6"), board, "sys")
            return board

        board = asyncio.run(scenario())
    finally:
        verifier_mod._replay = original

    v = board.vulns[0]
    assert v["status"] == "confirmed", v
    assert v["severity"] == "high", v


def test_parse_curl_requests_extracts_method_url_body():
    from core.verifier import _parse_curl_requests
    text = (
        "curl -i 'https://sso.example.com/authserver/checkNeedCaptcha.htl?username=admin%27%20OR%20%27a%27%3D%27a'\n"
        "curl -X POST 'https://example.com/api/login' -H 'Content-Type: application/json' --data '{\"user\":\"a\"}'\n"
    )
    reqs = _parse_curl_requests(text)
    assert len(reqs) == 2
    assert reqs[0]["method"] == "GET"
    assert "checkNeedCaptcha" in reqs[0]["url"]
    assert reqs[1]["method"] == "POST"
    assert reqs[1]["body"] == '{"user":"a"}'
    assert reqs[1]["headers"].get("Content-Type") == "application/json"

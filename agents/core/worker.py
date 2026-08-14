"""Explore worker: claims one intent, drives the LLM tool loop, and
concludes the intent with the resulting fact.

Workers never talk to each other — they coordinate through the board
(claim / conclude). Multiple workers can run concurrently (see
run_manager for the dispatcher)."""

from __future__ import annotations

import asyncio
import logging
from pathlib import Path
from typing import Any

from core.agent import AgentRun, render_graph_summary, render_template, run_tool_loop
from core.board import BoardClient
from tools.registry import ToolRegistry

log = logging.getLogger(__name__)

PROMPTS_DIR = Path(__file__).resolve().parent.parent / "prompts"
MAX_CONCLUSION_CHARS = 4000

# Markers that suggest the exploration found nothing worth concluding as a
# fact — in that case we fail the intent instead of polluting the board.
_DEAD_END_MARKERS = (
    "no vulnerability", "nothing found", "dead end", "no findings",
    "did not find", "could not confirm", "not vulnerable", "no evidence",
)


def render_auth_note(auth: dict[str, Any] | None) -> str:
    """Describe the authenticated session / account to the LLM and tell it
    how to reach behind-auth functionality (log in, capture the session)."""
    if not auth:
        return "没有已配置的登录会话。只测公开面。"
    cookies = (auth.get("cookies") or "")
    headers = (auth.get("headers") or {})
    note = auth.get("note") or ""
    username = (auth.get("username") or "")
    password = (auth.get("password") or "")
    login_url = (auth.get("login_url") or "")
    lines = ["目标配置了登录会话。http_request 会自动为匹配该目标的请求附加已存 Cookie。"]
    if cookies:
        lines.append(f"- 已有 Cookie: {cookies[:200]}{'…' if len(cookies) > 200 else ''}")
    for k, v in (headers or {}).items():
        lines.append(f"- 请求头 {k}: {str(v)[:120]}")
    if username and password:
        lines.append(f"- 账号: {username} / 密码已提供")
        if login_url:
            lines.append(f"- 登录地址: {login_url}")
        lines.append("- 如果还没登录：找到登录接口（POST 表单或 JSON），用 http_request 提交账号密码，"
                     "从响应 Set-Cookie 拿到会话，然后调用 `session_set` 保存——之后 http_request 会自动携带。"
                     "登录后重点测：IDOR、越权、登录后才可见的业务接口、权限提升。")
    if note:
        lines.append(f"- 备注: {note}")
    lines.append("- 测匿名面时，http_request 传 `inject_auth: false`；对比匿名 vs 已登录响应来找越权/IDOR。")
    return "\n".join(lines)


def _load_prompt(name: str) -> str:
    try:
        return (PROMPTS_DIR / name).read_text(encoding="utf-8")
    except FileNotFoundError:
        return ""


async def run_explore_worker(
    run: AgentRun,
    board: BoardClient,
    registry: ToolRegistry,
    system_prompt: str,
    intent: dict[str, Any],
    worker_name: str,
    auth_context: dict[str, Any] | None = None,
    llm_config: dict[str, Any] | None = None,
) -> str | None:
    """Return the concluded fact description, or None if not concluded."""
    intent_id = intent.get("id", "?")
    try:
        claimed = await board.claim_intent(run.run_id, intent_id, worker_name)
        if not claimed:
            log.info("worker %s: intent %s already claimed, skip", worker_name, intent_id)
            return None

        graph = await board.graph(run.run_id)
        auth_note = render_auth_note(auth_context)
        template = _load_prompt("explore.md")
        user_content = render_template(
            template,
            origin=run.target,
            goal=run.objective,
            graph_summary=render_graph_summary(graph),
            intent_id=intent_id,
            intent_description=intent.get("description", ""),
            auth_context=auth_note,
        ) if template else (
            f"Target: {run.target}\nGoal: {run.objective}\n\n"
            f"{render_graph_summary(graph)}\n\n"
            f"Explore this intent: {intent_id} — {intent.get('description', '')}\n"
            f"Session: {auth_note}\n"
            "Use tools, record findings, then summarize what you confirmed."
        )

        conclusion = await run_tool_loop(run, registry, system=system_prompt, user_content=user_content, llm_config=llm_config)
        conclusion = (conclusion or "").strip()
        if len(conclusion) > MAX_CONCLUSION_CHARS:
            conclusion = conclusion[:MAX_CONCLUSION_CHARS] + "…"

        low = conclusion.lower()
        if not conclusion or any(m in low for m in _DEAD_END_MARKERS) or low == "(no final summary text produced)":
            await board.fail_intent(run.run_id, intent_id, worker_name)
            log.info("worker %s: intent %s was a dead end", worker_name, intent_id)
            return None

        fact_id = await board.conclude_intent(run.run_id, intent_id, worker_name, conclusion)
        log.info("worker %s: concluded intent %s -> fact %s", worker_name, intent_id, fact_id)
        return conclusion
    except asyncio.CancelledError:
        log.info("worker %s: cancelled on intent %s", worker_name, intent_id)
        raise
    except Exception as e:  # noqa: BLE001
        log.warning("worker %s: intent %s errored: %s", worker_name, intent_id, e)
        try:
            await board.release_intent(run.run_id, intent_id, worker_name)
        except Exception:  # noqa: BLE001
            pass
        return None

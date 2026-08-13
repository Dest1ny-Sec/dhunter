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
    """Describe the authenticated session to the LLM (without dumping huge
    values), and remind it to compare anonymous vs authenticated access."""
    if not auth:
        return "No authenticated session is configured. Test the public surface only."
    cookies = (auth.get("cookies") or "")
    headers = (auth.get("headers") or {})
    note = auth.get("note") or ""
    lines = ["An authenticated session is configured for this target. http_request "
             "will AUTO-ATTACH these to requests whose URL matches the target host."]
    if cookies:
        lines.append(f"- cookies: {cookies[:200]}{'…' if len(cookies) > 200 else ''}")
    for k, v in (headers or {}).items():
        lines.append(f"- header {k}: {str(v)[:120]}")
    if note:
        lines.append(f"- note: {note}")
    lines.append("- To test the ANONYMOUS surface, call http_request with `inject_auth: false`. "
                 "Compare anonymous vs authenticated responses to hunt for IDOR / privilege "
                 "escalation / missing-auth issues.")
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

        conclusion = await run_tool_loop(run, registry, system=system_prompt, user_content=user_content)
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

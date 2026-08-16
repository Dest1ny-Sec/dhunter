"""Reason step: given the board, decide what to explore next.

Reads a compact graph summary and asks the LLM for new intents, a noop,
or a complete. Returns one of ("intents", n_created) / ("noop", None) /
("complete", summary)."""

from __future__ import annotations

import logging
import os
from pathlib import Path
from typing import Any

from core.agent import call_llm_text, parse_json_object, render_graph_summary, render_template
from core.board import BoardClient

log = logging.getLogger(__name__)

PROMPTS_DIR = Path(__file__).resolve().parent.parent / "prompts"
MAX_REASON_INTENTS = int(os.environ.get("DHUNTER_REASON_MAX_INTENTS", "3"))


def _load_prompt(name: str) -> str:
    try:
        return (PROMPTS_DIR / name).read_text(encoding="utf-8")
    except FileNotFoundError:
        log.warning("prompt %s not found, using fallback", name)
        return ""


async def run_reason_step(
    run,
    board: BoardClient,
    system_prompt: str,
    *,
    max_intents: int = MAX_REASON_INTENTS,
    llm_config: dict[str, Any] | None = None,
    known_fact_ids: set[str] | None = None,
) -> tuple[str, Any]:
    """One reason turn. Returns (kind, payload).

    `known_fact_ids` (the planner's previous view) enables INCREMENTAL
    planning: only facts the planner has not seen are listed, so each reason
    turn stops re-paying tokens for facts it already knows."""
    graph = await board.graph(run.run_id)
    template = _load_prompt("reason.md")
    user_content = render_template(
        template,
        origin=run.target,
        goal=run.objective,
        graph_summary=render_graph_summary(graph, known_fact_ids=known_fact_ids),
        max_intents=str(max_intents),
    ) if template else (
        f"Target: {run.target}\nGoal: {run.objective}\n\n"
        f"{render_graph_summary(graph, known_fact_ids=known_fact_ids)}\n\n"
        f"Return JSON: {{'kind': 'intents'|'noop'|'complete', ...}} at most {max_intents} intents."
    )

    text = await call_llm_text(run, system=system_prompt, user_content=user_content, llm_config=llm_config)
    parsed = parse_json_object(text)
    kind = parsed.get("kind")

    if kind == "intents":
        created = 0
        existing = {i.get("description", "").strip() for i in (graph.get("intents") or [])}
        for it in parsed.get("intents") or []:
            if not isinstance(it, dict):
                continue
            desc = str(it.get("description", "")).strip()
            if not desc or len(desc) > 500:
                continue
            if desc in existing:
                continue  # already proposed
            existing.add(desc)
            from_facts = it.get("from") or []
            if not isinstance(from_facts, list):
                from_facts = []
            await board.create_intent(run.run_id, from_facts, desc, creator="reason")
            created += 1
        if created == 0:
            log.info("reason produced no new intents run=%s", run.run_id)
            return ("noop", None)
        return ("intents", created)

    if kind == "complete":
        summary = str(parsed.get("summary", "")).strip()
        return ("complete", summary or "objective met")

    return ("noop", None)

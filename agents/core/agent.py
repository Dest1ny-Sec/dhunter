"""Agent primitives shared by the blackboard engine.

This module owns the "shape" of a run:
  - AgentRun: per-run state plus a bounded non-blocking event queue.
  - run_tool_loop: the LLM + tool-call loop a worker executes to explore
    one intent (extracted from the old single-agent loop).
  - call_llm_text: a tool-less LLM turn that returns text (used by reason).
  - render_graph_summary: turn the board graph into a compact prompt chunk
    without dumping the whole history into the context window.

The old single-agent loop is gone: a run is now a scheduler (run_manager)
that drives reason steps and parallel explore workers, all coordinating
through the board.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from llm.client import stream_chat
from tools.registry import ToolRegistry

log = logging.getLogger(__name__)

# Timeouts are env-tunable. The Go bridge holds the stream open for
# OVERALL_TIMEOUT + 10m, so keep OVERALL_TIMEOUT below the bridge deadline.
OVERALL_TIMEOUT = float(os.environ.get("DHUNTER_AGENT_OVERALL_TIMEOUT", "1800"))  # default 30 min (time-based run limit)
STEP_TIMEOUT = float(os.environ.get("DHUNTER_AGENT_STEP_TIMEOUT", "120"))          # default 120s per LLM turn
MAX_ITERATIONS = int(os.environ.get("DHUNTER_AGENT_MAX_ITERATIONS", "40"))         # tool-loop cap per worker
# Tool outputs are fed back to the LLM on the next turn and re-sent on every
# subsequent turn, so a large cap directly inflates the context for the rest
# of the loop. 8k keeps payloads meaningful without bloating the window;
# agents that need more re-issue a targeted request.
MAX_TOOL_RESULT_CHARS = 8_000  # truncate huge tool outputs before the next LLM turn

# --- context compaction (sliding window) -----------------------------------
#
# The tool loop re-sends the WHOLE message history every turn, so a long run
# grows the context without bound. When the number of turns exceeds
# MAX_CONTEXT_ROUNDS, the oldest turns are collapsed into a compact summary
# (one line per tool call) merged into the initial user message, keeping only
# the most recent _COMPACT_KEEP_ROUNDS turns fully intact. This caps the
# context for 30-minute runs at the cost of one prompt-cache miss when the
# prefix changes — ordinary runs (≤ MAX_CONTEXT_ROUNDS turns) never trigger
# it, so their cache stays intact.
#
# NOTE on "keep only recent thinking": Anthropic-style reasoning blocks must
# be echoed back verbatim WITH their signature (an altered thinking text
# fails signature validation), so thinking cannot be trimmed in place. The
# sliding window achieves the same goal safely: whole old turns — including
# their thinking blocks — are dropped together.
MAX_CONTEXT_ROUNDS = int(os.environ.get("DHUNTER_MAX_CONTEXT_ROUNDS", "20"))
_COMPACT_KEEP_ROUNDS = 12


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


@dataclass
class AgentRun:
    run_id: str
    target: str
    objective: str
    queue: asyncio.Queue
    status: str = "queued"
    summary: str = ""
    error: str | None = None
    created_at: str = field(default_factory=_now_iso)
    finished_at: str | None = None
    task: asyncio.Task | None = None
    # Set by the /pause endpoint; the run_manager loop checks it and stops
    # dispatching without emitting a terminal run_done (the board is kept, so
    # the run can be resumed via /continue).
    pause_event: asyncio.Event | None = None

    async def emit(self, event: str, data: dict[str, Any]) -> None:
        # Never block the agent on a full queue: if the SSE consumer (the
        # Go bridge / a browser) is disconnected or slow, drop the oldest
        # queued event instead of stalling the run. asyncio runs single-
        # threaded, so between full() and put() there is no interleaving —
        # this is race-free as long as the agent is the only producer.
        if self.queue.full():
            try:
                self.queue.get_nowait()
            except asyncio.QueueEmpty:
                pass
        await self.queue.put({"event": event, "data": data})


# --- LLM + tool loop -----------------------------------------------------


def _summarize_turn(assistant: dict[str, Any], tool_user: dict[str, Any]) -> str:
    """One compact line per tool call in a dropped turn:
    `· tool_name(args) → ok|error`. Pairs tool_use blocks (assistant
    message) with their tool_results (next user message) by tool_use_id."""
    result_map: dict[str, dict[str, Any]] = {}
    for rb in tool_user.get("content") or []:
        if isinstance(rb, dict) and rb.get("type") == "tool_result":
            result_map[str(rb.get("tool_use_id") or "")] = rb
    lines: list[str] = []
    for b in assistant.get("content") or []:
        if not isinstance(b, dict) or b.get("type") != "tool_use":
            continue
        name = str(b.get("name") or "?")
        args = json.dumps(b.get("input") or {}, ensure_ascii=False)
        if len(args) > 100:
            args = args[:100] + "…"
        rr = result_map.get(str(b.get("id") or "")) or {}
        status = "error" if rr.get("is_error") else "ok"
        lines.append(f"· {name}({args}) → {status}")
    return "\n".join(lines)


def _compact_messages(
    messages: list[dict[str, Any]],
    max_rounds: int = MAX_CONTEXT_ROUNDS,
    keep_rounds: int = _COMPACT_KEEP_ROUNDS,
) -> list[dict[str, Any]]:
    """Slide the context window: when the turn count exceeds `max_rounds`,
    collapse the oldest turns into a compact summary merged into the initial
    user message, keeping only the most recent `keep_rounds` turns fully
    intact. Message roles stay alternating (user/assistant), so the request
    remains valid for Anthropic and compat endpoints."""
    if len(messages) < 3:
        return messages
    rounds = (len(messages) - 1) // 2  # entries beyond the initial user form user/assistant pairs
    if rounds <= max_rounds:
        return messages
    keep = max(1, min(keep_rounds, rounds))
    drop = (rounds - keep) * 2
    dropped = messages[1 : 1 + drop]
    kept = messages[1 + drop :]

    lines: list[str] = []
    for i in range(0, len(dropped), 2):
        asst = dropped[i]
        tuser = dropped[i + 1] if i + 1 < len(dropped) else {}
        line = _summarize_turn(asst, tuser)
        if line:
            lines.append(line)
    summary_text = (
        "（以下为早期轮次的工具活动摘要，已压缩以节省上下文）\n"
        + ("\n".join(lines) if lines else "（早期轮次无工具调用）")
    )
    head = dict(messages[0])
    head["content"] = str(head.get("content") or "") + "\n\n" + summary_text
    return [head] + kept


async def run_tool_loop(
    run: AgentRun,
    registry: ToolRegistry,
    *,
    system: str,
    user_content: str,
    max_iterations: int = MAX_ITERATIONS,
    step_timeout: float = STEP_TIMEOUT,
    llm_config: dict[str, Any] | None = None,
) -> str:
    """Drive one worker's exploration: LLM turns interleaved with tool
    calls until the model stops requesting tools. Returns the final text
    summary (used as the intent's conclusion).

    `llm_config` (provider/base_url/model/api_key/max_tokens) is threaded
    through per-run so concurrent runs never clobber each other's config
    (we deliberately do NOT mutate the process environment)."""
    messages: list[dict[str, Any]] = [{"role": "user", "content": user_content}]
    final_text_parts: list[str] = []

    for iteration in range(max_iterations):
        tools = registry.all_tools()

        text_buf = ""
        tool_uses: list[dict[str, Any]] = []
        thinking_blocks: list[dict[str, Any]] = []
        current_tool: dict[str, Any] | None = None
        current_tool_input_json = ""
        current_thinking: dict[str, Any] | None = None
        stop_reason: str | None = None

        try:
            async with asyncio.timeout(step_timeout):
                async for ev in stream_chat(system=system, messages=messages, tools=tools, **(llm_config or {})):
                    t = ev.type
                    if t == "content_block_start":
                        block = ev.data.get("content_block") or {}
                        btype = block.get("type")
                        if btype == "tool_use":
                            current_tool = {"id": block.get("id"), "name": block.get("name"), "input": None}
                            current_tool_input_json = ""
                        elif btype == "thinking":
                            # Preserve the block (with signature) so it can be
                            # echoed back — Anthropic requires it on the next
                            # turn; reasoning models like DeepSeek emit it too.
                            current_thinking = {"type": "thinking", "thinking": "", "signature": block.get("signature", "")}
                    elif t == "content_block_delta":
                        delta = ev.data.get("delta") or {}
                        dtype = delta.get("type")
                        if dtype == "text_delta":
                            chunk = delta.get("text", "") or ""
                            text_buf += chunk
                            await run.emit("response_delta", {"delta": chunk, "accumulated": text_buf})
                        elif dtype == "input_json_delta":
                            current_tool_input_json += delta.get("partial_json", "") or ""
                        elif dtype == "thinking_delta":
                            chunk = delta.get("thinking", "") or ""
                            if current_thinking is not None:
                                current_thinking["thinking"] += chunk
                            await run.emit("reasoning_delta", {"delta": chunk, "accumulated": ""})
                    elif t == "content_block_stop":
                        if current_tool is not None:
                            raw = current_tool_input_json
                            try:
                                parsed = json.loads(raw) if raw else {}
                            except json.JSONDecodeError:
                                parsed = {"_raw": raw}
                            current_tool["input"] = parsed
                            tool_uses.append(current_tool)
                            current_tool = None
                            current_tool_input_json = ""
                        elif current_thinking is not None:
                            thinking_blocks.append(current_thinking)
                            current_thinking = None
                    elif t == "message_delta":
                        stop_reason = (ev.data.get("delta") or {}).get("stop_reason") or stop_reason
                    elif t == "usage":
                        try:
                            await run.emit("token_usage", ev.data)
                        except Exception:  # noqa: BLE001
                            log.warning("failed to emit token_usage event", exc_info=True)
                    elif t == "error":
                        raise RuntimeError(f"LLM error event: {json.dumps(ev.data)[:500]}")
        except asyncio.TimeoutError as e:
            raise RuntimeError(f"LLM step timeout ({step_timeout:.0f}s) on iteration {iteration}") from e

        content_blocks: list[dict[str, Any]] = []
        # Anthropic requires thinking blocks before text/tool_use, and the
        # signature must be echoed back verbatim.
        content_blocks.extend(thinking_blocks)
        if text_buf:
            content_blocks.append({"type": "text", "text": text_buf})
            final_text_parts.append(text_buf)
        for tu in tool_uses:
            content_blocks.append({"type": "tool_use", "id": tu["id"], "name": tu["name"], "input": tu["input"] or {}})
        if not content_blocks:
            raise RuntimeError("LLM returned an empty message (no text and no tool calls)")

        messages.append({"role": "assistant", "content": content_blocks})
        await run.emit("message_done", {"role": "assistant", "content": text_buf})

        if not tool_uses:
            break

        tool_result_blocks: list[dict[str, Any]] = []
        for tu in tool_uses:
            name = tu["name"]
            args = tu["input"] or {}
            # call_id is the LLM's tool_use block id — the bridge relays it
            # to the frontend so a tool_call can be paired with its
            # tool_result (one logical invocation = one UI row).
            call_id = str(tu.get("id") or "")
            await run.emit("tool_call", {"name": name, "arguments": args, "call_id": call_id})
            t0 = time.monotonic()
            try:
                result = await registry.call(name, args, current_run_id=run.run_id)
            except Exception as e:  # noqa: BLE001
                result = {"content": f"tool `{name}` crashed: {type(e).__name__}: {e}", "is_error": True}
            duration_ms = int((time.monotonic() - t0) * 1000)
            content_str = result["content"] or ""
            if len(content_str) > MAX_TOOL_RESULT_CHARS:
                content_str = content_str[:MAX_TOOL_RESULT_CHARS] + f"\n... [truncated to {MAX_TOOL_RESULT_CHARS} chars]"
            await run.emit("tool_result", {
                "name": name, "content": content_str,
                "is_error": bool(result.get("is_error")), "duration_ms": duration_ms,
                "call_id": call_id,
            })
            tool_result_blocks.append({"type": "tool_result", "tool_use_id": tu["id"], "content": content_str, "is_error": bool(result.get("is_error"))})

        messages.append({"role": "user", "content": tool_result_blocks})
        # Sliding window: keep the context bounded on long runs (see the
        # compaction docstring above).
        messages = _compact_messages(messages)

    summary = "\n\n".join(p for p in final_text_parts if p).strip() or "(no final summary text produced)"
    return summary


async def call_llm_text(
    run: AgentRun,
    *,
    system: str,
    user_content: str,
    step_timeout: float = STEP_TIMEOUT,
    llm_config: dict[str, Any] | None = None,
) -> str:
    """A single tool-less LLM turn that returns the accumulated text.
    Used by the reason step and the vulnerability verifier."""
    buf = ""
    try:
        async with asyncio.timeout(step_timeout):
            async for ev in stream_chat(system=system, messages=[{"role": "user", "content": user_content}], **(llm_config or {})):
                if ev.type == "content_block_delta":
                    delta = ev.data.get("delta") or {}
                    if delta.get("type") == "text_delta":
                        chunk = delta.get("text", "") or ""
                        buf += chunk
                        await run.emit("response_delta", {"delta": chunk, "accumulated": buf})
                    elif delta.get("type") == "thinking_delta":
                        await run.emit("reasoning_delta", {"delta": delta.get("thinking", "") or "", "accumulated": ""})
                elif ev.type == "usage":
                    try:
                        await run.emit("token_usage", ev.data)
                    except Exception:  # noqa: BLE001
                        pass
                elif ev.type == "error":
                    raise RuntimeError(f"LLM error event: {json.dumps(ev.data)[:500]}")
    except asyncio.TimeoutError as e:
        raise RuntimeError(f"LLM step timeout ({step_timeout:.0f}s)") from e
    return buf


def render_template(template: str, **kwargs: str) -> str:
    """Replace only the named {placeholders}; leave JSON braces untouched.

    str.format() would choke on the literal {...} JSON examples inside the
    prompts, so we do targeted replacement instead.
    """
    for key, value in kwargs.items():
        template = template.replace("{" + key + "}", str(value))
    return template


def parse_json_object(text: str) -> dict[str, Any]:
    """Tolerant JSON extraction: try the whole text, then the first
    balanced {...} block. Returns {} on failure."""
    text = text.strip()
    try:
        obj = json.loads(text)
        if isinstance(obj, dict):
            return obj
    except json.JSONDecodeError:
        pass
    # Find the first { ... } block (balanced brace scan).
    start = text.find("{")
    if start >= 0:
        depth = 0
        in_str = False
        esc = False
        for i in range(start, len(text)):
            ch = text[i]
            if in_str:
                if esc:
                    esc = False
                elif ch == "\\":
                    esc = True
                elif ch == '"':
                    in_str = False
                continue
            if ch == '"':
                in_str = True
            elif ch == "{":
                depth += 1
            elif ch == "}":
                depth -= 1
                if depth == 0:
                    try:
                        obj = json.loads(text[start : i + 1])
                        if isinstance(obj, dict):
                            return obj
                    except json.JSONDecodeError:
                        pass
                    break
    return {}


# --- graph summary -------------------------------------------------------


# Cap how many facts the planner/worker reads each turn — the LLM pays tokens
# for the whole summary. Lower = cheaper but less context; 40 is a good balance.
REASON_MAX_FACTS = int(os.environ.get("DHUNTER_REASON_MAX_FACTS", "40"))


def render_graph_summary(graph: dict[str, Any], max_facts: int = REASON_MAX_FACTS, known_fact_ids: set[str] | None = None) -> str:
    """Compact, token-cheap rendering of the board for LLM prompts.
    Facts are one line each; open intents and hints are listed. The full
    graph lives in the backend — this is the *summary* fed to the model.

    `known_fact_ids` (the planner's previous view) enables INCREMENTAL
    planning: only facts the planner has not seen yet are listed, so each
    reason turn stops re-paying tokens for facts it already knows."""
    lines: list[str] = []
    facts = graph.get("facts") or []
    intents = graph.get("intents") or []
    hints = graph.get("hints") or []

    if known_fact_ids:
        new_facts = [f for f in facts if f.get("id") not in known_fact_ids]
        lines.append(f"## Confirmed facts ({len(facts)} total, {len(new_facts)} new since last planning)")
        recent = new_facts[-max_facts:]
        for i, f in enumerate(recent):
            desc = (f.get("description") or "").strip().replace("\n", " ")
            if len(desc) > 140:
                desc = desc[:140] + "…"
            lines.append(f"- {f.get('id', '?')}: {desc}")
        if not new_facts:
            lines.append("- (no new facts since last planning)")
    else:
        lines.append(f"## Confirmed facts ({len(facts)})")
        recent = facts[-max_facts:]
        dropped = len(facts) - len(recent)
        if dropped > 0:
            lines.append(f"(showing the latest {len(recent)} of {len(facts)} facts)")
        for i, f in enumerate(recent):
            desc = (f.get("description") or "").strip().replace("\n", " ")
            if len(desc) > 140:
                desc = desc[:140] + "…"
            lines.append(f"- {f.get('id', '?')}: {desc}")

    open_its = [i for i in intents if i.get("status") in ("open", "claimed")]
    lines.append(f"## Open intents ({len(open_its)})")
    if open_its:
        for i in open_its:
            state = "claimed" if i.get("status") == "claimed" else "open"
            lines.append(f"- {i.get('id', '?')} [{state}]: {i.get('description', '')}")
    else:
        lines.append("- (none)")

    # Already-explored directions (concluded = produced a fact; failed =
    # dead end) so the planner doesn't re-propose the same work.
    explored = [i for i in intents if i.get("status") in ("concluded", "failed")]
    if explored:
        lines.append(f"## Already explored ({len(explored)})")
        for i in explored[-15:]:
            mark = "dead-end" if i.get("status") == "failed" else "done"
            lines.append(f"- {i.get('id', '?')} [{mark}]: {i.get('description', '')}")

    if hints:
        lines.append("## Human hints")
        for h in hints:
            lines.append(f"- {h.get('content', '')}")

    return "\n".join(lines)

"""Single-agent loop for Dhunter.

Drives the LLM in iterations: send messages -> stream response -> execute
any tool_use blocks -> append tool_results -> repeat. Stops when the
model no longer requests a tool, or when an iteration / overall timeout
fires. All progress is emitted through the run's event queue as SSE
events.

Timeouts:
    OVERALL_TIMEOUT  hard cap on the whole run (default 1h)
    STEP_TIMEOUT     cap on a single LLM turn (default 120s)

Tool calls themselves have no agent-level timeout -- the underlying
httpx clients cap them (60s for MCP, 30s default for http_request).
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

from llm.anthropic_client import stream_chat
from tools.registry import ToolRegistry

log = logging.getLogger(__name__)

# Timeouts are env-tunable so ops can tighten or loosen them without a
# code change. The Go bridge holds the stream open for OVERALL_TIMEOUT+10m,
# so keep OVERALL_TIMEOUT comfortably below the bridge deadline.
OVERALL_TIMEOUT = float(os.environ.get("DHUNTER_AGENT_OVERALL_TIMEOUT", "3600"))  # default 1h
STEP_TIMEOUT = float(os.environ.get("DHUNTER_AGENT_STEP_TIMEOUT", "120"))          # default 120s per LLM turn
MAX_ITERATIONS = int(os.environ.get("DHUNTER_AGENT_MAX_ITERATIONS", "40"))         # safety cap per worker turn
MAX_TOOL_RESULT_CHARS = 20_000  # truncate huge tool outputs before they hit the next LLM turn


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


async def run_agent(
    run: AgentRun,
    registry: ToolRegistry,
    *,
    system_prompt: str,
    max_iterations: int = MAX_ITERATIONS,
) -> None:
    """Top-level coroutine: drive the loop and always emit run_done."""
    started = time.monotonic()
    run.status = "running"
    try:
        await asyncio.wait_for(
            _drive(run, registry, system_prompt, max_iterations, started),
            timeout=OVERALL_TIMEOUT,
        )
    except asyncio.TimeoutError:
        run.status = "failed"
        run.error = f"overall timeout ({int(OVERALL_TIMEOUT)}s) exceeded"
        log.warning("run %s timed out after %ss", run.run_id, OVERALL_TIMEOUT)
        await run.emit("run_done", {"status": "failed", "error": run.error})
    except asyncio.CancelledError:
        run.status = "failed"
        run.error = "run cancelled"
        log.warning("run %s cancelled", run.run_id)
        try:
            await run.emit("run_done", {"status": "failed", "error": run.error})
        except Exception:  # noqa: BLE001
            pass
        raise
    except Exception as e:  # noqa: BLE001
        run.status = "failed"
        run.error = f"{type(e).__name__}: {e}"
        log.exception("run %s failed", run.run_id)
        await run.emit("run_done", {"status": "failed", "error": run.error})
    finally:
        run.finished_at = _now_iso()


async def _drive(
    run: AgentRun,
    registry: ToolRegistry,
    system_prompt: str,
    max_iterations: int,
    started: float,
) -> None:
    messages: list[dict[str, Any]] = [
        {
            "role": "user",
            "content": (
                f"## Target\n{run.target}\n\n"
                f"## Objective\n{run.objective}\n\n"
                f"## Run id\n{run.run_id}\n\n"
                "Begin with passive recon, then move to active testing. "
                "Use http_request for any HTTP probe, write_finding to record confirmed vulns. "
                "When you stop, summarise what you tested, what you found, and what needs re-test."
            ),
        }
    ]

    final_text_parts: list[str] = []

    for iteration in range(max_iterations):
        if time.monotonic() - started > OVERALL_TIMEOUT:
            raise RuntimeError(f"overall timeout ({int(OVERALL_TIMEOUT)}s) exceeded before iteration {iteration}")

        tools = registry.all_tools()

        # --- LLM turn (per-step timeout) ---------------------------------
        text_buf = ""
        tool_uses: list[dict[str, Any]] = []
        current_tool: dict[str, Any] | None = None
        current_tool_input_json = ""
        stop_reason: str | None = None

        try:
            async with asyncio.timeout(STEP_TIMEOUT):
                async for ev in stream_chat(
                    system=system_prompt,
                    messages=messages,
                    tools=tools,
                ):
                    t = ev.type
                    if t == "content_block_start":
                        block = ev.data.get("content_block") or {}
                        btype = block.get("type")
                        if btype == "tool_use":
                            current_tool = {
                                "id": block.get("id"),
                                "name": block.get("name"),
                                "input": None,
                            }
                            current_tool_input_json = ""
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
                            # Anthropic extended thinking -> reasoning stream
                            await run.emit(
                                "reasoning_delta",
                                {"delta": delta.get("thinking", "") or "", "accumulated": ""},
                            )
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
                    elif t == "message_delta":
                        stop_reason = (ev.data.get("delta") or {}).get("stop_reason") or stop_reason
                    elif t == "message_stop":
                        pass
                    elif t == "usage":
                        # Cumulative token usage for this LLM call.
                        # We push it as an SSE event so the Go bridge
                        # can persist it under the run.
                        try:
                            await run.emit("token_usage", ev.data)
                        except Exception:  # noqa: BLE001
                            log.warning("failed to emit token_usage event", exc_info=True)
                    elif t == "error":
                        raise RuntimeError(f"LLM error event: {json.dumps(ev.data)[:500]}")
        except asyncio.TimeoutError as e:
            raise RuntimeError(f"LLM step timeout ({int(STEP_TIMEOUT)}s) on iteration {iteration}") from e

        # --- Build the assistant message --------------------------------
        content_blocks: list[dict[str, Any]] = []
        if text_buf:
            content_blocks.append({"type": "text", "text": text_buf})
            final_text_parts.append(text_buf)
        for tu in tool_uses:
            content_blocks.append({
                "type": "tool_use",
                "id": tu["id"],
                "name": tu["name"],
                "input": tu["input"] or {},
            })
        if not content_blocks:
            raise RuntimeError("LLM returned an empty message (no text and no tool calls)")

        messages.append({"role": "assistant", "content": content_blocks})
        await run.emit("message_done", {"role": "assistant", "content": text_buf})

        if not tool_uses:
            # LLM stopped calling tools -- we're done.
            break

        # --- Execute tools sequentially (MVP: simple, predictable) -------
        tool_result_blocks: list[dict[str, Any]] = []
        for tu in tool_uses:
            name = tu["name"]
            args = tu["input"] or {}
            await run.emit("tool_call", {"name": name, "arguments": args})
            t0 = time.monotonic()
            try:
                result = await registry.call(name, args, current_run_id=run.run_id)
            except Exception as e:  # noqa: BLE001
                result = {
                    "content": f"tool `{name}` crashed: {type(e).__name__}: {e}",
                    "is_error": True,
                }
            duration_ms = int((time.monotonic() - t0) * 1000)
            content_str = result["content"] or ""
            if len(content_str) > MAX_TOOL_RESULT_CHARS:
                content_str = content_str[:MAX_TOOL_RESULT_CHARS] + f"\n... [truncated to {MAX_TOOL_RESULT_CHARS} chars]"
            await run.emit("tool_result", {
                "name": name,
                "content": content_str,
                "is_error": bool(result.get("is_error")),
                "duration_ms": duration_ms,
            })
            tool_result_blocks.append({
                "type": "tool_result",
                "tool_use_id": tu["id"],
                "content": content_str,
                "is_error": bool(result.get("is_error")),
            })

        messages.append({"role": "user", "content": tool_result_blocks})
        await run.emit(
            "message_done",
            {
                "role": "tool_results",
                "content": json.dumps(
                    [
                        {
                            "tool_use_id": b.get("tool_use_id", "?"),
                            "name": _tool_name_for(tu, tool_uses),
                            "ok": not b.get("is_error"),
                        }
                        for b, tu in zip(tool_result_blocks, tool_uses)
                    ]
                ),
            },
        )

    # --- End of loop ----------------------------------------------------
    summary = "\n\n".join(p for p in final_text_parts if p).strip() or "(no final summary text produced)"
    run.status = "success"
    run.summary = summary
    await run.emit("run_done", {"status": "success", "summary": summary})


def _tool_name_for(tool_use: dict[str, Any], all_uses: list[dict[str, Any]]) -> str:
    """Tiny helper to look up the name for a tool_use_id (used in summary)."""
    return tool_use.get("name") or "?"

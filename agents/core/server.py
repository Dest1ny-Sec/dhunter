"""FastAPI server for the Dhunter single-agent service.

Endpoints:
    POST /v1/runs              start a new agent run (returns 202)
    GET  /v1/runs/{id}/events  SSE stream of agent events
    GET  /v1/runs/{id}         read final run state
    GET  /healthz              liveness probe (returns ok)

Binds to 127.0.0.1:9100 by default. Override with:
    DHUNTER_AGENT_HOST  (default 127.0.0.1)
    DHUNTER_AGENT_PORT  (default 9100)
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import sys
from contextlib import asynccontextmanager
from pathlib import Path
from typing import AsyncIterator

from fastapi import Depends, FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field
from sse_starlette.sse import EventSourceResponse

# Make sibling package imports work whether this file is run as a module
# (`python -m core.server`) or imported (`from core.server import app`).
_HERE = Path(__file__).resolve().parent
_AGENTS_DIR = _HERE.parent
if str(_AGENTS_DIR) not in sys.path:
    sys.path.insert(0, str(_AGENTS_DIR))

from core.agent import AgentRun  # noqa: E402
from core.board import BoardClient  # noqa: E402
from core.run_manager import RunManager  # noqa: E402
from tools.registry import ToolRegistry  # noqa: E402

from core.logconfig import setup_logging  # noqa: E402

log_dir = setup_logging()
log = logging.getLogger("dhunter.agent")

PROMPT_PATH = _AGENTS_DIR / "prompts" / "system.md"
DEFAULT_PROMPT = "You are a security testing agent. Be thorough, manual, evidence-based."

RUNS: dict[str, AgentRun] = {}
QUEUES: dict[str, asyncio.Queue] = {}


def _load_system_prompt() -> str:
    try:
        return PROMPT_PATH.read_text(encoding="utf-8")
    except FileNotFoundError:
        log.warning("system prompt not found at %s, using fallback", PROMPT_PATH)
        return DEFAULT_PROMPT


@asynccontextmanager
async def lifespan(app: FastAPI):
    registry = ToolRegistry()
    try:
        await registry.initialize()
    except Exception as e:  # noqa: BLE001
        log.warning("tool registry init error: %s", e)
    # External MCP hub: fetch the user's enabled external MCP servers
    # and connect to each. Failure of one server is logged but does
    # not abort the rest (the hub isolates per-server).
    try:
        ext_status = await registry.ext.load_from_backend()
        if ext_status:
            connected = sum(1 for s in ext_status.values() if s.get("status") == "connected")
            log.info("external MCPs: %d/%d connected", connected, len(ext_status))
    except Exception as e:  # noqa: BLE001
        log.warning("external MCP load error: %s", e)
    app.state.registry = registry
    app.state.board = BoardClient()
    app.state.system_prompt = _load_system_prompt()
    log.info(
        "agent ready: tools=%d (mcp_loaded=%s)",
        len(registry.all_tools()),
        registry.mcp_status().get("tool_count", 0),
    )
    try:
        yield
    finally:
        await registry.aclose()
        await app.state.board.aclose()


app = FastAPI(title="dhunter-agent", version="0.1.0", lifespan=lifespan)


# --- Agent API auth ------------------------------------------------------
#
# The agent sidecar binds 127.0.0.1 by default but is still reachable by any
# local process (and by anything on the LAN when DHUNTER_AGENT_HOST is set
# to 0.0.0.0). Gate every /v1 route with a bearer token so the sidecar is
# protected by the same credential standard as the Go server and the MCP
# toolbelt. The token comes from DHUNTER_AGENT_TOKEN (the start scripts pass
# the platform admin token); when unset, auth is OFF and the server logs a
# prominent warning — explicit opt-out for local-only development.
_AUTH_TOKEN = os.environ.get("DHUNTER_AGENT_TOKEN", "").strip()
if not _AUTH_TOKEN:
    log.warning(
        "DHUNTER_AGENT_TOKEN is not set — the agent API (127.0.0.1:%s) is running "
        "WITHOUT authentication. Set it to the same bearer token as the platform "
        "server for a protected deployment.",
        os.environ.get("DHUNTER_AGENT_PORT", "9100"),
    )


def require_token(request: Request) -> None:
    """FastAPI dependency: enforce the bearer token when one is configured."""
    if not _AUTH_TOKEN:
        return
    header = request.headers.get("Authorization", "")
    if not header.startswith("Bearer "):
        raise HTTPException(status_code=401, detail="unauthorized")
    if header[len("Bearer "):].strip() != _AUTH_TOKEN:
        raise HTTPException(status_code=401, detail="unauthorized")


# --- Request / response models ------------------------------------------


class StartRunBody(BaseModel):
    run_id: str = Field(..., min_length=1, max_length=128)
    target: str = Field(..., min_length=1, max_length=2048)
    objective: str = Field(..., min_length=1, max_length=4096)


# --- Routes --------------------------------------------------------------


@app.get("/healthz")
async def healthz() -> dict[str, object]:
    return {"ok": True}


@app.get("/readyz")
async def readyz() -> dict[str, object]:
    """Cheap readiness: registry initialised, no fatal init errors."""
    registry: ToolRegistry | None = getattr(app.state, "registry", None)
    if registry is None:
        return {"ok": False, "reason": "registry not initialised"}
    status = registry.mcp_status()
    return {
        "ok": True,
        "tool_count": len(registry.all_tools()),
        "mcp": status,
    }


@app.post("/v1/runs", status_code=202, dependencies=[Depends(require_token)])
async def start_run(body: StartRunBody) -> JSONResponse:
    if body.run_id in RUNS:
        # Continue: the same run is being resumed from its durable board.
        # Drop the old in-memory run (the Python agent's live state), and
        # start a fresh agent loop that re-reads the board (facts/intents)
        # instead of a stale message history. This is how we "go deeper"
        # without the context growing forever.
        old = RUNS.pop(body.run_id, None)
        if old is not None and old.task is not None and not old.task.done():
            old.task.cancel()
        QUEUES.pop(body.run_id, None)

    queue: asyncio.Queue = asyncio.Queue(maxsize=2048)
    run = AgentRun(
        run_id=body.run_id,
        target=body.target,
        objective=body.objective,
        queue=queue,
        pause_event=asyncio.Event(),
    )
    RUNS[body.run_id] = run
    QUEUES[body.run_id] = queue

    registry: ToolRegistry = app.state.registry
    board: BoardClient = app.state.board
    system_prompt: str = app.state.system_prompt

    manager = RunManager(run, board, registry, system_prompt)
    task = asyncio.create_task(
        manager.execute(),
        name=f"agent-run-{body.run_id}",
    )
    run.task = task
    # Don't keep finished tasks forever; clean them up after done.
    task.add_done_callback(lambda _t, rid=body.run_id: _schedule_cleanup(rid))

    return JSONResponse({"run_id": body.run_id, "status": "queued"}, status_code=202)


@app.post("/v1/runs/{run_id}/cancel", dependencies=[Depends(require_token)])
async def cancel_run(run_id: str) -> dict[str, object]:
    """Cancel a running run: the Go backend calls this when the operator
    hits POST /api/runs/:id/cancel. The RunManager's CancelledError path
    emits a terminal run_done with status=cancelled."""
    run = RUNS.get(run_id)
    if run is None:
        raise HTTPException(status_code=404, detail="run not found")
    if run.task is not None and not run.task.done():
        run.task.cancel()
        return {"run_id": run_id, "status": "cancelling"}
    return {"run_id": run_id, "status": run.status}


@app.post("/v1/runs/{run_id}/pause", dependencies=[Depends(require_token)])
async def pause_run(run_id: str) -> dict[str, object]:
    """Pause a running run: signal the run_manager loop to stop dispatching
    without a terminal run_done. The board is kept, so the run can be resumed
    via POST /api/runs/:id/continue (a fresh loop over the same board)."""
    run = RUNS.get(run_id)
    if run is None:
        raise HTTPException(status_code=404, detail="run not found")
    if run.pause_event is not None:
        run.pause_event.set()
    return {"run_id": run_id, "status": "pausing"}


@app.get("/v1/runs/{run_id}", dependencies=[Depends(require_token)])
async def get_run(run_id: str) -> dict[str, object]:
    run = RUNS.get(run_id)
    if run is None:
        raise HTTPException(status_code=404, detail="run not found")
    return {
        "run_id": run.run_id,
        "target": run.target,
        "objective": run.objective,
        "status": run.status,
        "summary": run.summary,
        "error": run.error,
        "created_at": run.created_at,
        "finished_at": run.finished_at,
    }


@app.get("/v1/runs/{run_id}/events", dependencies=[Depends(require_token)])
async def stream_events(run_id: str, request: Request) -> EventSourceResponse:
    if run_id not in RUNS:
        raise HTTPException(status_code=404, detail="run not found")
    queue = QUEUES[run_id]
    run = RUNS[run_id]

    async def event_gen() -> AsyncIterator[dict[str, object]]:
        # Opening event so the client knows the stream is live.
        yield {"event": "ready", "data": json.dumps({"run_id": run_id, "status": run.status})}
        while True:
            if await request.is_disconnected():
                log.info("SSE client for run %s disconnected", run_id)
                break
            try:
                evt = await asyncio.wait_for(queue.get(), timeout=15.0)
            except asyncio.TimeoutError:
                # Heartbeat keeps proxies from closing the connection.
                yield {"event": "ping", "data": json.dumps({"ts": _now()})}
                continue
            # Normalise: ensure `data` is a JSON string, not a Python dict,
            # otherwise sse-starlette renders the dict as a Python repr
            # (single-quoted) which Go's JSON parser silently drops.
            ev_type = evt.get("event", "")
            ev_data = evt.get("data")
            if not isinstance(ev_data, str):
                ev_data = json.dumps(ev_data, ensure_ascii=False)
            yield {"event": ev_type, "data": ev_data}
            if ev_type == "run_done":
                # Terminal -- close the stream.
                break

    return EventSourceResponse(event_gen())


@app.post("/v1/mcp/reload", dependencies=[Depends(require_token)])
async def reload_external_mcps(request: Request):
    """Re-fetch the user's enabled external MCP servers from the Go
    backend and reconnect. Lets the user add a new server in the
    Settings UI and pick it up without restarting the agent.

    The current MCP connection is closed only after the new clients
    are ready, so an in-flight call doesn't race the close.
    """
    registry = request.app.state.registry
    status = await registry.ext.load_from_backend()
    connected = sum(1 for s in status.values() if s.get("status") == "connected")
    return {"reloaded": len(status), "connected": connected, "status": status}


@app.get("/v1/mcp/status", dependencies=[Depends(require_token)])
async def external_mcp_status(request: Request):
    """Agent's view of its external MCP connections. The Settings UI
    polls this to show the "上次同步 · X 分钟前" indicator and the
    per-row green/gray dot. Read-only; use /v1/mcp/reload to refresh.
    """
    registry = request.app.state.registry
    return registry.ext.sync_info()


# --- Internals ----------------------------------------------------------


def _now() -> str:
    from datetime import datetime, timezone
    return datetime.now(timezone.utc).isoformat()


def _schedule_cleanup(run_id: str) -> None:
    """Drop the run from the in-memory registry a short while after it ends.

    Keeps the dict small for long-lived servers without losing the chance
    for late SSE clients to grab the final state via GET /v1/runs/{id}.
    """
    async def _drop_later() -> None:
        await asyncio.sleep(60.0)
        QUEUES.pop(run_id, None)
        RUNS.pop(run_id, None)
        log.debug("cleaned up run %s from registry", run_id)

    try:
        asyncio.get_running_loop().create_task(_drop_later())
    except RuntimeError:
        # No running loop (shouldn't happen in a server context) -- skip.
        pass


# --- Entrypoint (optional) ----------------------------------------------


def main() -> None:
    import uvicorn

    host = os.environ.get("DHUNTER_AGENT_HOST", "127.0.0.1")
    port = int(os.environ.get("DHUNTER_AGENT_PORT", "9100"))
    uvicorn.run(
        "core.server:app",
        host=host,
        port=port,
        log_level=os.environ.get("DHUNTER_AGENT_LOG_LEVEL", "info").lower(),
        reload=False,
    )


if __name__ == "__main__":  # pragma: no cover
    main()

"""Run manager: the blackboard dispatcher.

Replaces the old single-agent loop. For each run it:
  1. seeds the origin/goal facts on the board,
  2. dispatches explore workers for open intents (bounded concurrency),
  3. when no work is in flight, runs a reason step to propose new intents
     or to declare convergence (noop / complete),
  4. stops when converged, timed out, or cancelled.

Workers coordinate purely through the board (claim / conclude) — this is
the cairn-style stigmergy, implemented from scratch against dhunter's Go
board API.
"""

from __future__ import annotations

import asyncio
import logging
import os
import time
from typing import Any

from core.agent import OVERALL_TIMEOUT, AgentRun
from core.board import BoardClient
from core.reason import run_reason_step
from core.verifier import run_verifier
from core.worker import run_explore_worker
from tools.registry import ToolRegistry

log = logging.getLogger(__name__)

MAX_WORKERS = int(os.environ.get("DHUNTER_MAX_WORKERS", "3"))
MAX_REASON_ROUNDS = int(os.environ.get("DHUNTER_MAX_REASON_ROUNDS", "8"))
WORKER_SLICE_SECONDS = 2.0


class RunManager:
    def __init__(self, run: AgentRun, board: BoardClient, registry: ToolRegistry, system_prompt: str):
        self.run = run
        self.board = board
        self.registry = registry
        self.system_prompt = system_prompt
        self.workers: dict[asyncio.Task, dict[str, Any]] = {}  # task -> intent
        self.dispatching: set[str] = set()  # intent ids handed to a worker
        self.reason_rounds = 0
        self.started = time.monotonic()

    # --- lifecycle ------------------------------------------------------

    async def execute(self) -> None:
        self.run.status = "running"
        try:
            await asyncio.wait_for(self._loop(), timeout=OVERALL_TIMEOUT)
        except asyncio.TimeoutError:
            self.run.status = "failed"
            self.run.error = f"overall timeout ({int(OVERALL_TIMEOUT)}s) exceeded"
            log.warning("run %s timed out after %ss", self.run.run_id, int(OVERALL_TIMEOUT))
            await self.run.emit("run_done", {"status": "failed", "error": self.run.error})
        except asyncio.CancelledError:
            self.run.status = "cancelled"
            self.run.error = "run cancelled"
            log.warning("run %s cancelled", self.run.run_id)
            await self._cancel_workers()
            try:
                await self.run.emit("run_done", {"status": "cancelled", "error": self.run.error})
            except Exception:  # noqa: BLE001
                pass
            raise
        except Exception as e:  # noqa: BLE001
            self.run.status = "failed"
            self.run.error = f"{type(e).__name__}: {e}"
            log.exception("run %s failed", self.run.run_id)
            await self._cancel_workers()
            await self.run.emit("run_done", {"status": "failed", "error": self.run.error})
        finally:
            self.run.finished_at = _now_iso()

    # --- scheduler ------------------------------------------------------

    async def _loop(self) -> None:
        await self._seed_origin_goal()
        converged = False
        while time.monotonic() - self.started < OVERALL_TIMEOUT:
            graph = await self.board.graph(self.run.run_id)
            open_its = [i for i in (graph.get("intents") or []) if i.get("status") == "open"]

            # reap finished workers
            done = [t for t in self.workers if t.done()]
            for t in done:
                intent = self.workers.pop(t, None)
                if intent:
                    self.dispatching.discard(intent.get("id"))
                try:
                    t.result()  # surface exceptions (worker already logs)
                except asyncio.CancelledError:
                    pass
                except Exception:  # noqa: BLE001
                    pass

            # dispatch workers for open intents up to the concurrency cap
            free = MAX_WORKERS - len(self.workers)
            for intent in open_its:
                if free <= 0:
                    break
                iid = intent.get("id")
                if iid in self.dispatching:
                    continue
                self.dispatching.add(iid)
                worker_name = f"w{len(self.workers) + 1}"
                task = asyncio.create_task(
                    run_explore_worker(
                        self.run, self.board, self.registry, self.system_prompt,
                        intent, worker_name,
                    ),
                    name=f"explore-{self.run.run_id}-{iid}",
                )
                self.workers[task] = intent
                free -= 1
                log.info("run %s: dispatched worker %s for intent %s", self.run.run_id, worker_name, iid)

            # no work in flight and nothing open → reason once
            if not self.workers and not open_its:
                outcome = await self._reason_once(graph)
                if outcome in ("noop", "complete"):
                    converged = True
                    break

            # wait for progress (or a short tick)
            if self.workers:
                done, _ = await asyncio.wait(self.workers, timeout=WORKER_SLICE_SECONDS, return_when=asyncio.FIRST_COMPLETED)
                if not done:
                    continue  # workers still running; re-check the board
            else:
                await asyncio.sleep(1.0)

        if not converged and time.monotonic() - self.started >= OVERALL_TIMEOUT:
            # The loop's own timeout fired (rare — wait_for would usually
            # have caught it). Surface it as a failure.
            raise RuntimeError(f"overall timeout ({int(OVERALL_TIMEOUT)}s) exceeded")

        # Quality gate: independently re-judge every pending finding.
        await run_verifier(self.run, self.board, self.system_prompt)

        # final summary from the board
        summary = await self._build_summary()
        self.run.status = "success"
        self.run.summary = summary
        await self.run.emit("run_done", {"status": "success", "summary": summary})

    async def _seed_origin_goal(self) -> None:
        try:
            graph = await self.board.graph(self.run.run_id)
            existing = {f.get("source") for f in (graph.get("facts") or [])}
            if "origin" not in existing:
                await self.board.create_fact(self.run.run_id, f"target: {self.run.target}", source="origin")
            if "goal" not in existing:
                await self.board.create_fact(self.run.run_id, f"objective: {self.run.objective}", source="goal")
        except Exception as e:  # noqa: BLE001
            # Non-fatal: the board may not be reachable at run start; the
            # worker's graph fetch will surface a real error later.
            log.warning("run %s: failed to seed origin/goal facts: %s", self.run.run_id, e)

    async def _reason_once(self, graph: dict[str, Any]) -> str:
        self.reason_rounds += 1
        if self.reason_rounds > MAX_REASON_ROUNDS:
            log.info("run %s: reason round cap (%s) reached, converging", self.run.run_id, MAX_REASON_ROUNDS)
            return "noop"
        log.info("run %s: reason round %s", self.run.run_id, self.reason_rounds)
        kind, payload = await run_reason_step(self.run, self.board, self.system_prompt)
        if kind == "intents":
            log.info("run %s: reason proposed %s new intents", self.run.run_id, payload)
            return "intents"
        if kind == "complete":
            log.info("run %s: reason declared complete", self.run.run_id)
            if payload:
                self.run.summary = str(payload)
            return "complete"
        log.info("run %s: reason noop — converging", self.run.run_id)
        return "noop"

    async def _build_summary(self) -> str:
        # A reason "complete" already set a meaningful summary — keep it.
        if self.run.summary:
            return self.run.summary
        try:
            graph = await self.board.graph(self.run.run_id)
        except Exception:  # noqa: BLE001
            return self.run.summary or "run finished"
        facts = [f for f in (graph.get("facts") or []) if f.get("source") not in ("origin", "goal")]
        intents = graph.get("intents") or []
        concluded = [i for i in intents if i.get("status") == "concluded"]
        lines = [f"Converged after exploring {len(concluded)} intents ({len(facts)} facts recorded)."]
        for f in facts[:20]:
            desc = (f.get("description") or "").strip().replace("\n", " ")
            if len(desc) > 200:
                desc = desc[:200] + "…"
            lines.append(f"- {desc}")
        return "\n".join(lines)

    async def _cancel_workers(self) -> None:
        for t in list(self.workers):
            t.cancel()
        if self.workers:
            await asyncio.wait(self.workers, timeout=5.0)


def _now_iso() -> str:
    from datetime import datetime, timezone
    return datetime.now(timezone.utc).isoformat()

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
# A reason "noop" is only trustworthy after real exploration happened. If the
# board is still basically empty (no facts beyond origin/goal), a noop is
# almost certainly the LLM misjudging an empty board — we force a bootstrap
# recon intent instead of converging an empty run.
MIN_EXPLORED_FACTS = 3
# Findings wait in "pending" until the verifier re-judges them. We run the
# verifier periodically DURING the run (not just at convergence) so confirmed
# / dismissed results surface while the agent is still digging.
VERIFY_INTERVAL = float(os.environ.get("DHUNTER_VERIFY_INTERVAL", "60"))


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
        self.run_auth: dict[str, Any] | None = None
        self.max_run_tokens = 0
        self.llm_config: dict[str, Any] | None = None
        self._last_verify = time.monotonic()

    # --- lifecycle ------------------------------------------------------

    async def execute(self) -> None:
        self.run.status = "running"
        self.run.summary = ""
        self.run.error = None
        try:
            await asyncio.wait_for(self._loop(), timeout=OVERALL_TIMEOUT)
        except asyncio.TimeoutError:
            # Reached the time-based run limit — stop gracefully, not as a failure.
            mins = int(OVERALL_TIMEOUT / 60)
            self.run.status = "success"
            self.run.summary = f"达到运行时间上限（{mins} 分钟），已自动停止。可点击「继续深入」从当前进度续跑。"
            log.warning("run %s reached %s-min time limit", self.run.run_id, mins)
            await self.run.emit("run_done", {"status": "success", "summary": self.run.summary})
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
            self.registry.clear_run_auth(self.run.run_id)

    # --- scheduler ------------------------------------------------------

    async def _loop(self) -> None:
        await self._seed_origin_goal()
        await self._load_auth()
        await self._load_llm_config()
        await self._load_budget()
        await self._load_knowledge()
        # Make sure the MCP toolbelt is loaded (retries after a startup race).
        await self.registry.ensure_mcp()
        if self.registry.mcp_status().get("tool_count"):
            log.info("run %s: MCP toolbelt loaded (%d tools)", self.run.run_id, self.registry.mcp_status()["tool_count"])
        else:
            log.warning("run %s: MCP not loaded yet, starting with fallback tools", self.run.run_id)
        converged = False
        while time.monotonic() - self.started < OVERALL_TIMEOUT:
            graph = await self.board.graph(self.run.run_id)
            # time-based run limit is the only stop mechanism (see OVERALL_TIMEOUT)
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
                        intent, worker_name, auth_context=self.run_auth, llm_config=self.llm_config,
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

            # Retry MCP connection periodically (the sidecar may come up later).
            await self.registry.ensure_mcp()

            # Verify pending findings periodically so confirmed results
            # surface while the run is still digging.
            if time.monotonic() - self._last_verify > VERIFY_INTERVAL:
                self._last_verify = time.monotonic()
                await run_verifier(self.run, self.board, self.system_prompt, llm_config=self.llm_config)

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
        # Run a second pass if a worker landed a finding in the last instant
        # (a race that otherwise leaves a fresh finding stuck in "pending").
        await run_verifier(self.run, self.board, self.system_prompt, llm_config=self.llm_config)
        for _ in range(2):
            try:
                still = [v for v in await self.board.list_vulnerabilities(self.run.run_id) if v.get("status") in ("pending", "open")]
            except Exception:  # noqa: BLE001
                break
            if not still:
                break
            await run_verifier(self.run, self.board, self.system_prompt, llm_config=self.llm_config)

        # Learn reusable intel for future runs on this host family.
        try:
            await self._learn_knowledge(graph)
        except Exception:  # noqa: BLE001
            pass

        # final summary from the board
        summary = await self._build_summary()
        self.run.status = "success"
        self.run.summary = summary
        await self.run.emit("run_done", {"status": "success", "summary": summary})

    async def _load_llm_config(self) -> None:
        """Use the LLM config saved in the platform (ccswitch-style import)
        if one exists. Stored per-run on self.llm_config and threaded through
        the call chain — we never mutate os.environ so concurrent runs with
        different providers don't clobber each other."""
        try:
            resp = await self.board.get_llm_config()
        except Exception as e:  # noqa: BLE001
            log.warning("run %s: no saved llm config: %s", self.run.run_id, e)
            return
        if not resp:
            return
        model = resp.get("model") or ""
        base_url = resp.get("base_url") or ""
        api_key = resp.get("api_key") or ""
        if model and base_url and api_key:
            self.llm_config = {
                "model": model,
                "base_url": base_url,
                "api_key": api_key,
                "provider": resp.get("provider") or "",
                "max_tokens": int(resp.get("max_tokens") or 8192),
            }
            log.info("run %s: using saved LLM config: %s @ %s", self.run.run_id, model, base_url)

    async def _load_budget(self) -> None:
        """Load the per-run token budget red line (0 = unlimited)."""
        self.max_run_tokens = 0
        try:
            resp = await self.board.get_budget()
            self.max_run_tokens = int(resp.get("max_run_tokens") or 0)
            if self.max_run_tokens > 0:
                log.info("run %s: token budget red line = %s", self.run.run_id, self.max_run_tokens)
        except Exception:  # noqa: BLE001
            pass

    def _host_family(self) -> str:
        """Root-domain family for cross-target knowledge reuse."""
        target = self.run.target.lower().strip()
        from urllib.parse import urlparse
        host = urlparse(target).hostname or target
        parts = host.split(".")
        if len(parts) >= 2:
            return ".".join(parts[-2:])
        return host

    async def _load_knowledge(self) -> None:
        """Inject reusable intel from prior runs on this host family into the
        system prompt so the agent starts with known endpoints/creds/fingerprints
        instead of rediscovering them."""
        try:
            items = await self.board.get_knowledge(self._host_family())
        except Exception:  # noqa: BLE001
            return
        if not items:
            return
        lines = ["\n# 跨目标先验知识（本域名族历史扫描结论，直接复用，不要重复发现）"]
        for it in items[:30]:
            v = (it.get("value") or "").strip().replace("\n", " ")
            if len(v) > 180:
                v = v[:180] + "…"
            lines.append(f"- [{it.get('kind')}] {v}")
        self.system_prompt += "\n".join(lines) + "\n"
        log.info("run %s: loaded %d knowledge item(s) for %s", self.run.run_id, len(items), self._host_family())

    async def _learn_knowledge(self, graph: dict[str, Any]) -> None:
        """After convergence, extract reusable facts (endpoints, creds,
        fingerprints) and store them for future runs on this host family."""
        family = self._host_family()
        import re as _re
        added = 0
        for f in (graph.get("facts") or []):
            desc = (f.get("description") or "").strip()
            if not desc:
                continue
            kind = None
            low = desc.lower()
            if "password" in low or "credential" in low or "token:" in low or "api key" in low or "账号" in low or "密码" in low:
                kind = "credential"
            elif _re.match(r"https?://", desc) or "/api/" in desc or ".kuaishou.com" in desc:
                kind = "endpoint"
            elif "fingerprint" in low or "vue" in low or "nginx" in low or "spring" in low or "版本" in low:
                kind = "fingerprint"
            if not kind:
                continue
            await self.board.add_knowledge(family, kind, desc)
            added += 1
        if added:
            log.info("run %s: learned %d knowledge item(s) for %s", self.run.run_id, added, family)

    async def _budget_exhausted(self, graph: dict[str, Any]) -> bool:
        """True when cumulative run tokens exceed the configured budget."""
        if not self.max_run_tokens or self.max_run_tokens <= 0:
            return False
        r = graph.get("run") or {}
        # Cache reads are cheap (prompt-cache reuse); count real spend only.
        used = int(r.get("input_tokens") or 0) + int(r.get("output_tokens") or 0)
        return used >= self.max_run_tokens

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

    async def _load_auth(self) -> None:
        """Fetch the target's stored session (if any) and hand it to the
        registry so http_request auto-injects cookies/headers for this run.
        Also records it for the worker prompt."""
        self.run_auth: dict[str, Any] | None = None
        try:
            graph = await self.board.graph(self.run.run_id)
            target_id = (graph.get("run") or {}).get("target_id")
            if not target_id:
                return
            target = await self.board.get_target(target_id)
            raw = target.get("auth_context") or ""
            if not raw or raw == "{}":
                return
            import json as _json
            parsed = _json.loads(raw)
            host = (target.get("normalized") or target.get("value") or "")
            host = host.rstrip("/")
            if host.startswith(("http://", "https://")):
                from urllib.parse import urlparse
                host = urlparse(host).hostname or host
            # Normalize auth into {host, active, accounts:{a,b}} for A/B IDOR.
            accounts = {}
            if parsed.get("account_a"):
                accounts["a"] = {k: parsed["account_a"].get(k) for k in ("username", "password", "login_url", "cookies", "headers", "note")}
            if parsed.get("account_b"):
                accounts["b"] = {k: parsed["account_b"].get(k) for k in ("username", "password", "login_url", "cookies", "headers", "note")}
            if not accounts:
                # legacy single-account: cookies/headers at top level -> account a
                accounts["a"] = {"cookies": parsed.get("cookies"), "headers": parsed.get("headers"), "note": parsed.get("note")}
            self.run_auth = {"host": host, "active": "a", "accounts": accounts}
            self.registry.set_run_auth(self.run.run_id, self.run_auth)
            n = sum(1 for a in accounts.values() if (a.get("cookies") or (a.get("username") and a.get("password"))))
            if n:
                log.info("run %s: %d account session(s) loaded for %s", self.run.run_id, n, host)

            # Custom guardrails: inject the target's red lines into the system
            # prompt so EVERY reason/explore turn follows them.
            red_lines = (target.get("red_lines") or "").strip()
            if red_lines:
                self.system_prompt = self.system_prompt + "\n\n# Red lines (MUST always follow)\n" + red_lines
                log.info("run %s: %d red line(s) injected", self.run.run_id, len(red_lines.splitlines()))
        except Exception as e:  # noqa: BLE001
            log.warning("run %s: failed to load auth context: %s", self.run.run_id, e)

    async def _reason_once(self, graph: dict[str, Any]) -> str:
        self.reason_rounds += 1
        if self.reason_rounds > MAX_REASON_ROUNDS:
            log.info("run %s: reason round cap (%s) reached, converging", self.run.run_id, MAX_REASON_ROUNDS)
            return "noop"
        log.info("run %s: reason round %s", self.run.run_id, self.reason_rounds)
        kind, payload = await run_reason_step(self.run, self.board, self.system_prompt, llm_config=self.llm_config)
        if kind == "intents":
            log.info("run %s: reason proposed %s new intents", self.run.run_id, payload)
            return "intents"
        if kind == "complete":
            log.info("run %s: reason declared complete", self.run.run_id)
            if payload:
                self.run.summary = str(payload)
            return "complete"
        # noop — but guard against premature convergence on an empty board.
        explored = [f for f in (graph.get("facts") or []) if f.get("source") not in ("origin", "goal")]
        attempted = [i for i in (graph.get("intents") or []) if i.get("status") in ("concluded", "failed")]
        if not explored and not attempted:
            # Nothing has been explored at all — this noop is the LLM
            # misjudging an empty board. Force a bootstrap recon intent.
            log.warning("run %s: reason noop on empty board — forcing bootstrap recon", self.run.run_id)
            await self._inject_bootstrap_recon()
            return "intents"
        log.info("run %s: reason noop after %s fact(s) / %s intent(s) attempted — converging", self.run.run_id, len(explored), len(attempted))
        return "noop"

    async def _inject_bootstrap_recon(self) -> None:
        """Force an initial reconnaissance intent so a fresh run never
        converges empty (LLMs sometimes noop on an empty board)."""
        target = self.run.target
        descs = [
            f"Initial reconnaissance: enumerate the attack surface of {target} "
            "(subdomains / endpoints / JS assets / technologies), then record what is live.",
        ]
        for desc in descs:
            await self.board.create_intent(self.run.run_id, [], desc, creator="bootstrap")

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

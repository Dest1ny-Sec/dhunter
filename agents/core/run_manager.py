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
from core.logconfig import clear_run_id, set_run_id
from core.reason import run_reason_step
from core.verifier import run_verifier
from core.worker import run_explore_worker
from tools.registry import ToolRegistry

log = logging.getLogger(__name__)

# Default concurrency. 2 workers instead of 3: each worker runs its own
# full tool-loop context, so 3 means ~3x the context spend in flight.
# 2 keeps parallel exploration while cutting token burn by a third
# (speed is slightly lower — acceptable trade, quality unchanged).
MAX_WORKERS = int(os.environ.get("DHUNTER_MAX_WORKERS", "2"))
MAX_REASON_ROUNDS = int(os.environ.get("DHUNTER_MAX_REASON_ROUNDS", "8"))
WORKER_SLICE_SECONDS = 2.0
# A reason "noop" is only trustworthy after real exploration happened. If the
# board is still basically empty (no facts beyond origin/goal), a noop is
# almost certainly the LLM misjudging an empty board — we force a bootstrap
# recon intent instead of converging an empty run.
MIN_EXPLORED_FACTS = 3
# Findings wait in "pending" until the verifier re-judges them. We verify
# DURING the run (not just at convergence) so confirmed/dismissed results
# surface while the agent is still digging — but ONLY when a worker just
# landed a finding (reaped_any), never on a fixed timer: a periodic LLM
# judge call would burn tokens even when nothing new arrived.

# Two-level public suffixes under which the registrable domain is the THIRD
# label from the right (ecust.edu.cn, qq.com.cn, ...). Without this, a
# university target would collapse into the shared "edu.cn" bucket and every
# .edu.cn run would inherit each other's intel.
_CN_PSL = ("com.cn", "edu.cn", "gov.cn", "org.cn", "net.cn", "ac.cn", "mil.cn")


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
        # Fact ids the planner has already seen — enables incremental reason
        # turns (only NEW facts are re-sent, see _reason_once).
        self._known_fact_ids: set[str] = set()
        self.run_auth: dict[str, Any] | None = None
        self.max_run_tokens = 0
        self.llm_config: dict[str, Any] | None = None
        # Per-project worker cap (target attributes override the env default).
        self.max_workers = MAX_WORKERS
        # Set when the operator pauses the run — the loop stops without
        # emitting a terminal run_done so the board is preserved for resume.
        self.paused = False

    # --- lifecycle ------------------------------------------------------

    async def execute(self) -> None:
        self.run.status = "running"
        self.run.summary = ""
        self.run.error = None
        # Tag every log line on this run's task tree with [run_id] so the full
        # conversation can be replayed from logs/agent.log.
        set_run_id(self.run.run_id)
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
            clear_run_id()

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
            # Operator paused this run → stop dispatching, keep the board.
            if self.run.pause_event is not None and self.run.pause_event.is_set():
                self.paused = True
                break

            graph = await self.board.graph(self.run.run_id)

            # Token budget red line: if the run burned its configured budget
            # (input+output, cache reads excluded), stop gracefully — same
            # shape as the time-based run limit so the board survives for a
            # later "continue".
            if await self._budget_exhausted(graph):
                self.run.summary = (
                    f"达到 token 预算上限（{self.max_run_tokens} tokens），已自动停止。"
                    "可点击「继续深入」从当前进度续跑。"
                )
                log.info("run %s: token budget (%s) exhausted, stopping", self.run.run_id, self.max_run_tokens)
                break

            # Stop mechanisms: per-run token budget (above) and the
            # time-based run limit (see OVERALL_TIMEOUT).
            open_its = [i for i in (graph.get("intents") or []) if i.get("status") == "open"]

            # reap finished workers
            done = [t for t in self.workers if t.done()]
            reaped_any = bool(done)
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
            free = self.max_workers - len(self.workers)
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

            # Verify pending findings — promptly when a worker just landed a
            # finding (出漏洞优先验证). No fixed timer: a worker finishing
            # (reaped) is the only trigger, so no tokens are spent when
            # nothing new arrived. The convergence tail re-checks leftovers.
            if reaped_any:
                await run_verifier(self.run, self.board, self.system_prompt, llm_config=self.llm_config,
                                   auth_context=self.registry.get_run_auth(self.run.run_id))

            # wait for progress (or a short tick)
            if self.workers:
                done, _ = await asyncio.wait(self.workers, timeout=WORKER_SLICE_SECONDS, return_when=asyncio.FIRST_COMPLETED)
                if not done:
                    continue  # workers still running; re-check the board
            else:
                await asyncio.sleep(1.0)

        if self.paused:
            # Paused by the operator: stop cleanly WITHOUT a terminal run_done.
            # The Go backend has already set the run status to "paused" and the
            # board stays intact, so the run can be resumed via /continue.
            await self._cancel_workers()
            log.info("run %s paused (board kept, resume via continue)", self.run.run_id)
            return

        if not converged and time.monotonic() - self.started >= OVERALL_TIMEOUT:
            # The loop's own timeout fired (rare — wait_for would usually
            # have caught it). Surface it as a failure.
            raise RuntimeError(f"overall timeout ({int(OVERALL_TIMEOUT)}s) exceeded")

        # Quality gate: independently re-judge every pending finding.
        # Run a second pass if a worker landed a finding in the last instant
        # (a race that otherwise leaves a fresh finding stuck in "pending").
        await run_verifier(self.run, self.board, self.system_prompt, llm_config=self.llm_config,
                           auth_context=self.registry.get_run_auth(self.run.run_id))
        for _ in range(2):
            try:
                still = [v for v in await self.board.list_vulnerabilities(self.run.run_id) if v.get("status") in ("pending", "open")]
            except Exception:  # noqa: BLE001
                break
            if not still:
                break
            await run_verifier(self.run, self.board, self.system_prompt, llm_config=self.llm_config,
                               auth_context=self.registry.get_run_auth(self.run.run_id))

        # Findings still pending after all verifier passes mean the SRC gate
        # silently missed them (LLM judge call failed, backend PATCH failed,
        # or a race). Surface it instead of letting it hide.
        try:
            left = [v for v in await self.board.list_vulnerabilities(self.run.run_id) if v.get("status") in ("pending", "open")]
        except Exception:  # noqa: BLE001
            left = []
        if left:
            log.warning(
                "run %s: %d finding(s) still pending/open after verifier (gate likely missed them): %s",
                self.run.run_id, len(left), [(v.get("title") or "")[:60] for v in left][:5],
            )

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
        if len(parts) >= 3 and ".".join(parts[-2:]) in _CN_PSL:
            return ".".join(parts[-3:])
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

            # Per-project worker cap: target attributes override the env default.
            try:
                import json as _j
                mw = int(_j.loads(target.get("attributes") or "{}").get("max_workers") or 0)
                if mw > 0:
                    self.max_workers = min(mw, 16)
                    log.info("run %s: per-target max_workers=%d", self.run.run_id, self.max_workers)
            except Exception:  # noqa: BLE001
                pass

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
        # Incremental planning: the first reason turn sees ALL facts; later
        # turns only see NEW facts since the last reason step (saves tokens
        # on the re-sent summary). The known set is updated from the same
        # graph snapshot we reason over.
        kind, payload = await run_reason_step(
            self.run, self.board, self.system_prompt, llm_config=self.llm_config,
            known_fact_ids=self._known_fact_ids,
        )
        for f in (graph.get("facts") or []):
            if f.get("id"):
                self._known_fact_ids.add(f["id"])
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

"""Vulnerability verifier: the quality gate behind "real vulnerabilities".

Every write_finding lands as status=pending. After the run converges, the
verifier asks the LLM to independently re-judge each pending finding from
its evidence (a second model pass, not the worker that found it). Findings
that survive are confirmed; weak or unreproducible ones are dismissed.

This is intentionally cheap: one tool-less LLM call per finding, and the
whole pass can be disabled with DHUNTER_VERIFY=0.
"""

from __future__ import annotations

import logging
import os
from typing import Any

from core.agent import AgentRun, call_llm_text, parse_json_object
from core.board import BoardClient

log = logging.getLogger(__name__)

VERIFY_ENABLED = os.environ.get("DHUNTER_VERIFY", "1") != "0"

VERIFY_PROMPT = """# Task
You are the verification reviewer for a penetration testing platform. A worker
submitted the finding below. Your job: decide whether it is a REAL, confirmed,
exploitable vulnerability backed by the evidence — or should be dismissed.

Do not re-run any tool. Judge from the evidence text alone. Be skeptical: a
status code alone is not proof of a vuln; look for a concrete exploit signal
(payload returned, data exfiltration, auth bypass demonstrated, etc.).

## Finding
Title: {title}
Severity: {severity}
Target: {target}

## Evidence
{evidence}

# Output
Return ONLY one JSON object:
{{"confirm": true, "reason": "<one sentence>"}}
or
{{"confirm": false, "reason": "<one sentence>"}}
"""


async def run_verifier(run: AgentRun, board: BoardClient, system_prompt: str) -> None:
    if not VERIFY_ENABLED:
        return
    try:
        vulns = await board.list_vulnerabilities(run.run_id)
    except Exception as e:  # noqa: BLE001
        log.warning("verifier: cannot list vulns for run %s: %s", run.run_id, e)
        return

    pending = [v for v in vulns if v.get("status") in ("pending", "open")]
    if not pending:
        return
    log.info("verifier: reviewing %s pending finding(s) for run %s", len(pending), run.run_id)

    confirmed, dismissed = 0, 0
    for v in pending:
        verdict = await _judge(run, system_prompt, v)
        try:
            await board.set_vuln_status(v["id"], "confirmed" if verdict else "dismissed")
        except Exception as e:  # noqa: BLE001
            log.warning("verifier: failed to update %s: %s", v.get("id"), e)
            continue
        if verdict:
            confirmed += 1
        else:
            dismissed += 1

    if confirmed or dismissed:
        log.info("verifier: run %s -> %s confirmed, %s dismissed", run.run_id, confirmed, dismissed)


async def _judge(run: AgentRun, system_prompt: str, v: dict[str, Any]) -> bool:
    user_content = VERIFY_PROMPT.format(
        title=(v.get("title") or "")[:300],
        severity=(v.get("severity") or "info"),
        target=(v.get("target") or ""),
        evidence=(v.get("evidence") or "")[:4000],
    )
    try:
        text = await call_llm_text(run, system=system_prompt, user_content=user_content)
    except Exception as e:  # noqa: BLE001
        log.warning("verifier: judge call failed for %s: %s", v.get("id"), e)
        return False
    parsed = parse_json_object(text)
    return parsed.get("confirm") in (True, "true", "True")

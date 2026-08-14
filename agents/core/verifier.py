"""Vulnerability verifier — the SRC acceptance gate.

A worker's write_finding lands as status=pending. After the run converges,
this pass re-judges every pending finding against REAL bug-bounty standards
so the platform only reports findings a white-hat would actually submit:

  * "Phenomenon is not a vulnerability; a vulnerability is a RESULT."
    CORS reflect, missing HSTS, version disclosure, endpoint existence,
    internal-hostname leaks etc. are config noise — dismissed unless they
    demonstrably lead to data access / auth bypass / code execution.
  * Complete evidence required (request -> response -> demonstrated impact).
  * Severity must reflect SRC norms: a config issue is at most LOW.

Disabled entirely with DHUNTER_VERIFY=0.
"""

from __future__ import annotations

import logging
import os
from typing import Any

from core.agent import AgentRun, call_llm_text, parse_json_object
from core.board import BoardClient

log = logging.getLogger(__name__)

VERIFY_ENABLED = os.environ.get("DHUNTER_VERIFY", "1") != "0"

# Phenomena that SRC programs typically reject outright. Used as a hard
# pre-filter in addition to the LLM's judgment, so a weak LLM can't sneak
# scanner-noise through.
_REJECT_HINTS = (
    "cors", "access-control-allow-origin", "missing security header",
    "security header", "hsts", "strict-transport-security", "x-frame-options",
    "x-content-type-options", "tls", "ssl", "https", "http accepted",
    "plain http", "version disclosure", "version leak", "server version",
    "source map", "sourcemap", "self-xss", "open redirect", "rate limit",
    "rate limiting", "directory listing", "directory index", "favicon",
    "internal hostname", "internal ip", "ip address disclosure",
    "information disclosure", "stack trace", "error page",
)
# If a title matches a reject hint AND the evidence shows no actual exploit
# (no data pulled, no bypass, no execution), it's noise.
_EXPLOIT_MARKERS = (
    "accessed", "extracted", "exfiltrated", "read", "bypass", "bypassed",
    "executed", "command", "shell", "token", "jwt", "session", "data of",
    "other user", "admin", "200 ok with", "returned the", "leaked the",
    "contains the", "disclosed the",
)

VERIFY_PROMPT = """# Task
You are the SRC (bug bounty) triage reviewer for a penetration testing platform.
A worker submitted the finding below. Judge it against REAL bug-bounty acceptance
standards. You are skeptical and experienced: most automated findings are noise.

## Ground rules (memorize)
A finding is ONLY confirmable when it demonstrates an actual attack RESULT with
impact, backed by evidence:
  - unauthorized access to another user's or protected data (not "returns 200")
  - authentication / authorization bypass that grants access
  - injection that retrieves data or executes code
  - any concrete, reproducible exploit whose PoC shows the harm

DISMISS findings that are phenomena / config noise, even if technically true:
  - CORS misconfiguration (unless it demonstrably enables a working cross-origin
    data theft — a reflected Access-Control-Allow-Origin alone is NOT a vuln)
  - missing / weak security headers, HSTS, TLS or "plain HTTP accepted" notes
  - version / fingerprint / stack / framework disclosure
  - endpoint existence, API path enumeration, or "graphql is reachable"
  - internal hostname / IP / path disclosure with no demonstrated impact
  - open redirect (unless chained to something harmful)
  - rate limiting, self-XSS, directory listing, SourceMap exposure

Severity must follow SRC norms: a config issue is at most LOW. Only a finding
with proven data access / auth bypass / code execution may be HIGH or CRITICAL.

## Finding
Title: {title}
Reported severity: {severity}
Target: {target}

## Evidence (what the worker wrote)
{evidence}

## Reproduction steps (what the worker claims reproduces it)
{reproduction}

A finding is only confirmable if it comes with reproducible steps that
demonstrate the impact (a numbered curl + the expected result). A finding with
no reproduction / no demonstrated impact is dismissed.

## Output — ONE JSON object only
{{"confirm": true, "reason": "<one sentence>", "severity": "<critical|high|medium|low|info>"}}
or
{{"confirm": false, "reason": "<one sentence why it's noise / not proven>"}}
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
    log.info("verifier: SRC-gating %s pending finding(s) for run %s", len(pending), run.run_id)

    confirmed, dismissed = 0, 0
    for v in pending:
        verdict, reason, severity = await _judge(run, system_prompt, v)
        try:
            if verdict:
                # Also correct inflated severities from the LLM.
                final_sev = _cap_severity(v.get("severity") or "medium", v.get("title") or "", severity)
                await board.set_vuln_severity(v["id"], final_sev)
                await board.set_vuln_status(v["id"], "confirmed")
                confirmed += 1
            else:
                await board.set_vuln_status(v["id"], "dismissed")
                dismissed += 1
            log.info("verifier: %s -> %s (%s)", (v.get("title") or "")[:50], "confirmed" if verdict else "dismissed", reason)
        except Exception as e:  # noqa: BLE001
            log.warning("verifier: failed to update %s: %s", v.get("id"), e)

    if confirmed or dismissed:
        log.info("verifier: run %s -> %s confirmed, %s dismissed", run.run_id, confirmed, dismissed)


async def _judge(run: AgentRun, system_prompt: str, v: dict[str, Any]) -> tuple[bool, str, str]:
    """Returns (confirmed, reason, llm_severity)."""
    title = (v.get("title") or "")
    evidence = (v.get("evidence") or "")

    # Hard pre-filter: obvious config noise with no exploit marker.
    low = title.lower()
    is_noise = any(h in low for h in _REJECT_HINTS)
    has_exploit = any(m in evidence.lower() or m in low for m in _EXPLOIT_MARKERS)
    if is_noise and not has_exploit:
        return False, "config noise / phenomenon, no demonstrated exploit", "low"

    user_content = VERIFY_PROMPT.format(
        title=title[:300],
        severity=(v.get("severity") or "info"),
        target=(v.get("target") or ""),
        evidence=evidence[:4000],
        reproduction=(v.get("reproduction") or "")[:3000],
    )
    try:
        text = await call_llm_text(run, system=system_prompt, user_content=user_content)
    except Exception as e:  # noqa: BLE001
        log.warning("verifier: judge call failed for %s: %s", v.get("id"), e)
        return False, "verifier LLM call failed", "info"
    parsed = parse_json_object(text)
    confirm = parsed.get("confirm") in (True, "true", "True")
    reason = str(parsed.get("reason") or "")
    severity = str(parsed.get("severity") or "").lower()
    if severity not in ("critical", "high", "medium", "low", "info"):
        severity = ""
    return confirm, reason, severity


def _cap_severity(reported: str, title: str, llm_sev: str) -> str:
    """SRC severity calibration: config-ish findings can't be high/critical."""
    low = title.lower()
    configy = any(h in low for h in _REJECT_HINTS)
    chosen = llm_sev or reported
    if configy and chosen in ("critical", "high"):
        return "low"
    return chosen

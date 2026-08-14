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
# Phenomena that are NEVER a vuln on their own (dismiss outright even with
# an exploit marker). NOTE: "internal hostname exposed" / "internal system
# reachable" is deliberately NOT here — an internal system reachable from the
# public internet CAN be a reportable SRC finding IF the tester demonstrates
# access to data or unauthenticated functionality. Those go to the LLM for
# an evidence-based judgment instead of being auto-dismissed.
_REJECT_HINTS = (
    "cors", "access-control-allow-origin", "missing security header",
    "security header", "hsts", "strict-transport-security", "x-frame-options",
    "x-content-type-options", "tls", "ssl", "http accepted",
    "plain http", "version disclosure", "version leak", "server version",
    "source map", "sourcemap", "self-xss", "open redirect", "rate limit",
    "rate limiting", "directory listing", "directory index", "favicon",
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

DISMISS phenomena / config noise, even if technically true:
  - CORS misconfiguration (unless it demonstrably enables a working cross-origin
    data theft — a reflected Access-Control-Allow-Origin alone is NOT a vuln)
  - missing / weak security headers, HSTS, TLS or "plain HTTP accepted" notes
  - version / fingerprint / stack / framework disclosure
  - endpoint existence, API path enumeration, or "graphql is reachable"
  - open redirect (unless chained to something harmful)
  - rate limiting, self-XSS, directory listing, SourceMap exposure

CRITICAL distinction for internal systems:
  - "Internal system reachable from the public internet" is NOT automatically
    noise. It is CONFIRMABLE when the tester DEMONSTRATED access to data or
    unauthenticated functionality (e.g. "browsed the admin panel and read user
    records", "queried the internal API and got data"). Report it.
  - An error page / banner / header that merely LEAKS an internal hostname or
    username, with no data access, is info-level → dismiss.

Severity follows SRC norms: a config issue is at most LOW. Only a finding with
proven data access / auth bypass / code execution may be HIGH or CRITICAL.
An internal system you actually accessed data through is typically HIGH.

## Finding
Title: {title}
Reported severity: {severity}
Target: {target}

## Evidence (what the worker wrote)
{evidence}

## Reproduction steps (what the worker claims reproduces it)
{reproduction}

## Mechanical replay (platform actually re-requested the endpoint)
{replay_note}
If the replay shows the endpoint is DOWN (connection failed), the finding is likely
stale — dismiss. If it returns a status, weigh it against the claimed impact.

A finding is only confirmable if it comes with reproducible steps that
demonstrate the impact (a numbered curl + the expected result). A finding with
no reproduction / no demonstrated impact is dismissed.

## Output — ONE JSON object only
{{"confirm": true, "reason": "<one sentence>", "severity": "<critical|high|medium|low|info>"}}
or
{{"confirm": false, "reason": "<one sentence why it's noise / not proven>"}}
"""


async def run_verifier(run: AgentRun, board: BoardClient, system_prompt: str, llm_config: dict[str, Any] | None = None) -> None:
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
        verdict, reason, severity = await _judge(run, system_prompt, v, llm_config=llm_config)
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


async def _replay(v: dict[str, Any]) -> dict[str, Any]:
    """Mechanically re-request the finding's target to verify it is still
    reachable and returns a plausible status. This grounds the LLM judgment
    in reality instead of trusting the worker's text alone."""
    import re as _re
    import httpx as _httpx
    url = (v.get("target") or "").strip()
    method = "GET"
    # try to extract method + URL from a curl line in the reproduction
    repro = (v.get("reproduction") or "") + "\n" + (v.get("evidence") or "")
    for m in _re.finditer(r"curl(?: -X)?\s+(?:\S+\s+)*(?:'(https?://[^' ]+)'|\"(https?://[^\" ]+)\")", repro):
        url = m.group(1) or m.group(2)
        break
    m = _re.search(r"curl(?: -X)?\s+([A-Z]+)\s", repro)
    if m and m.group(1) in ("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"):
        method = m.group(1)
    if not url.startswith("http"):
        return {"ok": False, "error": "no url to replay"}
    try:
        async with _httpx.AsyncClient(timeout=10.0, trust_env=False, follow_redirects=True) as client:
            resp = await client.request(method, url)
        return {"ok": True, "status": resp.status_code, "url": url, "method": method}
    except Exception as e:  # noqa: BLE001
        return {"ok": False, "error": f"{type(e).__name__}: {e}"}


async def _judge(run: AgentRun, system_prompt: str, v: dict[str, Any], llm_config: dict[str, Any] | None = None) -> tuple[bool, str, str]:
    """Returns (confirmed, reason, llm_severity)."""
    title = (v.get("title") or "")
    evidence = (v.get("evidence") or "")

    # Hard pre-filter: obvious config noise with no exploit marker.
    low = title.lower()
    is_noise = any(h in low for h in _REJECT_HINTS)
    has_exploit = any(m in evidence.lower() or m in low for m in _EXPLOIT_MARKERS)
    if is_noise and not has_exploit:
        return False, "config noise / phenomenon, no demonstrated exploit", "low"

    # Mechanical replay: re-request the endpoint to ground the judgment.
    replay = await _replay(v)
    replay_note = ""
    if replay.get("ok"):
        replay_note = f"机械重放: {replay.get('method')} {replay.get('url')} -> HTTP {replay.get('status')}"
    else:
        replay_note = f"机械重放失败: {replay.get('error', '')}"
    user_content = VERIFY_PROMPT.format(
        title=title[:300],
        severity=(v.get("severity") or "info"),
        target=(v.get("target") or ""),
        evidence=evidence[:4000],
        reproduction=(v.get("reproduction") or "")[:3000],
        replay_note=replay_note,
    )
    try:
        text = await call_llm_text(run, system=system_prompt, user_content=user_content, llm_config=llm_config)
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

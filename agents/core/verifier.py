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

import asyncio
import json as json_lib
import logging
import os
import re
from typing import Any

from core.agent import AgentRun, call_llm_text, parse_json_object
from core.board import BoardClient

log = logging.getLogger(__name__)

VERIFY_ENABLED = os.environ.get("DHUNTER_VERIFY", "1") != "0"

# The verifier judges each finding by the rules in the USER message
# (VERIFY_PROMPT below). It does NOT need the run's injected red lines /
# cross-target knowledge, so it uses this tiny fixed system instead of
# forwarding the worker's (token-heavy) system prompt on every call.
VERIFIER_SYSTEM = "你是 SRC 漏洞复核评审。严格按用户消息中的判定规则判断，只输出一个 JSON 对象。"

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
    # Account / username enumeration: a bare existence oracle is only
    # reportable when chained to an exploit (brute-force, lockout bypass).
    # A naked "isNeed:true means the account exists" leak is noise on its own.
    "user enumeration", "account enumeration", "username enumeration",
    "账号枚举", "用户枚举", "用户名枚举",
)
# If a title matches a reject hint AND the evidence shows no actual exploit
# (no data pulled, no bypass, no execution), it's noise.
_EXPLOIT_MARKERS = (
    "accessed", "extracted", "exfiltrated", "read", "bypass", "bypassed",
    "executed", "command", "shell", "token", "jwt", "session", "data of",
    "other user", "admin", "200 ok with", "returned the", "leaked the",
    "contains the", "disclosed the",
)
# Markers that must match as WHOLE WORDS. A bare substring fires on unrelated
# words: "admin" inside "sysadmin" / "netadmin" / "hradmin" is just an account
# name in enumeration evidence — not proof of privileged access.
_WORD_BOUNDARY_MARKERS = {"admin"}


def _has_exploit_marker(title: str, evidence: str) -> bool:
    """True when the finding's text carries an exploit marker. "admin" is
    matched as a whole word so probing 'sysadmin'/'netadmin' usernames never
    counts as evidence of admin access; the rest stay substring matches
    ("another user" still fires "other user", etc.)."""
    text = f"{title}\n{evidence}".lower()
    for m in _EXPLOIT_MARKERS:
        if m in _WORD_BOUNDARY_MARKERS:
            if re.search(rf"\b{re.escape(m)}\b", text):
                return True
        elif m in text:
            return True
    return False

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

## Mechanical replay (platform re-ran the finding's OWN PoC requests)
{replay_note}
If the replay shows the endpoint is DOWN (connection failed), the finding is likely
stale — dismiss. A replay marked "不稳定/unstable" means the exact same request
returned DIFFERENT results on repeated runs — that signal is time-varying noise;
dismiss unless the variation is clearly endpoint-inherent and unrelated to the
finding. A stable replay should be weighed normally against the claimed impact.

A finding is only confirmable if it comes with reproducible steps that
demonstrate the impact (a numbered curl + the expected result). A finding with
no reproduction / no demonstrated impact is dismissed.

## Output — ONE JSON object only
{{"confirm": true, "reason": "<one sentence>", "severity": "<critical|high|medium|low|info>"}}
or
{{"confirm": false, "reason": "<one sentence why it's noise / not proven>"}}
"""


async def run_verifier(run: AgentRun, board: BoardClient, system_prompt: str, llm_config: dict[str, Any] | None = None,
                       auth_context: dict[str, Any] | None = None) -> None:
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
        # system_prompt is intentionally NOT forwarded: the verifier uses
        # the small fixed VERIFIER_SYSTEM (rules live in the user message).
        verdict, reason, severity, replay = await _judge(run, v, llm_config=llm_config, auth_context=auth_context)
        try:
            if verdict:
                # Also correct inflated severities from the LLM.
                final_sev = _cap_severity(v.get("severity") or "medium", v.get("title") or "", severity)
                await board.set_vuln_severity(v["id"], final_sev)
                await board.set_vuln_status(v["id"], "confirmed")
                # Attach the machine-verified PoC record so the report
                # surfaces a strix-shaped "X/Y reproduced, here's the curl"
                # block instead of just the LLM's prose evidence. We only
                # attach when the replay was stable and produced a
                # reproducible signal — otherwise the LLM's text stands
                # alone and we don't fake a PoC.
                if replay and replay.get("ok") and replay.get("stable"):
                    poc_md = render_poc_evidence(replay)
                    if poc_md:
                        try:
                            await board.set_vuln_poc_evidence(v["id"], poc_md)
                        except Exception as e:  # noqa: BLE001
                            log.warning("verifier: failed to write poc_evidence for %s: %s", v.get("id"), e)
                confirmed += 1
            else:
                await board.set_vuln_status(v["id"], "dismissed")
                dismissed += 1
            log.info("verifier: %s -> %s (%s)", (v.get("title") or "")[:50], "confirmed" if verdict else "dismissed", reason)
        except Exception as e:  # noqa: BLE001
            log.warning("verifier: failed to update %s: %s", v.get("id"), e)

    if confirmed or dismissed:
        log.info("verifier: run %s -> %s confirmed, %s dismissed", run.run_id, confirmed, dismissed)


# --- mechanical replay: reproduce the ORACLE, not just the endpoint --------
#
# The old replay did a single GET of the target URL and called it grounding.
# That only detects "endpoint is down"; a false oracle (an isNeed flag that
# flips between requests) sails through. The new replay re-runs the finding's
# OWN PoC curl commands several times and flags any request that returns a
# different (status, body) across identical runs — the exact signature of a
# time-varying signal misread as a differential.

_REPLAY_PASSES = int(os.environ.get("DHUNTER_VERIFY_REPLAY_PASSES", "3"))
_REPLAY_MAX_REQS = int(os.environ.get("DHUNTER_VERIFY_MAX_REQS", "5"))
_REPLAY_INTERVAL = float(os.environ.get("DHUNTER_VERIFY_REPLAY_INTERVAL", "0.5"))

# Terms marking a finding as "oracle-style": its proof IS a differential
# between inputs. For these, a non-reproducible replay is fatal (auto-dismiss).
_ORACLE_HINTS = (
    "oracle", "boolean", "blind", "bool", "differential", "enumeration", "enum",
    "枚举", "布尔", "盲注", "翻转", "isneed", "exists",
)


def _parse_curl_requests(text: str) -> list[dict[str, Any]]:
    """Extract (method, url, headers, body) from the curl commands in a
    finding's reproduction/evidence. Handles single-line curls of the form:
    `curl [-i] [-X METHOD] 'URL' [-H 'H'] [--data 'body']`, including
    backslash-continued multi-line commands."""
    reqs: list[dict[str, Any]] = []
    seen: set[tuple[Any, ...]] = set()

    # Join backslash-continued lines so a multi-line curl parses as one command.
    cmds: list[str] = []
    buf = ""
    for raw in (text or "").splitlines():
        ln = raw.strip()
        if not ln or ln.startswith("#"):
            if buf:
                cmds.append(buf)
                buf = ""
            continue
        buf = f"{buf} {ln}"
        if not ln.endswith("\\"):
            cmds.append(buf)
            buf = ""
    if buf:
        cmds.append(buf)

    for cmd in cmds:
        if "curl" not in cmd:
            continue
        m = re.search(r"(?:'([^']*https?://[^']*)'|\"([^\"]*https?://[^\"]*)\")", cmd)
        if not m:
            continue
        url = m.group(1) or m.group(2)
        m2 = re.search(r"-(?:X|request)\s+([A-Z]+)", cmd)
        method = (m2.group(1) if m2 else "GET").upper()
        if method == "GET" and re.search(r"(?:--data(?:-binary|-raw)?|--json|-d)\b", cmd):
            method = "POST"
        headers: dict[str, str] = {}
        for hm in re.finditer(r"-H\s+(?:'([^']*)'|\"([^\"]*)\")", cmd):
            hv = (hm.group(1) or hm.group(2) or "").strip()
            if ":" in hv:
                k, _, v = hv.partition(":")
                headers[k.strip()] = v.strip()
        body = None
        for bm in re.finditer(
            r"(?:--data-binary|--data-raw|--data|--json|-d)\s+(?:'([^']*)'|\"([^\"]*)\"|(\S+))", cmd
        ):
            body = bm.group(1) or bm.group(2) or bm.group(3)
            break
        key = (method, url, tuple(sorted(headers.items())), body)
        if key in seen:
            continue
        seen.add(key)
        reqs.append({"method": method, "url": url, "headers": headers, "body": body})
    return reqs


def _is_oracle_finding(title: str, evidence: str) -> bool:
    low = f"{title}\n{evidence}".lower()
    return any(h in low for h in _ORACLE_HINTS)


def _stable_replay_note(replay: dict[str, Any]) -> str:
    lines = [f"机械重放({_REPLAY_PASSES} 次, 同请求结果一致/稳定):"]
    for r in replay.get("requests") or []:
        statuses = ", ".join(f"HTTP {s}" for s, _ in r.get("hits", []))
        lines.append(f"- {r['method']} {r['url']} -> {statuses}")
    return "\n".join(lines)


def _unstable_replay_note(replay: dict[str, Any]) -> str:
    lines = [f"⚠️ 机械重放不稳定({_REPLAY_PASSES} 次, 同一请求结果不一致):"]
    for r in replay.get("requests") or []:
        seen = sorted({s for s, _ in r.get("hits", [])})
        lines.append(f"- {r['method']} {r['url']} -> {seen}")
    lines.append(
        "同一请求多次返回不同结果, finding 依赖的信号可能只是时变噪声; "
        "除非证据明确解释该变化是端点固有行为且与漏洞无关, 否则应驳回。"
    )
    return "\n".join(lines)


def _auth_headers_for(auth_context: dict[str, Any] | None) -> tuple[dict[str, str], str]:
    """Resolve the run session's (headers, host): the ACTIVE account's cookies
    and custom headers plus the target host they apply to. Returns empty when
    no session is configured."""
    if not auth_context:
        return {}, ""
    accounts = auth_context.get("accounts") or {}
    active = auth_context.get("active") or "a"
    acct = accounts.get(active) or {}
    cookies = acct.get("cookies") or auth_context.get("cookies")
    hdrs = acct.get("headers") or auth_context.get("headers") or {}
    headers: dict[str, str] = {}
    if cookies:
        headers["Cookie"] = cookies
    for k, v in (hdrs or {}).items():
        headers.setdefault(str(k), str(v))
    host = (auth_context.get("host") or "").strip().lower()
    return headers, host


def _with_auth(r: dict[str, Any], auth_headers: dict[str, str], auth_host: str) -> dict[str, Any]:
    """Attach the run session to a request whose URL belongs to the target
    host (exact host or a subdomain of it). A Cookie the PoC itself set is
    kept — the finding's own headers win over the stored session."""
    if not auth_headers or not auth_host:
        return r
    from urllib.parse import urlparse as _up
    try:
        host = (_up(r["url"]).hostname or "").lower()
    except Exception:  # noqa: BLE001
        return r
    if not (host == auth_host or host.endswith("." + auth_host)):
        return r
    out = dict(r)
    headers = dict(r.get("headers") or {})
    header_keys = {k.lower() for k in headers}
    for k, v in auth_headers.items():
        if k.lower() == "cookie" and "cookie" in header_keys:
            continue  # the PoC set its own Cookie — trust it
        headers.setdefault(k, v)
    out["headers"] = headers
    return out


async def _replay(v: dict[str, Any], auth_context: dict[str, Any] | None = None) -> dict[str, Any]:
    """Mechanically re-run the finding's PoC requests and check the oracle
    actually reproduces. Each distinct curl in reproduction/evidence is fired
    `_REPLAY_PASSES` times; if the same request yields different (status, body)
    across passes, the signal is unstable -> the finding does not reproduce.

    `auth_context` (the run's stored session) is injected into requests that
    hit the target host, so behind-auth findings (IDOR / privilege escalation)
    are replayed with the same session the worker used — a bare replay without
    cookies would 401/302 a valid finding into a false dismissal."""
    import httpx as _httpx

    text = (v.get("reproduction") or "") + "\n" + (v.get("evidence") or "")
    reqs = _parse_curl_requests(text)
    if not reqs:
        url = (v.get("target") or "").strip()
        if not url.startswith("http"):
            return {"ok": False, "error": "no url to replay"}
        reqs = [{"method": "GET", "url": url, "headers": {}, "body": None}]
    reqs = reqs[:_REPLAY_MAX_REQS]

    # Resolve the active account's session (cookies/headers) once.
    auth_headers, auth_host = _auth_headers_for(auth_context)

    outcomes: list[dict[str, Any]] = []
    stable = True
    try:
        async with _httpx.AsyncClient(timeout=10.0, trust_env=False, follow_redirects=True) as client:
            for r in reqs:
                r = _with_auth(r, auth_headers, auth_host)
                hits: list[tuple[int, str]] = []
                for _ in range(_REPLAY_PASSES):
                    resp = await client.request(r["method"], r["url"], headers=r["headers"], content=r["body"])
                    hits.append((resp.status_code, resp.text))
                    await asyncio.sleep(_REPLAY_INTERVAL)
                if len(set(hits)) > 1:
                    stable = False
                outcomes.append({"method": r["method"], "url": r["url"], "hits": hits, "body": r["body"]})
    except Exception as e:  # noqa: BLE001
        return {"ok": False, "error": f"{type(e).__name__}: {e}"}

    first = outcomes[0]
    return {
        "ok": True,
        "status": first["hits"][0][0],
        "url": first["url"],
        "method": first["method"],
        "stable": stable,
        "requests": outcomes,
    }


# Response body excerpt length for the PoC block. Short enough to keep the
# report scannable, long enough that the reader can see what came back.
_POC_BODY_EXCERPT_CHARS = 240


def render_poc_evidence(replay: dict[str, Any]) -> str:
    """Build a Markdown "PoC 验证证据" block from a replay result.

    The block is the single source of truth the report renderer uses to show
    that a finding is real and reproducible — same shape regardless of whether
    the curl came from the worker's `reproduction` or was synthesized by the
    verifier. Strix-shaped: method / URL / payload / status / reproducibility
    count / response excerpt / curl one-liner.
    """
    if not replay.get("ok") or not replay.get("requests"):
        return ""
    reqs = replay["requests"]
    first = reqs[0]
    method = first.get("method") or "GET"
    url = first.get("url") or ""
    status = first.get("hits", [[None, ""]])[0][0]
    body = first.get("hits", [[None, ""]])[0][1] or ""
    body_excerpt = (body[:_POC_BODY_EXCERPT_CHARS] + "…") if len(body) > _POC_BODY_EXCERPT_CHARS else body
    # Reproducibility: same response across all 3 passes?
    pass_count = _REPLAY_PASSES
    all_statuses = [h[0] for h in first.get("hits", [])]
    distinct_statuses = len(set(all_statuses))
    reproducible = (distinct_statuses == 1)

    payload = first.get("body") or ""
    payload_line = ""
    if payload:
        # Trim and present as a literal code span; full payload still in
        # the curl one-liner below.
        short = payload.strip().replace("\n", " ")
        if len(short) > 200:
            short = short[:200] + "…"
        payload_line = f"- **Payload**: `{short}`\n"

    curl_body = ""
    if payload:
        # Body must be passed with --data-raw (preserves special chars);
        # method/URL is a straight interpolation that the reader can copy.
        curl_body = f" --data-raw {json_lib.dumps(payload)}"
    curl = f"curl -X {method} {url}{curl_body}"

    lines = [
        f"- **方法 / URL**: `{method} {url}`",
        payload_line.rstrip(),
        f"- **状态**: `{status}`",
        f"- **复现**: {pass_count}/{pass_count} {'✓' if reproducible else '✗ 不稳定'} (响应一致: {'是' if reproducible else '否, 共 ' + str(distinct_statuses) + ' 种不同状态'})",
    ]
    if body_excerpt:
        lines.append(f"- **响应片段**:\n\n```\n{body_excerpt}\n```")
    lines.append(f"\n**curl 复现**:\n\n```bash\n{curl}\n```")
    return "\n".join(lines)


async def _judge(run: AgentRun, v: dict[str, Any], llm_config: dict[str, Any] | None = None,
                 auth_context: dict[str, Any] | None = None) -> tuple[bool, str, str, dict[str, Any] | None]:
    """Returns (confirmed, reason, llm_severity, replay_result).

    `replay_result` is the dict the verifier just produced mechanically; the
    caller uses it to write a PoC evidence block onto confirmed findings so
    the report can show method/URL/payload/status/reproducibility instead of
    only the LLM's prose.

    Uses the small fixed VERIFIER_SYSTEM instead of the worker's system
    prompt — the judging rules live in the user message, so forwarding the
    run's red lines / knowledge would only burn tokens on every call.
    """
    title = (v.get("title") or "")
    evidence = (v.get("evidence") or "")

    # Hard pre-filter: obvious config noise with no exploit marker.
    low = title.lower()
    is_noise = any(h in low for h in _REJECT_HINTS)
    has_exploit = _has_exploit_marker(title, evidence)
    if is_noise and not has_exploit:
        return False, "config noise / phenomenon, no demonstrated exploit", "low", None

    # Mechanical replay: re-run the finding's own PoC requests and check the
    # oracle is stable. The judge must not trust the worker's text alone — a
    # differential that flips between two identical requests is noise.
    replay = await _replay(v, auth_context)
    if not replay.get("ok"):
        return False, f"机械重放失败: {replay.get('error', '')}", "info", replay
    if replay.get("stable") is False:
        if _is_oracle_finding(title, evidence):
            # An oracle finding whose signal does not reproduce is dead on
            # arrival — dismiss without spending an LLM judgment call.
            return False, "机械重放不稳定: 同一请求多次结果不一致, finding 依赖的 oracle 不可复现, 已自动驳回", "info", replay
        replay_note = _unstable_replay_note(replay)
    else:
        replay_note = _stable_replay_note(replay)
    user_content = VERIFY_PROMPT.format(
        title=title[:300],
        severity=(v.get("severity") or "info"),
        target=(v.get("target") or ""),
        evidence=evidence[:2000],
        reproduction=(v.get("reproduction") or "")[:1500],
        replay_note=replay_note,
    )
    try:
        text = await call_llm_text(run, system=VERIFIER_SYSTEM, user_content=user_content, llm_config=llm_config)
    except Exception as e:  # noqa: BLE001
        log.warning("verifier: judge call failed for %s: %s", v.get("id"), e)
        return False, "verifier LLM call failed", "info", replay
    parsed = parse_json_object(text)
    confirm = parsed.get("confirm") in (True, "true", "True")
    reason = str(parsed.get("reason") or "")
    severity = str(parsed.get("severity") or "").lower()
    if severity not in ("critical", "high", "medium", "low", "info"):
        severity = ""
    return confirm, reason, severity, replay


def _cap_severity(reported: str, title: str, llm_sev: str) -> str:
    """SRC severity calibration: config-ish findings can't be high/critical."""
    low = title.lower()
    configy = any(h in low for h in _REJECT_HINTS)
    chosen = llm_sev or reported
    if configy and chosen in ("critical", "high"):
        return "low"
    return chosen

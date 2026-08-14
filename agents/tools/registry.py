"""Tool registry: fallback HTTP tools + live MCP tools.

Fallback tools keep the agent functional even when the MCP server is
unreachable. They are defined inline so the MVP doesn't depend on a
sidecar process to start a run.

Env:
    DHUNTER_BACKEND_URL  Go server for write_finding POST (default http://127.0.0.1:8080)
    DHUNTER_MCP_URL      MCP streamable-HTTP endpoint
    DHUNTER_MCP_TOKEN    MCP bearer token
"""

from __future__ import annotations

import json
import logging
import os
import time
from typing import Any

import httpx

from .mcp_client import MCPClient, MCPError

log = logging.getLogger(__name__)


def _backend_url() -> str:
    return os.environ.get("DHUNTER_BACKEND_URL", "http://127.0.0.1:8080").rstrip("/")


# --- Fallback tool implementations --------------------------------------


async def _http_request(args: dict[str, Any]) -> dict[str, Any]:
    method = (args.get("method") or "GET").upper()
    url = args.get("url") or ""
    if not url:
        return {"content": "http_request: `url` is required", "is_error": True}
    headers = args.get("headers") or {}
    if not isinstance(headers, dict):
        return {"content": "http_request: `headers` must be an object", "is_error": True}
    body = args.get("body")
    if body is not None and not isinstance(body, str):
        body = str(body)
    try:
        timeout_s = float(args.get("timeout") or 30.0)
    except (TypeError, ValueError):
        timeout_s = 30.0
    # TLS verification is ON by default; opt out with `insecure: true`
    # (self-signed targets).
    insecure = bool(args.get("insecure", False))
    try:
        async with httpx.AsyncClient(trust_env=False, timeout=timeout_s, follow_redirects=True, verify=not insecure) as client:
            resp = await client.request(method, url, headers=headers, content=body)
        text = resp.text
        truncated = False
        if len(text) > 16_000:
            text = text[:16_000] + f"\n... [truncated, total {len(resp.text)} bytes]"
            truncated = True
        out_lines = [
            f"HTTP {resp.status_code} {resp.request.method} {url}",
            f"--- request headers ---",
            json.dumps(dict(resp.request.headers), indent=2),
            f"--- response headers ---",
            json.dumps(dict(resp.headers), indent=2),
            f"--- response body ({len(resp.text)} bytes{' truncated' if truncated else ''}) ---",
            text,
        ]
        return {"content": "\n".join(out_lines), "is_error": False}
    except Exception as e:  # noqa: BLE001 -- the agent wants a string error
        return {"content": f"http_request error: {type(e).__name__}: {e}", "is_error": True}


async def _write_finding(args: dict[str, Any], current_run_id: str = "") -> dict[str, Any]:
    title = (args.get("title") or "").strip()
    if not title:
        return {"content": "write_finding: `title` is required", "is_error": True}
    severity = (args.get("severity") or "info").lower()
    if severity not in {"critical", "high", "medium", "low", "info"}:
        return {"content": f"write_finding: invalid severity `{severity}`", "is_error": True}
    target = args.get("target") or ""
    evidence = args.get("evidence") or ""
    reproduction = args.get("reproduction") or ""
    # Prefer the explicit run_id from the LLM (for batch writes that
    # reference older runs); fall back to the currently active run so
    # the common case works without ceremony.
    run_id = args.get("run_id") or current_run_id

    payload: dict[str, Any] = {
        "title": title,
        "severity": severity,
        "target": target,
        "evidence": evidence,
        "reproduction": reproduction,
        # New findings wait for the verifier before they count as confirmed.
        "status": "pending",
    }
    if run_id:
        payload["run_id"] = run_id

    url = _backend_url() + "/api/vulnerabilities"
    headers = {"Content-Type": "application/json"}
    # Backend admin token (config.yaml admin.token). The token is a
    # service-level credential; not user-bound.
    backend_token = os.environ.get("DHUNTER_BACKEND_TOKEN", "").strip()
    if backend_token:
        headers["Authorization"] = f"Bearer {backend_token}"
    try:
        async with httpx.AsyncClient(trust_env=False, timeout=15.0) as client:
            resp = await client.post(url, json=payload, headers=headers)
        if resp.status_code >= 400:
            return {
                "content": (
                    f"write_finding: backend rejected HTTP {resp.status_code} {resp.text[:500]}"
                ),
                "is_error": True,
            }
        return {
            "content": f"write_finding: recorded (HTTP {resp.status_code}): {resp.text[:300]}",
            "is_error": False,
        }
    except Exception as e:  # noqa: BLE001
        # Don't fail the agent loop on a transient backend outage.
        log.warning("write_finding: backend unreachable: %s", e)
        return {
            "content": (
                f"write_finding: backend unreachable ({type(e).__name__}: {e}); "
                f"kept payload locally: {json.dumps(payload)[:500]}"
            ),
            "is_error": False,
        }


async def _write_fact(args: dict[str, Any], current_run_id: str = "") -> dict[str, Any]:
    """Record an intermediate confirmed observation on the run's board.

    Facts are the blackboard's stepping stones: subdomains found, an
    endpoint discovered, a fingerprint, a credential — anything the agent
    now knows that later exploration can build on. This is NOT for
    vulnerabilities (use write_finding) and NOT for unconfirmed guesses.
    """
    description = (args.get("description") or "").strip()
    if not description:
        return {"content": "write_fact: `description` is required", "is_error": True}
    run_id = args.get("run_id") or current_run_id
    if not run_id:
        return {"content": "write_fact: no run_id available", "is_error": True}

    url = _backend_url() + f"/api/runs/{run_id}/facts"
    headers = {"Content-Type": "application/json"}
    backend_token = os.environ.get("DHUNTER_BACKEND_TOKEN", "").strip()
    if backend_token:
        headers["Authorization"] = f"Bearer {backend_token}"
    try:
        async with httpx.AsyncClient(trust_env=False, timeout=15.0) as client:
            resp = await client.post(url, json={"description": description, "source": "agent"}, headers=headers)
        if resp.status_code >= 400:
            return {"content": f"write_fact: backend rejected HTTP {resp.status_code} {resp.text[:300]}", "is_error": True}
        return {"content": f"write_fact: recorded (HTTP {resp.status_code})", "is_error": False}
    except Exception as e:  # noqa: BLE001
        log.warning("write_fact: backend unreachable: %s", e)
        return {"content": f"write_fact: backend unreachable ({type(e).__name__}: {e})", "is_error": True}


_FALLBACK_HANDLERS: dict[str, Any] = {
    "http_request": _http_request,
    "write_finding": _write_finding,
    "write_fact": _write_fact,
}

_FALLBACK_TOOL_DEFS: list[dict[str, Any]] = [
    {
        "name": "http_request",
        "description": (
            "Send an arbitrary HTTP request. Use for manual probing, auth bypass, "
            "parameter tampering, header/cookie inspection, response comparison. "
            "Returns status line, request/response headers, and body (truncated to 16KB)."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "method": {
                    "type": "string",
                    "enum": ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"],
                    "description": "HTTP method. Default GET.",
                },
                "url": {"type": "string", "description": "Absolute URL to call."},
                "headers": {
                    "type": "object",
                    "description": "Optional request headers as key/value strings.",
                },
                "body": {"type": "string", "description": "Optional raw request body."},
                "timeout": {"type": "number", "description": "Timeout in seconds. Default 30."},
                "insecure": {"type": "boolean", "description": "Skip TLS certificate verification. Default false. Set true only for self-signed targets."},
                "inject_auth": {"type": "boolean", "description": "Default true: attach the target's stored session (cookies/headers). Set false to test the anonymous surface."},
            },
            "required": ["url"],
        },
    },
    {
        "name": "write_fact",
        "description": (
            "Record an intermediate confirmed observation on the run's board "
            "(a subdomain, an endpoint, a fingerprint, a credential). Facts are "
            "stepping stones other exploration builds on. NOT for vulnerabilities "
            "(use write_finding) and NOT for unconfirmed guesses."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "description": {"type": "string", "description": "One concise, factual observation."},
                "run_id": {"type": "string", "description": "Optional run id. Auto-set by the agent."},
            },
            "required": ["description"],
        },
    },
    {
        "name": "write_finding",
        "description": (
            "Record a confirmed vulnerability to the Dhunter backend. Call ONLY after "
            "you have reproducible evidence (status code, response body, payload, "
            "screenshots-equivalent). Do not call for unconfirmed hypotheses."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "title": {"type": "string", "description": "Short, specific finding title."},
                "severity": {
                    "type": "string",
                    "enum": ["critical", "high", "medium", "low", "info"],
                    "description": "Severity. Use `info` for non-vuln observations.",
                },
                "target": {"type": "string", "description": "Affected URL or host."},
                "evidence": {
                    "type": "string",
                    "description": "PoC + proof: request, response, payload, screenshot-equivalent text.",
                },
                "reproduction": {
                    "type": "string",
                    "description": "Step-by-step reproduction: numbered curl commands + expected result. REQUIRED for every confirmed finding.",
                },
                "run_id": {"type": "string", "description": "Optional run id. Auto-set by the agent."},
            },
            "required": ["title", "severity", "target", "evidence", "reproduction"],
        },
    },
]


# --- Tool registry ------------------------------------------------------


class ToolRegistry:
    """Unified view of fallback tools + MCP tools.

    `all_tools()` returns the Anthropic-format tool list to send to the LLM.
    `call(name, args)` dispatches to either a fallback handler or the MCP
    server. Both paths return a uniform `{content, is_error}` dict.
    """

    def __init__(self, mcp_client: MCPClient | None = None):
        self.mcp = mcp_client or MCPClient()
        self._mcp_tools: list[dict[str, Any]] = []
        self._initialized = False
        self._init_error: str | None = None
        # run_id -> auth context {cookies, headers, note, host} injected into
        # http_request calls for that run (see set_run_auth / _inject_auth).
        self._run_auths: dict[str, dict[str, Any]] = {}

    def set_run_auth(self, run_id: str, auth: dict[str, Any] | None) -> None:
        if auth:
            self._run_auths[run_id] = auth
        else:
            self._run_auths.pop(run_id, None)

    def clear_run_auth(self, run_id: str) -> None:
        self._run_auths.pop(run_id, None)

    def _inject_auth(self, args: dict[str, Any], run_id: str) -> dict[str, Any]:
        """Merge the run's stored session into an http_request's args when
        the target URL belongs to the target host. Returns a NEW dict.

        The LLM can opt out per-call with `inject_auth: false` (to test the
        anonymous surface), and an explicit Cookie header it sets is kept.
        """
        if not run_id or run_id not in self._run_auths:
            return args
        if args.get("inject_auth") is False:
            return args
        auth = self._run_auths[run_id]
        url = str(args.get("url") or "")
        host = auth.get("host") or ""
        if not host or not url:
            return args
        # match the host exactly or any subdomain of it
        try:
            from urllib.parse import urlparse
            u = urlparse(url)
        except Exception:  # noqa: BLE001
            return args
        if not u.hostname:
            return args
        if not (u.hostname == host or u.hostname.endswith("." + host)):
            return args

        out = dict(args)
        headers = dict(out.get("headers") or {})
        cookies = auth.get("cookies")
        if cookies and "Cookie" not in headers:
            headers["Cookie"] = cookies
        for k, v in (auth.get("headers") or {}).items():
            headers.setdefault(k, v)
        if headers:
            out["headers"] = headers
        return out

    async def initialize(self) -> None:
        if self._initialized:
            return
        try:
            await self.mcp.initialize()
            self._mcp_tools = await self.mcp.list_tools()
            log.info("MCP ready: %d tools loaded", len(self._mcp_tools))
        except (MCPError, httpx.HTTPError, OSError) as e:
            log.warning("MCP unavailable, fallback tools only: %s", e)
            self._init_error = str(e)
            self._mcp_tools = []
        finally:
            self._initialized = True

    async def aclose(self) -> None:
        await self.mcp.aclose()

    def all_tools(self) -> list[dict[str, Any]]:
        """Anthropic-format tool list (with `input_schema`).

        MCP tools that duplicate a fallback tool (http_request / write_finding
        / write_fact) are dropped so the LLM never sees two same-named tools
        — the fallback wins because it carries the run context (run_id).
        """
        tools: list[dict[str, Any]] = [dict(t) for t in _FALLBACK_TOOL_DEFS]
        fallback_names = {t["name"] for t in _FALLBACK_TOOL_DEFS}
        for t in self._mcp_tools:
            name = t.get("name") or ""
            if name in fallback_names:
                continue
            schema = t.get("inputSchema") or t.get("input_schema") or {"type": "object", "properties": {}}
            if not isinstance(schema, dict):
                schema = {"type": "object", "properties": {}}
            tools.append({
                "name": name,
                "description": t.get("description") or "",
                "input_schema": schema,
            })
        return tools

    def mcp_status(self) -> dict[str, Any]:
        return {
            "ready": self._initialized,
            "tool_count": len(self._mcp_tools),
            "error": self._init_error,
        }

    async def call(self, name: str, arguments: dict[str, Any] | None = None, *, current_run_id: str = "") -> dict[str, Any]:
        args = arguments or {}
        if name in _FALLBACK_HANDLERS:
            handler = _FALLBACK_HANDLERS[name]
            # write_finding / write_fact need the current run_id.
            if name in ("write_finding", "write_fact"):
                return await handler(args, current_run_id=current_run_id)
            # http_request auto-attaches the run's stored session.
            if name == "http_request":
                args = self._inject_auth(args, current_run_id)
            return await handler(args)
        # Delegate to MCP. If MCP was never initialised, try a one-shot init.
        if not self._initialized:
            await self.initialize()
        try:
            result = await self.mcp.call_tool(name, args)
        except (MCPError, httpx.HTTPError, OSError) as e:
            return {"content": f"mcp `{name}` error: {type(e).__name__}: {e}", "is_error": True}
        return _normalize_mcp_result(result)


def _normalize_mcp_result(result: dict[str, Any]) -> dict[str, Any]:
    """Convert an MCP `tools/call` result into the unified `{content, is_error}` shape."""
    if not isinstance(result, dict):
        return {"content": str(result), "is_error": False}
    is_error = bool(result.get("isError")) or bool(result.get("is_error"))
    content = result.get("content")
    return {"content": _content_to_str(content), "is_error": is_error}


def _content_to_str(content: Any) -> str:
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        out: list[str] = []
        for block in content:
            if isinstance(block, dict):
                btype = block.get("type")
                if btype == "text" or btype is None:
                    out.append(str(block.get("text", "")))
                else:
                    out.append(json.dumps(block, ensure_ascii=False))
            else:
                out.append(str(block))
        return "\n".join(out)
    if isinstance(content, dict):
        return json.dumps(content, ensure_ascii=False)
    return str(content)

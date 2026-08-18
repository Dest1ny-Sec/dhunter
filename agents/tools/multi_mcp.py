"""Multi-MCP aggregator.

The built-in dhunter-mcp stays as it was — a single MCPClient. This module
adds the "extension center": the Python agent fetches the user's list of
external MCP servers from the Go backend and connects to each one. Tools
from external servers are surfaced to the LLM as `<server_name>::<tool>`
so they never collide with built-ins (fofa_search, write_finding, ...).

Public surface used by the registry:

    multi = ExternalMCPHub()
    await multi.load_from_backend(backend_url, token)  # one-shot bootstrap
    # (re)load later by calling again — old clients are aclosed.

    multi.all_tools()   -> list of Anthropic-format tool defs (namespaced)
    multi.call(name, args) -> uniform {content, is_error}
    multi.status()      -> dict for /status / debugging

Failure isolation: one broken server does not take the others down. The
hub logs the error and returns an empty tool list for that server; the
rest stay live.
"""

from __future__ import annotations

import asyncio
import logging
import os
import re
import time
from typing import Any

import httpx

from .mcp_client import MCPClient, MCPError

log = logging.getLogger(__name__)

# Server name must be a safe token (letters/digits/_-.). The Go side
# enforces the same regex; we re-check defensively because the LLM
# is going to *see* this string as a tool prefix.
_SAFE_NAME = re.compile(r"^[A-Za-z0-9_.\-]{1,64}$")

# Per-call timeout for external tool invocations. initialize/list_tools
# already carry 10s timeouts; a slow/hung external server must not stall a
# worker's tool loop for the full httpx read timeout (60s+). A hung call
# returns an in-band error instead so the LLM can move on.
CALL_TIMEOUT = float(os.environ.get("DHUNTER_EXT_MCP_CALL_TIMEOUT", "45"))

# Tool-list caps: every external tool is sent to the LLM in the `tools`
# parameter on EVERY turn, so an unbounded list (a server advertising
# thousands of tools) would blow up context cost. Per-server cap + global
# cap; anything over the cap is dropped (with a truncated marker in status).
MAX_TOOLS_PER_SERVER = int(os.environ.get("DHUNTER_EXT_MCP_MAX_TOOLS_PER_SERVER", "100"))
MAX_TOTAL_EXTERNAL_TOOLS = int(os.environ.get("DHUNTER_EXT_MCP_MAX_TOTAL_TOOLS", "300"))


def _backend_url() -> str:
    return os.environ.get("DHUNTER_BACKEND_URL", "http://127.0.0.1:13343").rstrip("/")


def _backend_token() -> str:
    return os.environ.get("DHUNTER_BACKEND_TOKEN", "").strip()


def _ns(server: str, tool: str) -> str:
    return f"{server}::{tool}"


def _split(ns_name: str) -> tuple[str, str] | None:
    """Reverse of _ns. Returns (server, tool) or None if `ns_name`
    is not a namespaced name."""
    if "::" not in ns_name:
        return None
    server, _, tool = ns_name.partition("::")
    if not server or not tool:
        return None
    return server, tool


class ExternalMCPHub:
    """Holds one MCPClient per external server + a name->client map.

    Instances are designed to be reloaded: each call to
    load_from_backend() closes any previously connected clients and
    starts fresh. This makes it cheap to pick up "user added a new MCP"
    without restarting the agent.
    """

    def __init__(self) -> None:
        self._clients: dict[str, MCPClient] = {}  # server name -> client
        # server name -> {"status": ..., "tools": [...], "error": ...}
        self._status: dict[str, dict[str, Any]] = {}
        self._lock = asyncio.Lock()
        # last_reload_at: monotonic timestamp of the most recent
        # load_from_backend() call (any outcome). The UI shows this as
        # "已同步 · X 分钟前" so the user can tell whether the agent has
        # seen their latest config edits.
        self._last_reload_at: float = 0.0
        # last_reload_error: error from the most recent attempt, empty
        # when the last attempt succeeded or none has been made.
        self._last_reload_error: str = ""

    # --- lifecycle ------------------------------------------------------

    async def load_from_backend(
        self, backend_url: str | None = None, token: str | None = None, *,
        concurrency: int = 4,
    ) -> dict[str, dict[str, Any]]:
        """Fetch the enabled external MCP list from the Go backend and
        (re)connect to each one. Returns a per-server status report so
        callers can surface it on /status.

        Safe to call repeatedly: prior clients are aclosed before the
        new batch is opened. A failed server does not stop the others.
        """
        url = (backend_url or _backend_url()).rstrip("/")
        tok = token if token is not None else _backend_token()
        if not tok:
            log.warning("ExternalMCPHub: no DHUNTER_BACKEND_TOKEN; skipping load")
            return {}

        servers = await self._fetch_active(url, tok)
        # Record the attempt timestamp up front — the UI distinguishes
        # "never reloaded" (0) from "reloaded but no servers" (now).
        self._last_reload_at = time.time()
        if not servers:
            await self._reset()
            self._last_reload_error = ""
            return {}

        # Filter to entries that look valid client-side (Go already
        # validates, but a stale row or env race could send a bad one).
        clean: list[dict[str, Any]] = []
        for s in servers:
            name = (s.get("name") or "").strip()
            murl = (s.get("url") or "").strip()
            if not _SAFE_NAME.match(name) or not murl:
                log.warning("ExternalMCPHub: skipping bad row: %r", s)
                continue
            clean.append({
                "name": name,
                "url": murl,
                "token": s.get("token") or "",
                "auth_header": s.get("auth_header") or "Authorization",
                "auth_scheme": s.get("auth_scheme", "Bearer"),
            })
        if not clean:
            self._last_reload_error = "no valid rows after filter"
            return {}

        # Connect in parallel with a small semaphore. Each connection
        # has its own timeout so a stuck server doesn't block the rest.
        sem = asyncio.Semaphore(max(1, concurrency))

        async def connect_one(s: dict[str, Any]) -> tuple[str, dict[str, Any], MCPClient | None]:
            async with sem:
                client = MCPClient(
                    url=s["url"],
                    token=s["token"],
                    auth_header=s["auth_header"],
                    auth_scheme=s["auth_scheme"],
                )
                try:
                    await asyncio.wait_for(client.initialize(), timeout=10.0)
                    tools = await asyncio.wait_for(client.list_tools(), timeout=10.0)
                    # Cap the advertised tool list so a giant server cannot
                    # bloat the LLM `tools` parameter (sent every turn).
                    raw_count = len(tools)
                    truncated = raw_count > MAX_TOOLS_PER_SERVER
                    tools = tools[:MAX_TOOLS_PER_SERVER]
                    return s["name"], {
                        "status": "connected", "tools": tools, "error": "",
                        "raw_tool_count": raw_count, "truncated": truncated,
                    }, client
                except (MCPError, httpx.HTTPError, OSError, asyncio.TimeoutError) as e:
                    log.warning("ExternalMCPHub: %s failed: %s", s["name"], e)
                    # Best-effort close; ignore errors (client may not
                    # even have an open httpx session).
                    try:
                        await client.aclose()
                    except Exception:  # noqa: BLE001
                        pass
                    return s["name"], {"status": "error", "tools": [], "error": str(e)}, None

        results = await asyncio.gather(*(connect_one(s) for s in clean), return_exceptions=True)
        new_clients: dict[str, MCPClient] = {}
        new_status: dict[str, dict[str, Any]] = {}
        for r in results:
            if isinstance(r, BaseException):
                log.warning("ExternalMCPHub: gather error: %s", r)
                continue
            name, status, client = r
            new_status[name] = status
            if client is not None:
                new_clients[name] = client

        # Swap atomically — close the old clients AFTER the new ones
        # are live so an in-flight call against the old set doesn't
        # race the close.
        async with self._lock:
            old = list(self._clients.values())
            self._clients = new_clients
            self._status = new_status
        for c in old:
            try:
                await c.aclose()
            except Exception:  # noqa: BLE001
                pass
        # Surface the per-attempt summary so the UI can show "3/5
        # connected" instead of just "reloaded N". Success if at least
        # one server is connected; partial failures are still a success
        # for bookkeeping (we logged them).
        connected = sum(1 for s in new_status.values() if s.get("status") == "connected")
        if connected == 0 and new_status:
            self._last_reload_error = "no servers connected"
        else:
            self._last_reload_error = ""
        return new_status

    async def _reset(self) -> None:
        async with self._lock:
            old = list(self._clients.values())
            self._clients = {}
            self._status = {}
        for c in old:
            try:
                await c.aclose()
            except Exception:  # noqa: BLE001
                pass

    async def _fetch_active(self, url: str, token: str) -> list[dict[str, Any]]:
        try:
            async with httpx.AsyncClient(trust_env=False, timeout=8.0) as client:
                resp = await client.get(
                    url + "/api/mcp-servers/active",
                    headers={"Authorization": f"Bearer {token}"},
                )
            if resp.status_code >= 400:
                log.warning("ExternalMCPHub: /active returned %d: %s", resp.status_code, resp.text[:200])
                return []
            data = resp.json()
            if not isinstance(data, dict):
                return []
            rows = data.get("servers")
            return rows if isinstance(rows, list) else []
        except (httpx.HTTPError, OSError) as e:
            log.warning("ExternalMCPHub: backend unreachable: %s", e)
            return []

    # --- public surface -------------------------------------------------

    def all_tools(self) -> list[dict[str, Any]]:
        """Anthropic-format tool list. Names are `<server>::<tool>`.
        Enforces the global cap MAX_TOTAL_EXTERNAL_TOOLS (per-server caps
        were already applied at load time)."""
        out: list[dict[str, Any]] = []
        for server, status in self._status.items():
            if status.get("status") != "connected":
                continue
            for t in status.get("tools") or []:
                if not isinstance(t, dict):
                    continue
                raw_name = t.get("name") or ""
                if not raw_name:
                    continue
                schema = t.get("inputSchema") or t.get("input_schema") or {"type": "object", "properties": {}}
                if not isinstance(schema, dict):
                    schema = {"type": "object", "properties": {}}
                out.append({
                    "name": _ns(server, raw_name),
                    "description": t.get("description") or "",
                    "input_schema": schema,
                })
                if len(out) >= MAX_TOTAL_EXTERNAL_TOOLS:
                    return out
        return out

    def status(self) -> list[dict[str, Any]]:
        """Per-server status, suitable for /status JSON."""
        out: list[dict[str, Any]] = []
        for server, st in self._status.items():
            names = [t.get("name") for t in (st.get("tools") or []) if isinstance(t, dict) and t.get("name")]
            item: dict[str, Any] = {
                "name": server,
                "status": st.get("status", "unknown"),
                "tool_count": len(names),
                "tools": names,
                "error": st.get("error") or "",
            }
            # Surface the cap so the UI can say "150 个工具，已取前 100".
            if st.get("truncated"):
                item["truncated"] = True
                item["raw_tool_count"] = st.get("raw_tool_count", len(names))
            out.append(item)
        return out

    def sync_info(self) -> dict[str, Any]:
        """Agent-side snapshot for the UI's "last sync" indicator.

        `last_reload_at` is a Unix timestamp; the UI converts to relative
        time. `last_reload_error` is empty on success. `servers` is the
        same shape as `status()` (per-server ready/error/tools).
        """
        return {
            "last_reload_at": self._last_reload_at,
            "last_reload_error": self._last_reload_error,
            "servers": self.status(),
        }

    async def call(self, name: str, arguments: dict[str, Any] | None = None) -> dict[str, Any]:
        """Dispatch a namespaced tool call to the right client.

        Returns the uniform {content, is_error} shape the rest of the
        agent expects. Errors are NOT raised — they surface as a
        structured failure so the LLM can read them in-band.
        """
        parts = _split(name)
        if not parts:
            return {"content": f"external_mcp: name `{name}` is not namespaced (expected `<server>::<tool>`)", "is_error": True}
        server, tool = parts
        client = self._clients.get(server)
        if client is None:
            return {"content": f"external_mcp: server `{server}` is not connected", "is_error": True}
        try:
            # Bound the call: a hung external server must not stall the
            # worker's tool loop indefinitely (see CALL_TIMEOUT).
            result = await asyncio.wait_for(client.call_tool(tool, arguments or {}), timeout=CALL_TIMEOUT)
        except (MCPError, httpx.HTTPError, OSError, asyncio.TimeoutError) as e:
            return {"content": f"external_mcp `{name}` error: {type(e).__name__}: {e}", "is_error": True}
        return _normalize_mcp_result(result)

    async def aclose(self) -> None:
        await self._reset()


def _normalize_mcp_result(result: dict[str, Any]) -> dict[str, Any]:
    """Same shape as the built-in MCP path so the agent loop treats
    external and built-in identically."""
    if not isinstance(result, dict):
        return {"content": str(result), "is_error": False}
    is_error = bool(result.get("isError")) or bool(result.get("is_error"))
    content = result.get("content")
    return {"content": _content_to_str(content), "is_error": is_error}


def _content_to_str(content: Any) -> str:
    import json as _json
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        out = []
        for block in content:
            if isinstance(block, dict):
                btype = block.get("type")
                if btype == "text" or btype is None:
                    out.append(str(block.get("text", "")))
                else:
                    out.append(_json.dumps(block, ensure_ascii=False))
            else:
                out.append(str(block))
        return "\n".join(out)
    if isinstance(content, dict):
        return _json.dumps(content, ensure_ascii=False)
    return str(content)

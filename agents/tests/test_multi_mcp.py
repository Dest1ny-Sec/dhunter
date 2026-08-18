"""Tests for the external MCP hub (extension center).

Each test runs in a single `asyncio.run()` block so the aiohttp test
servers share the same event loop as the hub under test. Splitting
start/act into two `asyncio.run` calls kills the loop and the server
goes deaf — pytest shows that as a 5s ReadTimeout.
"""

from __future__ import annotations

import asyncio
import json
import socket
from typing import Any

import pytest

from tools.multi_mcp import (
    ExternalMCPHub,
    _ns,
    _split,
)


# --- pure helpers ------------------------------------------------------


def test_ns_and_split_roundtrip():
    assert _ns("nuclei", "scan") == "nuclei::scan"
    assert _split("nuclei::scan") == ("nuclei", "scan")
    assert _split("no_separator") is None
    assert _split("::tool") is None
    assert _split("server::") is None


def test_split_ignores_non_namespaced():
    """A bare tool name (built-in MCP path) is NOT a namespaced call."""
    assert _split("fofa_search") is None
    assert _split("http_request") is None


# --- mock MCP server ---------------------------------------------------


def _free_port() -> int:
    """Ask the kernel for a free TCP port. Closing the probe socket
    leaves the port briefly free, but aiohttp's TCPSite.start() re-binds
    fast enough that we never collide in practice. (`port=0` is not
    a reliable way to read the bound port back in aiohttp <4.)"""
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    p = s.getsockname()[1]
    s.close()
    return p


class MockMCPServer:
    """Tiny in-process MCP stand-in. Serves `initialize` + `tools/list`
    + `tools/call` over an httpx-compatible HTTP endpoint."""

    def __init__(self, name: str, tools: list[dict[str, Any]], call_handler=None):
        self._name = name
        self._tools = tools
        self._call_handler = call_handler
        self._app = None
        self._runner = None
        self._site = None
        self.port: int = 0
        self.url: str = ""

    async def start(self):
        from aiohttp import web  # type: ignore
        self.port = _free_port()
        self._app = web.Application()
        self._app.router.add_post("/mcp", self._handle)
        self._runner = web.AppRunner(self._app)
        await self._runner.setup()
        self._site = web.TCPSite(self._runner, "127.0.0.1", self.port)
        await self._site.start()
        self.url = f"http://127.0.0.1:{self.port}/mcp"

    async def stop(self):
        if self._runner is not None:
            await self._runner.cleanup()

    async def _handle(self, request):
        from aiohttp import web  # type: ignore
        body = await request.json()
        method = body.get("method")
        rid = body.get("id")
        if method == "initialize":
            return web.json_response({
                "jsonrpc": "2.0",
                "id": rid,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": self._name, "version": "1.0.0"},
                },
            })
        if method == "tools/list":
            return web.json_response({"jsonrpc": "2.0", "id": rid, "result": {"tools": self._tools}})
        if method == "tools/call":
            params = body.get("params") or {}
            tname = params.get("name")
            targs = params.get("arguments") or {}
            if self._call_handler:
                content = self._call_handler(tname, targs)
            else:
                content = f"echo: {tname} {json.dumps(targs)}"
            return web.json_response({
                "jsonrpc": "2.0",
                "id": rid,
                "result": {"content": [{"type": "text", "text": content}], "isError": False},
            })
        return web.json_response({"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "unknown method"}}, status=400)


class MockBackend:
    """Tiny stand-in for the Go /api/mcp-servers/active endpoint."""

    def __init__(self, rows: list[dict[str, Any]]):
        self._rows = rows
        self._app = None
        self._runner = None
        self._site = None
        self.port: int = 0
        self.url: str = ""

    async def start(self):
        from aiohttp import web  # type: ignore
        self.port = _free_port()

        async def active(request):
            return web.json_response({"servers": self._rows})

        self._app = web.Application()
        self._app.router.add_get("/api/mcp-servers/active", active)
        self._runner = web.AppRunner(self._app)
        await self._runner.setup()
        self._site = web.TCPSite(self._runner, "127.0.0.1", self.port)
        await self._site.start()
        self.url = f"http://127.0.0.1:{self.port}"

    async def stop(self):
        if self._runner is not None:
            await self._runner.cleanup()


def _make_mocks():
    return MockMCPServer("alpha", [
        {"name": "scan", "description": "alpha-scan", "inputSchema": {"type": "object", "properties": {"target": {"type": "string"}}}},
        {"name": "recon", "description": "alpha-recon", "inputSchema": {"type": "object"}},
    ], call_handler=lambda n, a: f"alpha::{n} ok {a}"), MockMCPServer("beta", [
        {"name": "lookup", "description": "beta-lookup", "inputSchema": {"type": "object"}},
    ], call_handler=lambda n, a: f"beta::{n} ok")


# --- tests -------------------------------------------------------------


def test_hub_initial_state_is_empty():
    hub = ExternalMCPHub()
    assert hub.all_tools() == []
    assert hub.status() == []


def test_hub_aggregates_and_dispatches():
    """Seed the hub directly (no backend) and verify namespacing + dispatch."""
    async def scenario():
        alpha, beta = _make_mocks()
        await alpha.start()
        await beta.start()
        try:
            from tools.mcp_client import MCPClient
            hub = ExternalMCPHub()
            hub._clients["alpha"] = MCPClient(url=alpha.url, token="")
            hub._clients["beta"] = MCPClient(url=beta.url, token="")
            hub._status["alpha"] = {"status": "connected", "tools": [
                {"name": "scan", "description": "a", "inputSchema": {"type": "object"}},
                {"name": "recon", "description": "a", "inputSchema": {"type": "object"}},
            ], "error": ""}
            hub._status["beta"] = {"status": "connected", "tools": [
                {"name": "lookup", "description": "b", "inputSchema": {"type": "object"}},
            ], "error": ""}

            tools = hub.all_tools()
            names = {t["name"] for t in tools}
            assert "alpha::scan" in names
            assert "alpha::recon" in names
            assert "beta::lookup" in names
            assert "scan" not in names  # no bare names
            assert "lookup" not in names

            statuses = {s["name"]: s for s in hub.status()}
            assert statuses["alpha"]["tool_count"] == 2
            assert statuses["beta"]["tool_count"] == 1
            assert statuses["alpha"]["status"] == "connected"

            res = await hub.call("alpha::scan", {"target": "x"})
            assert res["is_error"] is False
            assert "alpha::scan ok" in res["content"]
            res = await hub.call("beta::lookup", {})
            assert "beta::lookup ok" in res["content"]
        finally:
            await alpha.stop()
            await beta.stop()

    asyncio.run(scenario())


def test_hub_dispatches_unknown_server_cleanly():
    async def scenario():
        hub = ExternalMCPHub()
        res = await hub.call("ghost::whatever", {})
        assert res["is_error"] is True
        assert "not connected" in res["content"]
    asyncio.run(scenario())


def test_hub_dispatches_non_namespaced_as_error():
    async def scenario():
        hub = ExternalMCPHub()
        res = await hub.call("fofa_search", {})
        assert res["is_error"] is True
        assert "not namespaced" in res["content"]
    asyncio.run(scenario())


def test_hub_load_from_backend_with_mock():
    """End-to-end: stand up a fake /api/mcp-servers/active and verify
    the hub connects and aggregates."""
    async def scenario():
        alpha, beta = _make_mocks()
        await alpha.start()
        await beta.start()
        backend = MockBackend([
            {"name": "alpha", "url": alpha.url, "transport": "http", "token": ""},
            {"name": "beta", "url": beta.url, "transport": "http", "token": "tok"},
        ])
        await backend.start()
        try:
            hub = ExternalMCPHub()
            status = await hub.load_from_backend(backend_url=backend.url, token="test-tok")
            assert set(status.keys()) == {"alpha", "beta"}
            assert all(s.get("status") == "connected" for s in status.values())

            names = {t["name"] for t in hub.all_tools()}
            assert "alpha::scan" in names
            assert "beta::lookup" in names

            res = await hub.call("alpha::scan", {"target": "x"})
            assert res["is_error"] is False
            assert "alpha::scan ok" in res["content"]
        finally:
            await alpha.stop()
            await beta.stop()
            await backend.stop()

    asyncio.run(scenario())


def test_hub_isolates_failures():
    """One bad URL must not take the rest down."""
    async def scenario():
        alpha, _ = _make_mocks()
        await alpha.start()
        backend = MockBackend([
            {"name": "good", "url": alpha.url, "transport": "http", "token": ""},
            # Bad URL — port 1 reliably refuses on every platform.
            {"name": "bad", "url": "http://127.0.0.1:1/mcp", "transport": "http", "token": ""},
        ])
        await backend.start()
        try:
            hub = ExternalMCPHub()
            status = await hub.load_from_backend(backend_url=backend.url, token="t")
            assert status["good"]["status"] == "connected", f"expected good connected, got {status}"
            assert status["bad"]["status"] == "error"
            ok = await hub.call("good::scan", {"target": "x"})
            assert ok["is_error"] is False
            bad = await hub.call("bad::scan", {"target": "x"})
            assert bad["is_error"] is True
        finally:
            await alpha.stop()
            await backend.stop()

    asyncio.run(scenario())


def test_hub_empty_active_disables_all():
    """If the backend returns no servers, the hub disconnects everything."""
    async def scenario():
        alpha, _ = _make_mocks()
        await alpha.start()
        backend = MockBackend([])
        await backend.start()
        try:
            from tools.mcp_client import MCPClient
            hub = ExternalMCPHub()
            # Seed one client to verify it gets cleared.
            hub._clients["old"] = MCPClient(url=alpha.url, token="")
            hub._status["old"] = {"status": "connected", "tools": [], "error": ""}

            status = await hub.load_from_backend(backend_url=backend.url, token="t")
            assert status == {}
            assert hub.all_tools() == []
            assert hub.status() == []
        finally:
            await alpha.stop()
            await backend.stop()

    asyncio.run(scenario())


def test_sync_info_records_last_reload_and_per_server_status():
    """sync_info() must report last_reload_at (Unix seconds) and the
    per-server ready/error breakdown, even on partial failures."""
    import time as _t

    async def scenario():
        alpha, _ = _make_mocks()
        await alpha.start()
        backend = MockBackend([
            {"name": "good", "url": alpha.url, "transport": "http", "token": ""},
            {"name": "bad", "url": "http://127.0.0.1:1/mcp", "transport": "http", "token": ""},
        ])
        await backend.start()
        try:
            hub = ExternalMCPHub()
            t0 = _t.time()
            await hub.load_from_backend(backend_url=backend.url, token="t")
            t1 = _t.time()

            info = hub.sync_info()
            assert t0 <= info["last_reload_at"] <= t1
            # No "no servers connected" error since at least one (good) is up.
            assert info["last_reload_error"] == ""
            assert isinstance(info["servers"], list)
            assert len(info["servers"]) == 2
            by_name = {s["name"]: s for s in info["servers"]}
            assert by_name["good"]["status"] == "connected"
            assert by_name["bad"]["status"] == "error"
        finally:
            await alpha.stop()
            await backend.stop()

    asyncio.run(scenario())


def test_sync_info_zero_before_any_reload():
    hub = ExternalMCPHub()
    info = hub.sync_info()
    assert info["last_reload_at"] == 0.0
    assert info["last_reload_error"] == ""
    assert info["servers"] == []


class _HangingClient:
    """External MCP client whose call_tool never returns — must be bounded
    by the hub's CALL_TIMEOUT instead of stalling the tool loop."""

    async def initialize(self):
        return {}

    async def list_tools(self):
        return []

    async def call_tool(self, name, arguments=None):
        await asyncio.Event().wait()  # hang forever
        return {}

    async def aclose(self):
        pass


def test_hub_call_bounds_hung_external_server(monkeypatch):
    """A hung external server must surface as an in-band error after the
    CALL_TIMEOUT, NOT block the worker's tool loop indefinitely."""
    import core.agent  # noqa: F401  (ensures sys.path for relative imports)
    from tools.multi_mcp import CALL_TIMEOUT, ExternalMCPHub

    async def scenario():
        hub = ExternalMCPHub()
        hub._clients["hung-server"] = _HangingClient()  # noqa: SLF001
        monkeypatch.setattr("tools.multi_mcp.CALL_TIMEOUT", 0.2)  # 200ms in test
        t0 = asyncio.get_running_loop().time()
        result = await hub.call("hung-server::some_tool", {})
        elapsed = asyncio.get_running_loop().time() - t0
        assert result.get("is_error") is True, result
        assert "TimeoutError" in result.get("content", ""), result
        assert elapsed < 5, f"call did not bound: {elapsed:.2f}s"
        await hub.aclose()

    asyncio.run(scenario())


def test_hub_caps_oversized_tool_lists():
    """A server advertising more than MAX_TOOLS_PER_SERVER tools must be
    capped at load time (marked truncated), and all_tools() must never
    exceed the global cap."""
    from tools.multi_mcp import MAX_TOOLS_PER_SERVER, MAX_TOTAL_EXTERNAL_TOOLS, ExternalMCPHub

    async def scenario():
        # 150 tools on one server; 200 on another → global 300 cap kicks in.
        tools_a = [{"name": f"a_tool_{i}", "description": "x", "inputSchema": {"type": "object"}} for i in range(150)]
        tools_b = [{"name": f"b_tool_{i}", "description": "y", "inputSchema": {"type": "object"}} for i in range(200)]
        srv_a = MockMCPServer("big-a", tools_a)
        srv_b = MockMCPServer("big-b", tools_b)
        await srv_a.start()
        await srv_b.start()
        try:
            backend = MockBackend([
                {"name": "big-a", "url": srv_a.url, "transport": "http", "token": "t"},
                {"name": "big-b", "url": srv_b.url, "transport": "http", "token": "t"},
            ])
            await backend.start()
            try:
                hub = ExternalMCPHub()
                status = await hub.load_from_backend(backend.url, "tok")
                # Per-server cap applied at load.
                assert status["big-a"]["truncated"] is True, status["big-a"]
                assert status["big-a"]["raw_tool_count"] == 150, status["big-a"]
                assert len(status["big-a"]["tools"]) == MAX_TOOLS_PER_SERVER
                # Per-server cap already limits each to 100 → total 200.
                all_tools = hub.all_tools()
                assert len(all_tools) == 2 * MAX_TOOLS_PER_SERVER, len(all_tools)
                # status() reflects the truncated marker.
                st = {s["name"]: s for s in hub.status()}
                assert st["big-a"].get("truncated") is True
                assert st["big-a"].get("raw_tool_count") == 150
                assert st["big-b"].get("truncated") is True
                assert st["big-b"].get("raw_tool_count") == 200
                await hub.aclose()

                # Global cap: 4 servers × 100 tools → all_tools() stops at
                # MAX_TOTAL_EXTERNAL_TOOLS (pure state, no HTTP needed).
                hub2 = ExternalMCPHub()
                for i in range(4):
                    hub2._status[f"s{i}"] = {
                        "status": "connected",
                        "tools": [{"name": f"t{j}", "description": "x", "inputSchema": {"type": "object"}} for j in range(100)],
                        "error": "",
                    }
                assert len(hub2.all_tools()) == MAX_TOTAL_EXTERNAL_TOOLS, len(hub2.all_tools())
                await hub2.aclose()
            finally:
                await backend.stop()
        finally:
            await srv_a.stop()
            await srv_b.stop()

    asyncio.run(scenario())

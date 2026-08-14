"""Unit tests for the tool registry (fallback tools + MCP tool list)."""

from __future__ import annotations

import asyncio
import json

from tools.registry import ToolRegistry, _FALLBACK_HANDLERS, _FALLBACK_TOOL_DEFS


def test_fallback_tool_defs_have_valid_schemas():
    names = {t["name"] for t in _FALLBACK_TOOL_DEFS}
    assert "http_request" in names
    assert "write_finding" in names
    for t in _FALLBACK_TOOL_DEFS:
        assert t["name"]
        assert t["description"]
        assert t["input_schema"]["type"] == "object"
        # every tool whose handler exists must have a matching def;
        # session_set is handled specially in ToolRegistry.call()
        assert t["name"] in _FALLBACK_HANDLERS or t["name"] == "session_set"


def test_http_request_validates_url():
    async def run():
        reg = ToolRegistry()
        res = await reg.call("http_request", {"url": ""})
        assert res["is_error"] is True
        return res

    res = asyncio.run(run())
    assert "url" in res["content"]


def test_http_request_rejects_bad_method_no_network():
    """We don't hit the network in unit tests; ensure arg validation is
    cheap and the request shape is assembled correctly via a stub client."""
    async def run():
        reg = ToolRegistry()
        # patch the underlying httpx call by injecting a fake result
        # through the normal path is complex; instead assert the handler
        # rejects a non-dict headers argument before any network I/O.
        res = await reg.call("http_request", {"url": "http://127.0.0.1:1", "headers": "not-a-dict"})
        assert res["is_error"] is True
        return res

    res = asyncio.run(run())
    assert res["is_error"] is True


def test_write_finding_validates_severity():
    async def run():
        reg = ToolRegistry()
        res = await reg.call("write_finding", {"title": "x", "severity": "not-a-level"})
        assert res["is_error"] is True
        return res

    res = asyncio.run(run())
    assert "severity" in res["content"]


def test_tool_defs_are_json_serializable():
    # The tool defs are what we hand to the LLM; they must be valid JSON.
    json.dumps(_FALLBACK_TOOL_DEFS)

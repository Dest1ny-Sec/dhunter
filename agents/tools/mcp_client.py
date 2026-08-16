"""MCP streamable-HTTP client (JSON-RPC 2.0).

Speaks the minimum subset the Dhunter agent needs:
    - initialize
    - tools/list
    - tools/call

The wire format is one POST per request. The server may answer with either
a JSON body or an SSE stream; both are supported. Server-to-client
notifications (e.g. `notifications/tools/list_changed`) are not yet
handled -- the Go side can re-list tools on demand.

Env:
    DHUNTER_MCP_URL   default: http://127.0.0.1:9124/message
    DHUNTER_MCP_TOKEN default: (empty — no auth header; the MCP server
                      requires a token, so deployments must set this to the
                      same value as dhunter-mcp's -t / DHUNTER_MCP_TOKEN)
"""

from __future__ import annotations

import json
import logging
import os
from typing import Any

import httpx

log = logging.getLogger(__name__)

DEFAULT_MCP_URL = "http://127.0.0.1:9124/message"
# Intentionally EMPTY: there is no static default credential anymore. The
# start scripts generate a random MCP token and pass it via
# DHUNTER_MCP_TOKEN; without it the agent can't auth to the toolbelt and
# falls back to the built-in HTTP tools.
DEFAULT_MCP_TOKEN = ""
PROTOCOL_VERSION = "2024-11-05"


class MCPError(RuntimeError):
    """Wraps a JSON-RPC error from the MCP server."""

    def __init__(self, code: int, message: str, data: Any = None):
        super().__init__(f"MCP error {code}: {message}")
        self.code = code
        self.message = message
        self.data = data


class MCPClient:
    def __init__(self, url: str | None = None, token: str | None = None):
        self.url = (url or os.environ.get("DHUNTER_MCP_URL") or DEFAULT_MCP_URL).rstrip("/")
        self.token = token if token is not None else os.environ.get("DHUNTER_MCP_TOKEN", DEFAULT_MCP_TOKEN)
        self._id = 0
        self._client: httpx.AsyncClient | None = None

    # --- lifecycle ------------------------------------------------------

    async def _ensure_client(self) -> httpx.AsyncClient:
        if self._client is None or self._client.is_closed:
            self._client = httpx.AsyncClient(trust_env=False, 
                timeout=httpx.Timeout(connect=10.0, read=60.0, write=10.0, pool=10.0),
            )
        return self._client

    async def aclose(self) -> None:
        if self._client is not None and not self._client.is_closed:
            await self._client.aclose()
        self._client = None

    # --- core JSON-RPC --------------------------------------------------

    def _next_id(self) -> int:
        self._id += 1
        return self._id

    def _headers(self) -> dict[str, str]:
        h = {
            "content-type": "application/json",
            "accept": "application/json, text/event-stream",
        }
        if self.token:
            h["authorization"] = f"Bearer {self.token}"
        return h

    async def _rpc(self, method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "jsonrpc": "2.0",
            "id": self._next_id(),
            "method": method,
        }
        if params is not None:
            payload["params"] = params

        client = await self._ensure_client()
        resp = await client.post(self.url, json=payload, headers=self._headers())
        if resp.status_code >= 400:
            body = resp.text[:500]
            raise MCPError(resp.status_code, f"HTTP {resp.status_code}: {body}")
        ctype = resp.headers.get("content-type", "")
        if "text/event-stream" in ctype:
            data = await self._read_sse_message(resp)
        else:
            try:
                data = resp.json()
            except json.JSONDecodeError as e:
                raise MCPError(-1, f"non-JSON response (content-type={ctype}): {resp.text[:200]}") from e
        return self._unwrap(data)

    @staticmethod
    def _unwrap(data: dict[str, Any]) -> dict[str, Any]:
        if not isinstance(data, dict):
            raise MCPError(-1, f"unexpected response shape: {type(data).__name__}")
        if "error" in data and data["error"] is not None:
            err = data["error"] or {}
            raise MCPError(
                code=int(err.get("code", -1)),
                message=str(err.get("message", "unknown error")),
                data=err.get("data"),
            )
        if "result" not in data:
            raise MCPError(-1, f"missing `result` in response: {data}")
        return data["result"]

    @staticmethod
    async def _read_sse_message(resp: httpx.Response) -> dict[str, Any]:
        """Walk an SSE response and return the FIRST non-notification message.

        Notifications (no `id` field) and progress events are skipped; the
        first request/reply is returned. If the stream ends without a
        reply, raises MCPError.
        """
        event_type: str | None = None
        data_buf: list[str] = []
        async for line in resp.aiter_lines():
            if line == "":
                if data_buf:
                    joined = "\n".join(data_buf)
                    data_buf = []
                    try:
                        msg = json.loads(joined)
                    except json.JSONDecodeError:
                        continue
                    if isinstance(msg, dict) and "id" in msg:
                        return msg
                event_type = None
                continue
            if line.startswith(":"):
                continue
            if line.startswith("event:"):
                event_type = line[len("event:"):].strip()
                continue
            if line.startswith("data:"):
                payload = line[len("data:"):]
                if payload.startswith(" "):
                    payload = payload[1:]
                data_buf.append(payload)
        raise MCPError(-1, "SSE response closed without a JSON-RPC reply")

    # --- MCP methods ----------------------------------------------------

    async def initialize(self) -> dict[str, Any]:
        return await self._rpc(
            "initialize",
            {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": {"name": "dhunter-agent", "version": "0.1.0"},
            },
        )

    async def list_tools(self) -> list[dict[str, Any]]:
        result = await self._rpc("tools/list")
        tools = result.get("tools")
        if not isinstance(tools, list):
            raise MCPError(-1, f"tools/list returned non-list `tools`: {type(tools).__name__}")
        return tools

    async def call_tool(self, name: str, arguments: dict[str, Any] | None = None) -> dict[str, Any]:
        return await self._rpc(
            "tools/call",
            {"name": name, "arguments": arguments or {}},
        )

    async def ping(self) -> dict[str, Any]:
        return await self._rpc("ping")

"""Blackboard client: talks to the Go backend's board API.

The Go server owns the durable facts/intents/hints tables (single source
of truth, survives crashes). The Python dispatcher reads and mutates the
board over HTTP so multiple workers coordinate through it (stigmergy)
instead of talking to each other.

Env:
    DHUNTER_BACKEND_URL   default: http://127.0.0.1:8080
    DHUNTER_BACKEND_TOKEN admin bearer token
"""

from __future__ import annotations

import os
from typing import Any

import httpx

from tools.registry import log


def _backend_url() -> str:
    return os.environ.get("DHUNTER_BACKEND_URL", "http://127.0.0.1:8080").rstrip("/")


def _backend_token() -> str:
    return os.environ.get("DHUNTER_BACKEND_TOKEN", "").strip()


class BoardError(RuntimeError):
    """Raised when the backend rejects a board mutation."""


class BoardClient:
    def __init__(self, base_url: str | None = None, token: str | None = None):
        self.base_url = (base_url or _backend_url()).rstrip("/")
        self.token = token if token is not None else _backend_token()
        self._client: httpx.AsyncClient | None = None

    async def _ensure_client(self) -> httpx.AsyncClient:
        if self._client is None or self._client.is_closed:
            self._client = httpx.AsyncClient(timeout=httpx.Timeout(connect=5.0, read=30.0, write=10.0, pool=5.0))
        return self._client

    async def aclose(self) -> None:
        if self._client is not None and not self._client.is_closed:
            await self._client.aclose()
        self._client = None

    def _headers(self) -> dict[str, str]:
        h = {"content-type": "application/json"}
        if self.token:
            h["authorization"] = f"Bearer {self.token}"
        return h

    async def _request(self, method: str, path: str, **kwargs: Any) -> httpx.Response:
        client = await self._ensure_client()
        return await client.request(method, self.base_url + path, headers=self._headers(), **kwargs)

    async def graph(self, run_id: str) -> dict[str, Any]:
        resp = await self._request("GET", f"/api/runs/{run_id}/graph")
        resp.raise_for_status()
        return resp.json()

    async def create_fact(self, run_id: str, description: str, source: str = "agent") -> str:
        resp = await self._request("POST", f"/api/runs/{run_id}/facts", json={"description": description, "source": source})
        if resp.status_code >= 400:
            raise BoardError(f"create_fact http {resp.status_code}: {resp.text[:300]}")
        return resp.json().get("id", "")

    async def create_intent(self, run_id: str, from_facts: list[str], description: str, creator: str) -> str | None:
        resp = await self._request("POST", f"/api/runs/{run_id}/intents", json={"from": from_facts, "description": description, "creator": creator})
        if resp.status_code >= 400:
            raise BoardError(f"create_intent http {resp.status_code}: {resp.text[:300]}")
        return resp.json().get("id")

    async def claim_intent(self, run_id: str, intent_id: str, worker: str) -> bool:
        resp = await self._request("POST", f"/api/runs/{run_id}/intents/{intent_id}/claim", json={"worker": worker})
        return resp.status_code == 200  # 409 = already claimed, 404 = gone

    async def release_intent(self, run_id: str, intent_id: str, worker: str) -> None:
        await self._request("POST", f"/api/runs/{run_id}/intents/{intent_id}/release", json={"worker": worker})

    async def fail_intent(self, run_id: str, intent_id: str, worker: str) -> None:
        await self._request("POST", f"/api/runs/{run_id}/intents/{intent_id}/fail", json={"worker": worker})

    async def conclude_intent(self, run_id: str, intent_id: str, worker: str, description: str) -> str | None:
        resp = await self._request("POST", f"/api/runs/{run_id}/intents/{intent_id}/conclude", json={"worker": worker, "description": description})
        if resp.status_code >= 400:
            return None
        return resp.json().get("fact", {}).get("id")

    async def create_hint(self, run_id: str, content: str, creator: str = "agent") -> None:
        await self._request("POST", f"/api/runs/{run_id}/hints", json={"content": content, "creator": creator})

    async def create_vulnerability(self, payload: dict[str, Any]) -> None:
        """Direct vulnerability write (used by the write_finding tool and
        the verifier). The backend validates run_id."""
        resp = await self._request("POST", "/api/vulnerabilities", json=payload)
        if resp.status_code >= 400:
            raise BoardError(f"create_vulnerability http {resp.status_code}: {resp.text[:300]}")

    async def list_vulnerabilities(self, run_id: str) -> list[dict[str, Any]]:
        """Fetch the run's vulnerabilities (with their lifecycle status)."""
        resp = await self._request("GET", f"/api/runs/{run_id}/vulnerabilities")
        if resp.status_code >= 400:
            raise BoardError(f"list_vulnerabilities http {resp.status_code}: {resp.text[:300]}")
        data = resp.json()
        vulns = data.get("vulnerabilities") or data.get("data") or []
        return vulns if isinstance(vulns, list) else []

    async def set_vuln_status(self, vuln_id: str, status: str) -> None:
        """Flip a vulnerability's lifecycle status (pending -> confirmed/dismissed)."""
        resp = await self._request("PATCH", f"/api/vulnerabilities/{vuln_id}", json={"status": status})
        if resp.status_code >= 400:
            raise BoardError(f"set_vuln_status http {resp.status_code}: {resp.text[:300]}")

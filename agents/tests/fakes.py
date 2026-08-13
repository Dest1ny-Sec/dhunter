"""Fakes for testing the dispatcher without a backend or a real LLM.

FakeBoard is an in-memory implementation of BoardClient's interface,
replicating the claim CAS / conclude semantics the Go backend enforces.
ScriptedLLM replaces `core.agent.stream_chat` with a scripted responder
that branches on whether the prompt is a reason or an explore task.
"""

from __future__ import annotations

import uuid
from typing import Any

from llm.anthropic_client import StreamEvent


class FakeBoard:
    """In-memory board mirroring BoardClient's methods."""

    def _new_id(self) -> str:
        return uuid.uuid4().hex[:12]

    async def graph(self, run_id: str) -> dict[str, Any]:
        return {
            "run": {"id": run_id, "status": "running"},
            "facts": self.facts,
            "intents": self.intents,
            "hints": self.hints,
        }

    async def create_fact(self, run_id: str, description: str, source: str = "agent") -> str:
        f = {"id": self._new_id(), "run_id": run_id, "description": description, "source": source}
        self.facts.append(f)
        return f["id"]

    async def create_intent(self, run_id: str, from_facts: list[str], description: str, creator: str) -> str | None:
        it = {"id": self._new_id(), "run_id": run_id, "from": from_facts, "description": description,
              "creator": creator, "worker": None, "status": "open", "to_fact_id": None}
        self.intents.append(it)
        return it["id"]

    async def claim_intent(self, run_id: str, intent_id: str, worker: str) -> bool:
        for it in self.intents:
            if it["id"] == intent_id and it["status"] == "open" and it["worker"] is None:
                it["status"] = "claimed"
                it["worker"] = worker
                return True
        return False

    async def release_intent(self, run_id: str, intent_id: str, worker: str) -> None:
        for it in self.intents:
            if it["id"] == intent_id and it["worker"] == worker:
                it["status"] = "open"
                it["worker"] = None
                return

    async def fail_intent(self, run_id: str, intent_id: str, worker: str) -> None:
        for it in self.intents:
            if it["id"] == intent_id and it["worker"] == worker:
                it["status"] = "failed"
                return

    async def conclude_intent(self, run_id: str, intent_id: str, worker: str, description: str) -> str | None:
        for it in self.intents:
            if it["id"] == intent_id and it["worker"] == worker:
                f = {"id": self._new_id(), "run_id": run_id, "description": description, "source": f"intent:{intent_id}"}
                self.facts.append(f)
                it["status"] = "concluded"
                it["to_fact_id"] = f["id"]
                return f["id"]
        return None

    def __init__(self):
        self.facts: list[dict[str, Any]] = []
        self.intents: list[dict[str, Any]] = []
        self.hints: list[dict[str, Any]] = []
        self.vulns: list[dict[str, Any]] = []

    async def create_vulnerability(self, payload: dict[str, Any]) -> None:
        self.vulns.append({"id": self._new_id(), **payload})

    async def list_vulnerabilities(self, run_id: str) -> list[dict[str, Any]]:
        return [v for v in self.vulns if v.get("run_id") == run_id]

    async def set_vuln_status(self, vuln_id: str, status: str) -> None:
        for v in self.vulns:
            if v["id"] == vuln_id:
                v["status"] = status
                return

    async def set_vuln_severity(self, vuln_id: str, severity: str) -> None:
        for v in self.vulns:
            if v["id"] == vuln_id:
                v["severity"] = severity
                return

    async def create_hint(self, run_id: str, content: str, creator: str = "agent") -> None:
        self.hints.append({"id": self._new_id(), "run_id": run_id, "content": content, "creator": creator})


class ScriptedLLM:
    """Replace core.agent.stream_chat with a scripted responder.

    Branches on whether the first user message is an explore task
    (contains "Current intent") or a reason task (everything else).
    """

    def __init__(self, reason_response: str, explore_response: str):
        self.reason_response = reason_response
        self.explore_response = explore_response

    async def stream_chat(self, system, messages, tools=None, **kwargs):
        first = ""
        if messages:
            c = messages[0].get("content")
            first = c if isinstance(c, str) else ""
        text = self.explore_response if "Current intent" in first else self.reason_response
        # Emit text deltas in two chunks, then a usage event.
        mid = max(1, len(text) // 2)
        yield StreamEvent(type="content_block_delta", data={"delta": {"type": "text_delta", "text": text[:mid]}})
        yield StreamEvent(type="content_block_delta", data={"delta": {"type": "text_delta", "text": text[mid:]}})
        yield StreamEvent(type="usage", data={"input_tokens": 10, "output_tokens": 5,
                                             "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0})

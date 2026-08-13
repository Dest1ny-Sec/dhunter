"""SSE event payload types emitted by the Dhunter agent.

These are the wire-format TypedDicts the Go backend consumes from
`GET /v1/runs/{run_id}/events`. Field names are stable; do not rename
without coordinating with the Go side.
"""

from __future__ import annotations

from typing import Any, Literal, TypedDict

from typing_extensions import NotRequired


class ReasoningDeltaEvent(TypedDict):
    """A chunk of extended-thinking / internal reasoning from the LLM."""

    delta: str
    accumulated: str


class ToolCallEvent(TypedDict):
    """The agent is about to invoke a tool."""

    name: str
    arguments: dict[str, Any]


class ToolResultEvent(TypedDict):
    """A tool returned (or errored)."""

    name: str
    content: str
    is_error: bool
    duration_ms: int


class ResponseDeltaEvent(TypedDict):
    """A chunk of the assistant's user-facing reply."""

    delta: str
    accumulated: str


class MessageDoneEvent(TypedDict):
    """One logical message finished (assistant turn or aggregated tool batch)."""

    role: str
    content: str


class RunDoneEvent(TypedDict):
    """Terminal event for the run. Always last."""

    status: Literal["success", "failed"]
    summary: NotRequired[str]
    error: NotRequired[str]


# Convenience union for typing hints
SSEEvent = (
    ReasoningDeltaEvent
    | ToolCallEvent
    | ToolResultEvent
    | ResponseDeltaEvent
    | MessageDoneEvent
    | RunDoneEvent
)

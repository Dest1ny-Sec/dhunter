"""Centralized logging for the Dhunter agent.

Mirrors Hermes' setup_logging design so a full conversation can be replayed
from the logs:

  * Every log line emitted while a run is active carries that run's id as
    ``[<run_id>]`` — ``grep <run_id> logs/agent.log`` reconstructs a run's
    complete trace (the "view history conversation" story).
  * ``logs/agent.log``   — INFO+, everything (the catch-all).
  * ``logs/errors.log``  — WARNING+, quick triage.
  * RotatingFileHandler caps each file at 5 MiB; secrets (API keys, bearer
    tokens) are redacted before anything hits disk.

Set the per-run context with ``set_run_id(run_id)`` / ``clear_run_id()``;
it lives in a contextvar so concurrent run tasks never cross-contaminate
their log tags.
"""

from __future__ import annotations

import contextvars
import logging
import os
import re
from logging.handlers import RotatingFileHandler
from pathlib import Path

# Per-run context: set at run start, every log line on that task carries it.
run_id_var: contextvars.ContextVar[str] = contextvars.ContextVar("dhunter_run_id", default="")

_LOG_DIR = Path(os.environ.get("DHUNTER_LOG_DIR", str(Path(__file__).resolve().parent.parent / "logs")))

_configured = False


def set_run_id(run_id: str) -> None:
    """Tag subsequent log lines on this task/context with [run_id]."""
    run_id_var.set(run_id or "")


def clear_run_id() -> None:
    run_id_var.set("")


class RunFormatter(logging.Formatter):
    """Appends the active [run_id] and redacts secrets."""

    _SK_RE = re.compile(r"sk-[A-Za-z0-9_\-]{8,}")
    _BEARER_RE = re.compile(r"(Authorization: Bearer\s+)\S+", re.IGNORECASE)
    _KEY_RE = re.compile(r'("?api_?key"?\s*[:=]\s*["\'])[^"\']+', re.IGNORECASE)

    def format(self, record: logging.LogRecord) -> str:
        rid = run_id_var.get()
        record.run_id = f"[{rid}] " if rid else ""
        msg = super().format(record)
        msg = self._SK_RE.sub("sk-***", msg)
        msg = self._BEARER_RE.sub(r"\1***", msg)
        msg = self._KEY_RE.sub(r"\1***", msg)
        return msg


def setup_logging(level: str | None = None) -> str:
    """Idempotently install console + rotating file handlers on the root
    logger. Returns the resolved log directory (for the banner)."""
    global _configured
    if _configured:
        return str(_LOG_DIR)
    _configured = True

    _LOG_DIR.mkdir(parents=True, exist_ok=True)
    level = (level or os.environ.get("DHUNTER_AGENT_LOG_LEVEL", "INFO")).upper()
    fmt = "%(asctime)s %(levelname)s %(name)s :: %(run_id)s%(message)s"

    console = logging.StreamHandler()
    console.setLevel(level)
    console.setFormatter(RunFormatter(fmt))

    agent_fh = RotatingFileHandler(_LOG_DIR / "agent.log", maxBytes=5 * 1024 * 1024, backupCount=5, encoding="utf-8")
    agent_fh.setLevel(level)
    agent_fh.setFormatter(RunFormatter(fmt))

    err_fh = RotatingFileHandler(_LOG_DIR / "errors.log", maxBytes=5 * 1024 * 1024, backupCount=3, encoding="utf-8")
    err_fh.setLevel(logging.WARNING)
    err_fh.setFormatter(RunFormatter(fmt))

    root = logging.getLogger()
    root.setLevel(level)
    root.addHandler(console)
    root.addHandler(agent_fh)
    root.addHandler(err_fh)
    return str(_LOG_DIR)

"""Test bootstrap: make the `agents/` package importable.

The agent runs as `python -m core.server` from the agents/ dir, so the
package root is `agents/`. pytest needs it on sys.path explicitly.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

AGENTS_DIR = Path(__file__).resolve().parent.parent
if str(AGENTS_DIR) not in sys.path:
    sys.path.insert(0, str(AGENTS_DIR))

# Tests must never read a real API key / token from the environment.
for env in ("DHUNTER_LLM_KEY", "DHUNTER_MCP_TOKEN", "DHUNTER_BACKEND_TOKEN"):
    os.environ.pop(env, None)

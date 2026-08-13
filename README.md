# Dhunter

> AI-driven web penetration testing platform. Input a company name or a target URL, watch the agent plan, test, validate, and report.

Dhunter is a commercial-grade red-team productivity tool. It is not a vulnerability scanner: a single (and later multi-) LLM agent drives a curated toolbox to perform **manual-style** reconnaissance, active probing, and exploit verification.

> MVP scope: end-to-end single-agent loop (target parse → AI thinking stream → active testing → vulnerability list → Markdown report).
> Out of MVP scope: attack-chain graph, multi-agent parallelism, report template import (planned for v1.1+).

---

## Highlights

- **Target parser** — accept company name / domain / URL / IP, normalize into a `Target` struct
- **Single-agent loop** — Python FastAPI service that drives an LLM with a curated tool belt
- **Real-time thinking stream** — SSE-pushed `reasoning_delta` / `tool_call` / `tool_result` / `response_delta` events
- **Active testing tools** — HTTP probing, parameter fuzzing, auth bypass, info-leak path discovery
- **Vulnerability store** — SQLite, FK-bound to `Target` and `Run`
- **One-click report** — Markdown export per run
- **Cross-platform** — single static binary for Win/Mac/Linux, no installer, no Python runtime on the user side (Python agent is shipped embedded or run as a sidecar binary)

---

## Architecture (MVP)

```
┌────────────────┐     ┌────────────────┐     ┌─────────────────┐
│  Vue 3 Web UI  │ ←─→ │ Go HTTP+SSE    │ ←─→ │ Python Agent    │
│  (Vite build)  │     │  (Gin)         │     │ (FastAPI + MCP) │
└────────────────┘     └────────────────┘     └─────────────────┘
                              │                       │
                              ↓                       ↓
                       ┌────────────┐         ┌──────────────┐
                       │  SQLite    │         │  MCP tools   │
                       │  targets/  │         │  webhunter/  │
                       │  runs/     │         │  recon/      │
                       │  vulns/    │         │  exploit/    │
                       └────────────┘         └──────────────┘
```

For the MVP, Dhunter **reuses the webhunter tool belt** built on top of `desredteam/cmd/webhunter-mcp` (compatible streamable-HTTP MCP). The tool source is vendored under `internal/mcp/webhunter/` and licensed under Dhunter's commercial terms; the original `desredteam` brand is removed from all user-facing text, logs, and metadata.

---

## Quick start (developer)

```bash
# 1. Build the Go platform
go build -o bin/dhunter-server ./cmd/dhunter-server

# 2. Build the embedded webhunter MCP tool
go build -o bin/dhunter-mcp ./cmd/dhunter-mcp

# 3. Run the Python agent service
cd agents
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python -m core.server          # listens on 127.0.0.1:9100

# 4. Frontend
cd ../frontend
npm install
npm run dev                    # http://127.0.0.1:5173

# 5. Or use the all-in-one launcher
./scripts/run-dev.sh
```

Open <http://127.0.0.1:8080/> (default port; override with `--port`).

---

## Configuration

| Key | Default | Notes |
|---|---|---|
| `server.port` | `8080` | Go HTTP port |
| `server.sse_keepalive_seconds` | `15` | SSE heartbeat |
| `agent.python_url` | `http://127.0.0.1:9100` | Python sidecar |
| `mcp.webhunter.url` | `http://127.0.0.1:9124/message` | Streamable-HTTP MCP |
| `mcp.webhunter.token` | `<random per install>` | bearer token |
| `llm.provider` | `anthropic` | `anthropic` / `openai_compatible` |
| `llm.model` | `claude-sonnet-4-5` | model id |
| `llm.api_key` | (env `DHUNTER_LLM_KEY`) | LLM credential |
| `storage.sqlite_path` | `./data/dhunter.db` | vuln / run store |

---

## License

Commercial. See `LICENSE` for terms. This product is not affiliated with, endorsed by, or derived from any public open-source project under any branding you may have seen before.

---

## Roadmap (post-MVP)

- v1.1 — Multi-agent (Planner / Worker × N / Reviewer / Reporter)
- v1.2 — Attack-chain graph (react-flow + live updates)
- v1.3 — Report templates (import `.md` / `.docx` with placeholders)
- v1.4 — Multi-target runs in parallel
- v2.0 — Team mode, RBAC, audit log, deployment telemetry

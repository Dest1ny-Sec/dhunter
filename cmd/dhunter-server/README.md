# dhunter-server

Go HTTP + SSE host for the Dhunter platform. It owns the SQLite store,
talks to the Python FastAPI agent over HTTP/SSE, and exposes the JSON
API the Vue web UI calls.

## Build

```bash
go build -o bin/dhunter-server ./cmd/dhunter-server
```

The binary is fully static (no CGO) thanks to `modernc.org/sqlite` and
runs on macOS / Linux / Windows without extra runtime dependencies.

## Run

The server reads `configs/dhunter.yaml` by default. Override the path
with `--config`. Override the port with `--port` (or `DHUNTER_PORT`).
Useful flags and env vars:

| Flag         | Env var                              | Default                      |
|--------------|--------------------------------------|------------------------------|
| `--config`   | —                                    | `./configs/dhunter.yaml`     |
| `--port`     | `DHUNTER_PORT`                       | `8080`                       |
| —            | `DHUNTER_AGENT_URL`                  | `http://127.0.0.1:9100`      |
| —            | `DHUNTER_LLM_API_KEY`                | (empty)                      |
| —            | `DHUNTER_SQLITE_PATH`                | `./data/dhunter.db`          |
| —            | `DHUNTER_ADMIN_TOKEN`                | (yaml)                       |
| —            | `DHUNTER_ADMIN_BOOTSTRAP_PASSWORD`   | (yaml)                       |

```bash
./bin/dhunter-server --config ./configs/dhunter.yaml
```

On first launch the server prints an `ADMIN SETUP REQUIRED` banner with
a generated admin password. **Save it** — the plaintext is never
persisted and the bcrypt hash is regenerated on every restart, so the
only way to log in afterwards is to set `admin.bootstrap_password` in
the YAML and reboot.

## API surface

| Method | Path                                  | Notes                                         |
|--------|---------------------------------------|-----------------------------------------------|
| GET    | `/`                                   | Service banner (public)                       |
| GET    | `/api/healthz`                        | Liveness probe (public)                       |
| POST   | `/api/auth/login`                     | Body: `{password}` → `{token}` (public)       |
| POST   | `/api/targets`                        | Create target (auto-detects type)             |
| GET    | `/api/targets`                        | List targets                                  |
| GET    | `/api/targets/:id`                    | One target                                    |
| POST   | `/api/runs`                           | Start a run (forwards to Python agent)        |
| GET    | `/api/runs`                           | List runs                                     |
| GET    | `/api/runs/:id`                       | One run                                       |
| GET    | `/api/runs/:id/messages`              | Streamed messages for the run                 |
| GET    | `/api/runs/:id/vulnerabilities`       | Confirmed vulnerabilities for the run         |
| GET    | `/api/runs/:id/report`                | Markdown report (`text/markdown`)             |
| GET    | `/api/runs/:id/events`                | Live SSE relay from the Python agent          |
| GET    | `/api/vulnerabilities`                | Global list, supports `?run_id`/`?target_id`/`?severity` |

All `/api/...` routes except the public ones require
`Authorization: Bearer <admin_token>` (or `?token=` for SSE).

## Architecture

```
                     ┌──────────────┐
                     │  Vue web UI  │
                     └──────┬───────┘
                            │ HTTP + SSE
                            ▼
┌────────────────────────────────────────────────┐
│  dhunter-server  (this binary)                 │
│                                                │
│   Gin router ─► store ─► SQLite                │
│            │                                   │
│            └─► stream.Hub  ◄───── agent.Bridge │
└─────────────────────┬──────────────────────────┘
                      │  HTTP POST + SSE GET
                      ▼
            ┌─────────────────────┐
            │  Python agent       │
            │  (FastAPI + LLM)    │
            └─────────────────────┘
```

The Go side intentionally has no LLM code: it persists events, fans
them out to subscribers, and exposes the data the UI needs.

## Project layout

```
cmd/dhunter-server/main.go   # entrypoint, banner, graceful shutdown
internal/
  agent/                     # talks to the Python sidecar (HTTP + SSE)
  config/                    # YAML loader
  db/                        # sqlite open + migrations
  handler/                   # gin route handlers
  middleware/                # bearer-token auth
  store/                     # thin data-access over *db.DB
  stream/                    # in-process pub/sub hub
configs/dhunter.yaml         # default config
```

## Smoke test

```bash
go build -o /tmp/dhunter-server ./cmd/dhunter-server
/tmp/dhunter-server --port 18999 &

curl -s http://127.0.0.1:18999/api/healthz
# → {"status":"ok"}

curl -s -H "Authorization: Bearer dhunter-admin-please-change-me" \
     -H "Content-Type: application/json" \
     -X POST -d '{"input":"https://example.com","type":"auto"}' \
     http://127.0.0.1:18999/api/targets
```

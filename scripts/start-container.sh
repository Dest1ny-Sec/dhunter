#!/bin/bash
# start-container.sh — entrypoint for the Docker image.
# Brings up: dhunter-mcp (9124) -> python agent (9100) -> dhunter-server (13343).
# All three run in one container; SIGTERM is forwarded so `docker stop` is clean.
set -euo pipefail

BIN=/app/bin
AGENT_DIR=/app/agents
PLAT_PORT=${DHUNTER_SERVER_PORT:-13343}
AGENT_PORT=${DHUNTER_AGENT_PORT:-9100}
MCP_PORT=${DHUNTER_MCP_PORT:-9124}
LLM_KEY=${DHUNTER_LLM_KEY:-}

# Tokens: prefer env, else generate once and persist.
PLAT_TOKEN=${DHUNTER_ADMIN_TOKEN:-$(openssl rand -hex 16)}
MCP_TOKEN=${DHUNTER_MCP_TOKEN:-$(openssl rand -hex 16)}
export DHUNTER_ADMIN_TOKEN="$PLAT_TOKEN"

echo "== dhunter container start =="
echo "  server :$PLAT_PORT  agent :$AGENT_PORT  mcp :$MCP_PORT"

start_mcp() {
  "$BIN/dhunter-mcp" -addr "0.0.0.0:$MCP_PORT" -t "$MCP_TOKEN" &
  MCP_PID=$!
  echo "  [mcp] pid $MCP_PID"
}

start_agent() {
  cd "$AGENT_DIR"
  DHUNTER_LLM_KEY="$LLM_KEY" \
  DHUNTER_AGENT_TOKEN="$PLAT_TOKEN" \
  DHUNTER_MCP_URL="http://127.0.0.1:$MCP_PORT/message" \
  DHUNTER_MCP_TOKEN="$MCP_TOKEN" \
  DHUNTER_BACKEND_URL="http://127.0.0.1:$PLAT_PORT" \
  DHUNTER_BACKEND_TOKEN="$PLAT_TOKEN" \
    nohup python3 -m uvicorn core.server:app --host 127.0.0.1 --port "$AGENT_PORT" &
  AGENT_PID=$!
  echo "  [agent] pid $AGENT_PID"
}

start_server() {
  cd /app
  DHUNTER_ADMIN_TOKEN="$PLAT_TOKEN" \
  DHUNTER_AGENT_TOKEN="$PLAT_TOKEN" \
  DHUNTER_MCP_TOKEN="$MCP_TOKEN" \
    "$BIN/dhunter-server" --config /app/configs/dhunter.yaml --http &
  SERVER_PID=$!
  echo "  [server] pid $SERVER_PID"
}

stop_all() {
  kill "$SERVER_PID" "$AGENT_PID" "$MCP_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  echo "== container stopped =="
}
trap stop_all TERM INT

start_mcp
start_agent
start_server

# Keep the entrypoint alive and forward signals.
while true; do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "[server] exited; shutting down"
    exit 1
  fi
  sleep 2
done

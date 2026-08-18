#!/bin/bash
# start-dhunter.sh - Mac 一键启动 Dhunter 三件套
# 启动顺序: dhunter-mcp (9124) → Python agent (9100) → Dhunter server (13343)
# 用法: ./scripts/start-dhunter.sh {start|stop|restart|status|logs}
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# 内置扫描器目录 (setup-tools.sh 安装的 subfinder/httpx/katana... + 随仓库 exe)
export PATH="$ROOT/tools/bin:$PATH"

PLAT_BIN="/tmp/dhunter-server"
MCP_BIN="/tmp/dhunter-mcp"
AGENT_DIR="$ROOT/agents"
AGENT_PORT=9100
PLAT_PORT=13343
MCP_PORT=9124

# Tokens are generated once and persisted to .dhunter.tokens so restarts
# keep the same credentials (no more hardcoded defaults). Re-run `make
# tokens` or delete the file to rotate.
TOKEN_FILE="$ROOT/.dhunter.tokens"
if [ -f "$TOKEN_FILE" ]; then
  # shellcheck disable=SC1090
  source "$TOKEN_FILE"
else
  PLAT_TOKEN="$(openssl rand -hex 16)"
  MCP_TOKEN="$(openssl rand -hex 16)"
  umask 077
  printf 'PLAT_TOKEN=%s\nMCP_TOKEN=%s\n' "$PLAT_TOKEN" "$MCP_TOKEN" > "$TOKEN_FILE"
  echo "→ 首次运行:已生成随机 token,保存在 $TOKEN_FILE"
fi

LLM_KEY="${DHUNTER_LLM_KEY:-}"
if [ -z "$LLM_KEY" ]; then
  echo "⚠️  DHUNTER_LLM_KEY not set, LLM calls will fail"
fi

ok=0; fail=0
okln() { printf "  \033[32m✅\033[0m %s\n" "$1"; }
errln() { printf "  \033[31m❌\033[0m %s\n" "$1"; }
sec() { printf "\n\033[1;36m=== %s ===\033[0m\n" "$1"; }

build_if_needed() {
  if [ ! -x "$PLAT_BIN" ]; then
    echo "→ 编译 dhunter-server..."
    (cd "$ROOT" && go build -o "$PLAT_BIN" ./cmd/dhunter-server)
  fi
  if [ ! -x "$MCP_BIN" ]; then
    echo "→ 编译 dhunter-mcp..."
    (cd "$ROOT" && go build -o "$MCP_BIN" ./cmd/dhunter-mcp)
  fi
}

# Build frontend dist if missing. The repo normally ships a pre-built dist so
# this is a safety net for the case where the user wiped it or pulled an
# older commit. Without dist the Go server logs "no web UI found — only API
# is served" and the page is blank.
build_frontend_if_needed() {
  if [ -f "$ROOT/frontend/dist/index.html" ]; then
    return 0
  fi
  if ! command -v npm >/dev/null 2>&1; then
    errln "frontend/dist 缺失但未检测到 npm — 无法自动构建, 启动后只能访问 API"
    errln "在有 Node.js 的机器上跑: cd frontend && npm install && npm run build"
    return 1
  fi
  echo "→ 构建前端 (frontend/dist 缺失)..."
  if [ ! -d "$ROOT/frontend/node_modules" ]; then
    (cd "$ROOT/frontend" && npm install --no-audit --no-fund) || return 1
  fi
  (cd "$ROOT/frontend" && npm run build) || return 1
  okln "前端构建完成"
}

start_all() {
  build_if_needed
  build_frontend_if_needed
  echo "=== 启动顺序: MCP (9124) → Python agent (9100) → Dhunter server ($PLAT_PORT) ==="

  # 1. MCP
  if lsof -ti :$MCP_PORT >/dev/null 2>&1; then
    echo "[skip] dhunter-mcp :$MCP_PORT 已在跑"
  else
    nohup "$MCP_BIN" -addr "0.0.0.0:$MCP_PORT" -t "$MCP_TOKEN" > /tmp/dhunter-mcp.log 2>&1 &
    echo "[ok]   dhunter-mcp :$MCP_PORT (PID $!)"
  fi

  # 2. Python agent
  if lsof -ti :$AGENT_PORT >/dev/null 2>&1; then
    echo "[skip] python agent :$AGENT_PORT 已在跑"
  else
    cd "$AGENT_DIR"
    DHUNTER_LLM_KEY="$LLM_KEY" \
    DHUNTER_AGENT_TOKEN="$PLAT_TOKEN" \
    DHUNTER_MCP_URL="http://127.0.0.1:$MCP_PORT/message" \
    DHUNTER_MCP_TOKEN="$MCP_TOKEN" \
    DHUNTER_BACKEND_URL="http://127.0.0.1:$PLAT_PORT" \
    DHUNTER_BACKEND_TOKEN="$PLAT_TOKEN" \
    nohup python3 -m uvicorn core.server:app --host 127.0.0.1 --port $AGENT_PORT > /tmp/dhunter-agent.log 2>&1 &
    echo "[ok]   python agent :$AGENT_PORT (PID $!)"
    cd "$ROOT"
  fi

  # 3. Dhunter server (admin token overridden from .dhunter.tokens)
  if lsof -ti :$PLAT_PORT >/dev/null 2>&1; then
    echo "[skip] Dhunter :$PLAT_PORT 已在跑"
  else
    DHUNTER_ADMIN_TOKEN="$PLAT_TOKEN" \
    DHUNTER_AGENT_TOKEN="$PLAT_TOKEN" \
    DHUNTER_MCP_TOKEN="$MCP_TOKEN" \
      nohup "$PLAT_BIN" --config "$ROOT/configs/dhunter.yaml" --http > /tmp/dhunter-server.log 2>&1 &
    echo "[ok]   Dhunter :$PLAT_PORT (PID $!)"
  fi

  sleep 3
  status_all
}

stop_all() {
  for entry in \
    "Dhunter:$PLAT_PORT:dhunter-server" \
    "python-agent:$AGENT_PORT:core.server" \
    "dhunter-mcp:$MCP_PORT:dhunter-mcp"; do
    name="${entry%%:*}"; rest="${entry#*:}"; port="${rest%%:*}"; bin="${rest##*:}"
    pid="$(lsof -ti :$port 2>/dev/null || true)"
    if [ -n "$pid" ]; then
      kill "$pid" 2>/dev/null || true
      echo "[stop] $name :$port (PID $pid)"
    else
      echo "[skip] $name :$port 未在跑"
    fi
  done
}

status_all() {
  sec "📊 Dhunter 状态"
  for p in $PLAT_PORT $MCP_PORT $AGENT_PORT; do
    name="?"
    case "$p" in
      $PLAT_PORT) name="Dhunter server" ;;
      $MCP_PORT) name="dhunter-mcp" ;;
      $AGENT_PORT) name="python-agent" ;;
    esac
    if lsof -ti :$p >/dev/null 2>&1; then
      pid=$(lsof -ti :$p | head -1)
      okln "$name :$p up (PID $pid)"
    else
      errln "$name :$p down"
    fi
  done
  echo ""
  echo "Web UI:    http://127.0.0.1:$PLAT_PORT/"
  echo "Agent API: http://127.0.0.1:$AGENT_PORT/healthz"
  echo "MCP:       http://127.0.0.1:$MCP_PORT/healthz"
  echo ""
  if [ -f /tmp/dhunter-server.log ]; then
    ADMIN_PW=$(grep "password:" /tmp/dhunter-server.log 2>/dev/null | head -1 | awk '{print $2}')
    if [ -n "$ADMIN_PW" ]; then
      echo "Admin password (from latest log): $ADMIN_PW"
    fi
  fi
  echo ""
  echo "→ Smoke test: ./scripts/smoke-test.sh"
  echo "→ Logs: tail -f /tmp/dhunter-{server,agent,mcp}.log"
}

# watch_all — 简易 watchdog：前台循环检测三件套，任何一个挂了自动拉起并记录重启次数。
# 生产环境建议改用 systemd / supervisor / docker restart=always。
watch_all() {
  echo "== Dhunter watchdog 启动（Ctrl+C 退出）=="
  declare -A RESPAWN
  while true; do
    # 1. MCP (19124 → 由 token 文件决定端口？固定 9124)
    if ! lsof -ti :$MCP_PORT >/dev/null 2>&1; then
      nohup "$MCP_BIN" -addr "0.0.0.0:$MCP_PORT" -t "$MCP_TOKEN" > /tmp/dhunter-mcp.log 2>&1 &
      RESPAWN[mcp]=$(( ${RESPAWN[mcp]:-0} + 1 ))
      echo "[watch] $(date '+%H:%M:%S') dhunter-mcp 已重启 (第 ${RESPAWN[mcp]} 次)"
    fi
    # 2. Python agent (9100)
    if ! lsof -ti :$AGENT_PORT >/dev/null 2>&1; then
      cd "$AGENT_DIR"
      DHUNTER_AGENT_TOKEN="$PLAT_TOKEN" \
      DHUNTER_MCP_URL="http://127.0.0.1:$MCP_PORT/message" \
      DHUNTER_MCP_TOKEN="$MCP_TOKEN" \
      DHUNTER_BACKEND_URL="http://127.0.0.1:$PLAT_PORT" \
      DHUNTER_BACKEND_TOKEN="$PLAT_TOKEN" \
        nohup python3 -m uvicorn core.server:app --host 127.0.0.1 --port $AGENT_PORT > /tmp/dhunter-agent.log 2>&1 &
      cd "$ROOT"
      RESPAWN[agent]=$(( ${RESPAWN[agent]:-0} + 1 ))
      echo "[watch] $(date '+%H:%M:%S') python-agent 已重启 (第 ${RESPAWN[agent]} 次)"
    fi
    # 3. Go server (13343)
    if ! lsof -ti :$PLAT_PORT >/dev/null 2>&1; then
      DHUNTER_ADMIN_TOKEN="$PLAT_TOKEN" \
      DHUNTER_AGENT_TOKEN="$PLAT_TOKEN" \
      DHUNTER_MCP_TOKEN="$MCP_TOKEN" \
        nohup "$PLAT_BIN" --config "$ROOT/configs/dhunter.yaml" --http > /tmp/dhunter-server.log 2>&1 &
      RESPAWN[server]=$(( ${RESPAWN[server]:-0} + 1 ))
      echo "[watch] $(date '+%H:%M:%S') dhunter-server 已重启 (第 ${RESPAWN[server]} 次)"
    fi
    sleep 5
  done
}

case "${1:-start}" in
  start)   start_all ;;
  stop)    stop_all ;;
  restart) stop_all; sleep 2; start_all ;;
  status)  status_all ;;
  logs)    tail -f /tmp/dhunter-server.log /tmp/dhunter-agent.log /tmp/dhunter-mcp.log ;;
  watch)   watch_all ;;
  *) echo "用法: $0 {start|stop|restart|status|logs|watch}"; exit 1 ;;
esac
  logs)    tail -f /tmp/dhunter-server.log /tmp/dhunter-agent.log /tmp/dhunter-mcp.log ;;
  *) echo "用法: $0 {start|stop|restart|status|logs}"; exit 1 ;;
esac

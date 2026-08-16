#!/bin/bash
# dev-env.sh — 为「手动启动」生成/复用一致的三进程 token。
#
# 用法（先 source，再按 README 手动启动步骤执行）:
#   source scripts/dev-env.sh
#
# 与 start-dhunter.sh 共用同一个 .dhunter.tokens，因此手动/脚本两种启动
# 方式不会出现 token 不一致导致 agent 静默降级的问题。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOKEN_FILE="$ROOT/.dhunter.tokens"

if [ -f "$TOKEN_FILE" ]; then
  # shellcheck disable=SC1090
  source "$TOKEN_FILE"
else
  PLAT_TOKEN="$(openssl rand -hex 16)"
  MCP_TOKEN="$(openssl rand -hex 16)"
  umask 077
  printf 'PLAT_TOKEN=%s\nMCP_TOKEN=%s\n' "$PLAT_TOKEN" "$MCP_TOKEN" > "$TOKEN_FILE"
  echo "→ 首次运行: 已生成随机 token，保存在 $TOKEN_FILE"
fi

# 三进程一个标准：agent 复用平台 admin token，MCP 用独立 token。
export DHUNTER_ADMIN_TOKEN="$PLAT_TOKEN"
export DHUNTER_AGENT_TOKEN="$PLAT_TOKEN"
export DHUNTER_MCP_TOKEN="$MCP_TOKEN"
echo "→ 已设置 DHUNTER_ADMIN_TOKEN / DHUNTER_AGENT_TOKEN / DHUNTER_MCP_TOKEN（手动启动可直接用）"

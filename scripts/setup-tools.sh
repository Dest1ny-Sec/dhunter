#!/bin/bash
# setup-tools.sh — macOS/Linux 一键安装 Dhunter 外部扫描器依赖
# 输出到 tools/bin（启动脚本会自动把它加入 PATH）。
# 用法: ./scripts/setup-tools.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TOOLBIN="$ROOT/tools/bin"
mkdir -p "$TOOLBIN"

echo "════════════════════════════════════════════"
echo " Dhunter 工具依赖安装 (macOS/Linux)"
echo "════════════════════════════════════════════"

# 1. 基础网络扫描器 (Homebrew)
echo ""
echo "→ [1/3] 基础工具 (nmap)"
if command -v brew >/dev/null 2>&1; then
  brew list nmap >/dev/null 2>&1 || brew install nmap
  echo "   ✓ nmap"
else
  echo "   ⚠️ 未检测到 brew，跳过 nmap（可手动安装或改用其他扫描）"
fi

# 2. Go 扫描器 (go install → tools/bin)
echo ""
echo "→ [2/3] Go 扫描器 (go install)"
for pkg in \
  "github.com/projectdiscovery/httpx/cmd/httpx@latest" \
  "github.com/projectdiscovery/subfinder/v2/cmd/subfinder@latest" \
  "github.com/projectdiscovery/katana/cmd/katana@latest" \
  "github.com/lc/gau/v2/cmd/gau@latest" \
  "github.com/tomnomnom/waybackurls@latest" \
  "github.com/tomnomnom/assetfinder@latest"; do
  name="$(basename "$(dirname "$pkg")")"
  if [ -x "$TOOLBIN/$name" ]; then
    echo "   ✓ $name (已存在)"
    continue
  fi
  echo "   → 安装 $name ..."
  GOBIN="$TOOLBIN" go install "$pkg"
done

# 3. Python 工具 (可选)
echo ""
echo "→ [3/3] Python 工具 (arjun/uro, 可选)"
if python3 -c "import arjun" 2>/dev/null; then
  echo "   ✓ arjun"
else
  pip3 install -q arjun uro 2>/dev/null && echo "   ✓ arjun + uro" || echo "   ⚠️ pip 安装跳过（可选工具，不影响核心功能）"
fi

echo ""
echo "✅ 完成。启动脚本会自动把 tools/bin 加入 PATH。"
echo "   缺失的扫描器平台会自动跳过/改用替代方案。"

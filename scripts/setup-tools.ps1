# setup-tools.ps1 — Windows 一键安装 Dhunter 外部扫描器依赖
# 输出到 tools\bin（启动脚本会自动把它加入 PATH）。
# 用法: powershell -ExecutionPolicy Bypass -File scripts\setup-tools.ps1
$ErrorActionPreference = "Stop"
$Root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$ToolBin = Join-Path $Root "tools\bin"
New-Item -ItemType Directory -Force -Path $ToolBin | Out-Null

Write-Host "════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host " Dhunter 工具依赖安装 (Windows)" -ForegroundColor Cyan
Write-Host "════════════════════════════════════════════" -ForegroundColor Cyan

# 0. 仓库已随附的可复用工具 (tools/bin 下的 exe)
Write-Host "`n→ [0/4] 随仓库自带工具"
Get-ChildItem $ToolBin -Filter *.exe -ErrorAction SilentlyContinue | ForEach-Object {
  Write-Host "   ✓ $($_.Name)" -ForegroundColor Green
}

# 1. Go 扫描器 (go install → tools\bin)
Write-Host "`n→ [1/4] Go 扫描器 (go install)"
$env:GOBIN = $ToolBin
$packages = @(
  "github.com/projectdiscovery/httpx/cmd/httpx@latest",
  "github.com/projectdiscovery/subfinder/v2/cmd/subfinder@latest",
  "github.com/projectdiscovery/katana/cmd/katana@latest",
  "github.com/lc/gau/v2/cmd/gau@latest",
  "github.com/tomnomnom/waybackurls@latest",
  "github.com/tomnomnom/assetfinder@latest"
)
foreach ($pkg in $packages) {
  $name = ($pkg -split '/')[-2]
  if (Test-Path (Join-Path $ToolBin "$name.exe")) {
    Write-Host "   ✓ $name (已存在)" -ForegroundColor Green
    continue
  }
  Write-Host "   → 安装 $name ..."
  go install $pkg
}

# 2. nmap
Write-Host "`n→ [2/4] nmap (端口扫描)"
if (Get-Command nmap -ErrorAction SilentlyContinue) {
  Write-Host "   ✓ nmap 已在 PATH" -ForegroundColor Green
} else {
  Write-Host "   ⚠️ 未安装 nmap。请安装: https://nmap.org/download.html (或 winget install Insecure.Nmap)"
  Write-Host "     (可选工具，缺失时平台自动跳过)"
}

# 3. Python 工具 (可选)
Write-Host "`n→ [3/4] Python 工具 (arjun/uro, 可选)"
try {
  python -m pip install -q arjun uro
  Write-Host "   ✓ arjun + uro" -ForegroundColor Green
} catch {
  Write-Host "   ⚠️ pip 安装跳过（可选工具）"
}

# 4. 提示
Write-Host "`n→ [4/4] 完成" -ForegroundColor Green
Write-Host "启动脚本会自动把 tools\bin 加入 PATH。缺失的扫描器平台会自动跳过/改用替代方案。"

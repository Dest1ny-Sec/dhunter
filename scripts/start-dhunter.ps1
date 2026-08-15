# Dhunter Windows launcher (PowerShell 5.1+)
# Brings up: dhunter-mcp -> python agent -> dhunter-server, then opens the UI.
# Usage:  powershell -ExecutionPolicy Bypass -File .\scripts\start-dhunter.ps1 {start|stop|status}
param([string]$Action = "start")
$ErrorActionPreference = "Stop"
$Root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent   # repo root
Set-Location $Root

# 内置扫描器目录 (setup-tools.ps1 安装的 subfinder/httpx/katana... + 随仓库 exe)
$env:PATH = (Join-Path $Root "tools\bin") + ";" + $env:PATH

$PlatPort = 13343
$AgentPort = 9100
$McpPort = 9124
$PlatBin = Join-Path $Root "bin\dhunter-server.exe"
$McpBin  = Join-Path $Root "bin\dhunter-mcp.exe"
$AgentDir = Join-Path $Root "agents"

# tokens: reuse from a file, else generate
$TokenFile = Join-Path $Root ".dhunter.tokens"
if (Test-Path $TokenFile) {
  $tokens = Get-Content $TokenFile | ConvertFrom-StringData
  $platToken = $tokens["PLAT_TOKEN"]; $mcpToken = $tokens["MCP_TOKEN"]
} else {
  $platToken = -join ((48..57)+(65..90)+(97..122) | Get-Random -Count 24 | %{[char]$_})
  $mcpToken  = -join ((48..57)+(65..90)+(97..122) | Get-Random -Count 24 | %{[char]$_})
  Set-Content $TokenFile "PLAT_TOKEN=$platToken`nMCP_TOKEN=$mcpToken"
}

function IsPortFree([int]$port) { -not (Get-NetTCPConnection -LocalPort $port -ErrorAction SilentlyContinue) }

function Start-One([string]$name, [string]$cmd, [string]$args, [string]$log, [int]$port) {
  if (-not (IsPortFree $port)) { Write-Host "[skip] $name :$port already running" -ForegroundColor DarkGray; return }
  Start-Process -FilePath $cmd -ArgumentList $args -WindowStyle Hidden -RedirectStandardOutput $log -RedirectStandardError "$log.err"
  Write-Host "[ok] $name :$port" -ForegroundColor Green
}

function Start-All {
  Write-Host "== Dhunter Windows 启动 =="
  # 0. build if missing (cross-compile is pure Go, no CGO)
  if (-not (Test-Path $PlatBin)) { go build -o $PlatBin .\cmd\dhunter-server }
  if (-not (Test-Path $McpBin))  { go build -o $McpBin  .\cmd\dhunter-mcp }

  # 1. MCP
  Start-One "dhunter-mcp" $McpBin "-addr 0.0.0.0:$McpPort -t $mcpToken" (Join-Path $Root "data\mcp.log") $McpPort
  # 2. Python agent
  $env:DHUNTER_LLM_KEY = $env:DHUNTER_LLM_KEY
  $env:DHUNTER_MCP_URL = "http://127.0.0.1:$McpPort/message"
  $env:DHUNTER_MCP_TOKEN = $mcpToken
  $env:DHUNTER_BACKEND_URL = "http://127.0.0.1:$PlatPort"
  $env:DHUNTER_BACKEND_TOKEN = $platToken
  if (IsPortFree $AgentPort) {
    Start-Process -FilePath "python" -ArgumentList "-m","uvicorn","core.server:app","--host","127.0.0.1","--port","$AgentPort" -WorkingDirectory $AgentDir -WindowStyle Hidden -RedirectStandardOutput (Join-Path $Root "data\agent.log") -RedirectStandardError (Join-Path $Root "data\agent.log.err")
    Write-Host "[ok] python agent :$AgentPort" -ForegroundColor Green
  } else { Write-Host "[skip] python agent :$AgentPort" -ForegroundColor DarkGray }
  # 3. server (env token override)
  if (IsPortFree $PlatPort) {
    $env:DHUNTER_ADMIN_TOKEN = $platToken
    Start-Process -FilePath $PlatBin -ArgumentList "--config", (Join-Path $Root "configs\dhunter.yaml") -WindowStyle Hidden -RedirectStandardOutput (Join-Path $Root "data\server.log") -RedirectStandardError (Join-Path $Root "data\server.log.err")
    Write-Host "[ok] dhunter-server :$PlatPort" -ForegroundColor Green
  } else { Write-Host "[skip] dhunter-server :$PlatPort" -ForegroundColor DarkGray }

  Start-Sleep 3
  Write-Host ""
  Write-Host "  Web UI: http://127.0.0.1:$PlatPort/" -ForegroundColor Cyan
  Write-Host "  日志:   data\*.log"
  Start-Process "http://127.0.0.1:$PlatPort/"
}

function Stop-All {
  foreach ($port in @($PlatPort, $AgentPort, $McpPort)) {
    $conn = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue
    if ($conn) { $conn | ForEach-Object { Stop-Process -Id $_.OwningProcess -Force -ErrorAction SilentlyContinue }; Write-Host "[stop] :$port" }
  }
}

function Status {
  foreach ($port in @($PlatPort, $AgentPort, $McpPort)) {
    if (IsPortFree $port) { Write-Host "  :$port down" -ForegroundColor Red }
    else { Write-Host "  :$port up" -ForegroundColor Green }
  }
}

switch ($Action) {
  "start"  { Start-All }
  "stop"   { Stop-All }
  "status" { Status }
  default  { Write-Host "用法: start-dhunter.ps1 {start|stop|status}" }
}

# ADR-0001: 三进程架构（Go server / Python agent / Go MCP）

- 状态：已接受
- 日期：2026-08-18

## 背景
需要同时满足：单机一键启动、跨平台（macOS/Linux/Windows 无 CGO）、LLM agent 长连接流式交互、工具集可独立演进。

## 决策
拆成三个独立进程，各自专职：
1. **dhunter-server**（Go）：HTTP API + SSE 中继 + SQLite 持久化 + SPA 托管；
2. **dhunter-agent**（Python）：黑板调度器（reason/explore/verifier），持有 LLM 长连接；
3. **dhunter-mcp**（Go）：MCP 工具集（侦察/主动测试/记录），被 agent 经 streamable-HTTP 调用。

进程间仅通过 HTTP（Go↔Python SSE、agent↔MCP JSON-RPC），无共享内存。三进程统一 Bearer token 鉴权（`DHUNTER_AGENT_TOKEN` / `DHUNTER_MCP_TOKEN`）。

## 备选方案
- **单体**：Go 内嵌 Python（CGO）或重写 agent——CGO 破坏"纯静态二进制"，重写成本高。
- **双进程**（server+agent 合并 MCP）——工具集与调度器解耦是独立演进的前提。

## 后果
- 优点：每进程可独立重启/升级；语言各取所长（Go 并发+静态、Python LLM 生态）。
- 缺点：三进程编排复杂 → 由 `scripts/start-dhunter.sh` 统一管理，并提供 `watch` watchdog；
  进程间故障需要降级路径（MCP 挂了 agent 自动回落 fallback 工具）。

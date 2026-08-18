# ADR-0004: 工具集走 MCP streamable-HTTP（自研 toolbelt + 可选外部二进制）

- 状态：已接受
- 日期：2026-08-18

## 背景
agent 需要 20+ 侦察/主动测试工具；未来可能接入第三方 MCP（如 Luvv-MCP 前端分析引擎）。

## 决策
工具以 JSON-RPC 2.0 over HTTP（`/message`）暴露，Bearer 鉴权；agent 经 MCP client 调用。
内置工具全部自研（不 vendored）；外部扫描器（subfinder/httpx/katana/...）为可选依赖，
缺失时返回"[工具 xxx 未安装] 已跳过"并自动降级。
安全约束：`safeExec` 只接受 PATH 上的裸命令名（拒绝路径），LLM 不可指定任意可执行文件；
外部命令以数组参数执行（无 shell 注入）。

## 备选方案
- 直接函数调用（进程内）：无法接第三方 MCP 生态。
- stdio MCP：需 agent 管理子进程生命周期；HTTP 与现有 Go server 集成更顺。

## 后果
- 优点：工具与调度解耦、可插第三方 MCP、鉴权统一。
- 缺点：需要工具可用性探测（/availability）告知用户哪些外部依赖缺失。

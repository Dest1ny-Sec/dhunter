# Dhunter — Mac 端到端验收 & Bug Hunt 报告

> **生成时间**: 2026-08-13
> **范围**: 全栈 (Go server / Python agent / Vue 前端 / SQLite)
> **目标**: 真实漏洞挖掘 + UI 验收 + token 计数 + 项目会话

---

## ✅ 验收清单

| 功能点 | 状态 | 备注 |
|---|---|---|
| 端到端目标 → AI 思考 → 主动测试 → 漏洞入库 → Markdown 报告 | ✅ | Juice Shop 3 分 36 秒挖出 6 个真实漏洞 |
| Token 计数 (in / out / cache) | ✅ | Anthropic usage 解析 → SSE emit → Go bridge 累加 |
| Token 在 UI 上可见 | ✅ | RunsView 列 + RunDetailView header 实时显示 |
| 项目会话 (Project session) | ✅ | `GET /api/targets/:id/runs` + TargetsView "View runs" 按钮 |
| MCP 切换丝滑 | ✅ | LLM 主动调用 `http_request` / `write_finding` / `waf_detect` |
| 工具智能调度 | ✅ | LLM 8 轮调 70 个 tool,自动选最合适的工具 |
| AI 会话日志 | ✅ | `messages` 表 + UI EventStream 实时渲染 |
| Web UI 单端口访问 | ✅ | Go server 13343 同时 serve API + 前端 SPA |

---

## 🐛 Bug Hunt 修复清单 (本轮 13 个)

### 数据层 (3)

| # | Bug | 修复 |
|---|---|---|
| 1 | **DB 路径漂移** — config 写相对路径 `./data/dhunter.db`,server 实际在 desredteam/ 启动 → 写到 desredteam/data/dhunter.db | 改绝对路径 `/Users/destiny/Downloads/dhunter/data/dhunter.db` |
| 2 | **两份独立 db** — dhunter/data/dhunter.db 和 desredteam/data/dhunter.db 数据分裂 | `ATTACH + INSERT OR IGNORE` 一次性合并(4→17 runs, 15→22 vulns) |
| 3 | **dd6f3b66 数据 "丢失"** — 实际是 2 号 bug 的副作用,合并后已恢复 | 132 tool_calls + 8 vulns 全部可查 |

### API 层 (2)

| # | Bug | 修复 |
|---|---|---|
| 4 | **POST /api/runs 字段不一致** — 返 `run_id`,GET 返 `id` | 同时返 `id` 和 `run_id`(兼容老客户端) |
| 5 | **Go server 不 serve 静态文件** — 前端无法访问 | 加 `mountWebUI()`:自动找 frontend/dist,NoRoute 走 index.html SPA fallback |

### 前端层 (6)

| # | Bug | 修复 |
|---|---|---|
| 6 | **VulnsView 路径错** — `api.get('/vulns')` → 404 → 显示 0 条 | 改 `/vulnerabilities` |
| 7 | **TargetsView 字段名错** — POST 用 `value`,server 期望 `input` | 改 `input` |
| 8 | **TargetsView 缺 target 列表** — 只显示创建表单 | 加 Recent targets table(28 个) |
| 9 | **RunDetailView SSE 不带 token** — `EventSource` 不支持 header,server 拒 | URL 加 `?token=${localStorage.token}` |
| 10 | **RunDetailView 不显示 token** | header 加 in/out/cache chip |
| 11 | **RunDetailView loadRun 假定 res.data.vulns 存在** | 分两个端点 `/runs/:id/vulnerabilities` + `/runs/:id/report` |
| 12 | **RunsView 不显示 token** | 加 `Tokens (in / out / cache)` 列 |

### Agent/MCP (2)

| # | Bug | 修复 |
|---|---|---|
| 13 | **POST /api/runs 返 `run_id` 老 API**,Python write_finding 拿 `current_run_id` 正常但调试困难 | 与 #4 一致(同时返 id) |

---

## 🎯 真实漏洞挖掘 (Juice Shop, 3'36")

**Run ID**: `0abed0cf-b20e-47c9-87bc-3dfcdd69630d`
**Status**: completed
**Tokens**: in 67,694 / out 10,787 / cache 958,466
**Tool calls**: 70 (60 http_request, 8 write_finding, 2 waf_detect)

### 挖出的 6 个真实漏洞 (按严重度排序)

| # | Severity | Title | PoC |
|---|---|---|---|
| 1 | **CRITICAL** | SQL Injection in /rest/user/login (SQLite UNION-based auth bypass) | `curl -X POST .../rest/user/login -d '{"email":"'\'' OR 1=1--","password":"x"}'` |
| 2 | **CRITICAL** | Unauthenticated access to admin application configuration | `curl .../rest/admin/application-configuration` → 200 + 23KB JSON 含 OAuth client_id |
| 3 | **CRITICAL** | Admin credential and weak password hashing (admin@juice-sh.op / admin123) | `curl -X POST .../rest/user/login -d '{"email":"admin@juice-sh.op","password":"admin123"}'` |
| 4 | **CRITICAL** | SQL Injection in login allows impersonation of any user by email | `curl -X POST .../rest/user/login -d '{"email":"admin@juice-sh.op'\'' --","password":"x"}'` |
| 5 | **HIGH** | Sensitive file disclosure via path traversal listing at /ftp | `curl .../ftp` → 暴露 incident-support.kdbx, encrypt.pyc, eastere.gg |
| 6 | **LOW** | Stack-trace information disclosure on unhandled errors | `curl .../api/` → 500 + Express 堆栈 |

> **每个漏洞都带可复现的 curl PoC,AI 真的像人一样在测**(枚举 endpoint → 试 SQLi → 试 admin 弱口令 → 试目录遍历)

---

## 🖥️ UI 截图

* `/tmp/dhunter-login.png` — 登录页(深色主题,品牌色 #3b82f6 蓝)
* `/tmp/dhunter-targets.png` — Targets 页(28 个 recent + New Target 表单 + Objective 输入)
* `/tmp/dhunter-runs.png` — Runs 页(17 个 run + token 列 + 时间)
* `/tmp/dhunter-vulns.png` — Vulnerabilities 页(32 个漏洞,severity badge + PoC)
* `/tmp/dhunter-run-detail.png` — Run detail 页(完整 Markdown 报告 + 6 个漏洞 + token in/out/cache)

---

## 🔧 服务/端口

| 服务 | 端口 | 状态 |
|---|---|---|
| dhunter-server (Go) | 13343 | ✅ |
| Python agent (uvicorn) | 9100 | ✅ |
| dhunter-mcp (webhunter) | 9124 | ✅ |
| Juice Shop (docker) | 3000 | ✅ |
| Web UI | 13343 | ✅(单端口) |

**Admin token**: `dhunter-admin-please-change-me`
**Admin password**: 每次启动时 banner 显示 (如 `2a89b7d02cc3900f4e7db2eb1910d7ad`)

---

## 📋 启动 / 重启 / 停止

```bash
cd /Users/destiny/Downloads/dhunter
DHUNTER_LLM_KEY="sk-cp-..." ./scripts/start-dhunter.sh start   # 启动三件套
./scripts/start-dhunter.sh stop                                  # 停
./scripts/start-dhunter.sh restart                               # 重启
./scripts/start-dhunter.sh status                                # 状态
./scripts/start-dhunter.sh logs                                  # 实时日志
```

**Smoke test**: `./scripts/smoke-test.sh`

---

## 📁 关键文件

- `cmd/dhunter-server/main.go` — 服务入口 + `mountWebUI()` 静态服务
- `internal/agent/bridge.go` — Go ↔ Python SSE 桥,`bumpRunTokens()` 累加 token
- `internal/agent/sse.go` — SSE reader 解析 `event:` 行
- `internal/handler/run.go` — POST /api/runs 返 `id` + `run_id`
- `internal/handler/runs.go` — `ProjectRuns` handler (`/api/targets/:id/runs`)
- `internal/handler/vulns.go` — POST /api/vulnerabilities 创建漏洞
- `internal/handler/sse.go` — SSE 流(支持 `?token=`)
- `internal/store/store.go` — Run struct 4 个 token 字段 + `AddTokens()`
- `agents/core/agent.py` — 主 agent loop
- `agents/core/server.py` — FastAPI server,`/runs/{id}/events` SSE emit
- `agents/llm/anthropic_client.py` — 解析 usage → emit `token_usage` 事件
- `agents/tools/registry.py` — `write_finding(args, current_run_id)`
- `frontend/src/views/TargetsView.vue` — 创建 target + recent targets
- `frontend/src/views/RunsView.vue` — run 列表 + token 列
- `frontend/src/views/RunDetailView.vue` — SSE + report + vulns
- `frontend/src/views/VulnsView.vue` — 全局漏洞表

---

## 🗄️ 当前 DB 状态 (`data/dhunter.db`)

| 表 | 行数 |
|---|---|
| targets | 26 |
| runs | 17 |
| messages | 2501 |
| tool_calls | 602 |
| vulnerabilities | 32 (8 critical / 10 high / 3 medium / 5 low / 6 info) |

---

## 🧠 用户偏好 (Durable)

- AI 跑图用**豆包**而非 SD/MJ(中文 prompt + 无水印 + 图生图)
- Mac 平台优先(Go 编译 mac native)
- 商业 license
- 真实漏洞优于模板扫(LLM 主动渗透)

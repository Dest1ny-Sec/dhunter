# ADR-0002: 存储用 SQLite 而非 PostgreSQL

- 状态：已接受
- 日期：2026-08-18

## 背景
数据量：目标/运行/漏洞/消息/tool_calls（千~百万行级），单机部署为主。

## 决策
SQLite（modernc.org/sqlite 纯 Go 驱动，无 CGO），WAL 模式 + foreign_keys + 单写者连接池。
schema 由 migrations.go 幂等管理（CREATE IF NOT EXISTS + 列探测 ALTER）。

## 备选方案
- PostgreSQL：运维成本高，单机部署无必要；但多租户/高并发写场景需迁移。
- 内存/JSON 文件：无事务/索引，无法支撑 FTS 搜索与黑板并发。

## 后果
- 优点：零依赖、单文件备份、跨平台。
- 缺点：并发写受限（已用单写者+WAL 缓解）；未来多租户/集群需迁移 → 迁移路径：
  store 层 SQL 已集中，替换 driver + 重写 migrations 即可。

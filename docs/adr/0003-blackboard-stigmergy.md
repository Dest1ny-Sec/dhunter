# ADR-0003: 黑板（facts/intents/hints）作多 worker 协作媒介

- 状态：已接受
- 日期：2026-08-18

## 背景
多 worker 并行探索同一目标时，如何共享发现、避免重复、实现可恢复的"会话记忆"。

## 决策
worker 之间**不直接通信**（stigmergy）：全部经持久化黑板（SQLite 表 facts/intents/hints）。
- planner 读黑板 → 提出 intents；worker claim/conclude intent（CAS 事务防双认领）；
- intent 结论落为 fact，成为后续规划的输入；hints 承载人工干预。
- 黑板即会话记忆：暂停/继续（`/pause`→`/continue`）与崩溃恢复都基于它。

## 备选方案
- 对话历史内存传递：无法跨 worker 共享、不可恢复、上下文无限膨胀。
- 直接消息队列（worker↔worker）：耦合高、难以审计。

## 后果
- 优点：可恢复、可审计（图可回放）、worker 可热替换；增量 reason 只需发"新 facts"。
- 缺点：LLM 需读黑板摘要（已用 summary 压缩 + confidence 标注控制成本）。

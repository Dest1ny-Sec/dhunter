# Changelog

Dhunter 的版本变更记录。**详细 release notes 在 [GitHub Releases](https://github.com/Dest1ny-Sec/dhunter/releases) 页面**，本文件只列版本索引。

## 索引

| 版本 | 日期 | 类型 | 一句话 |
|---|---|---|---|
| [v0.7.0](https://github.com/Dest1ny-Sec/dhunter/releases/tag/v0.7.0) | 2026-08-18 | feat | MCP 扩展中心：用户可挂载外部 MCP server，工具以 `<server>::<tool>` 命名空间聚合 |
| [v0.6.0](https://github.com/Dest1ny-Sec/dhunter/releases/tag/v0.6.0) | 2026-08-18 | feat | PoC 硬证据入报告 + ReAct 反思进 worker tool loop |
| [v0.5.0](https://github.com/Dest1ny-Sec/dhunter/releases/tag/v0.5.0) | 2026-08-18 | feat | 安全加固 + 可观测性 + watchdog + ADR |
| [v0.4.0](https://github.com/Dest1ny-Sec/dhunter/releases/tag/v0.4.0) | 2026-08-18 | feat | 资产模型 + 证据质量（adapted from dsh-pentest） |
| [v0.3.5](https://github.com/Dest1ny-Sec/dhunter/releases/tag/v0.3.5) | 2026-08-18 | fix | Kali 安装修复 + Stargaze 视觉精修 |
| [v0.3.2](https://github.com/Dest1ny-Sec/dhunter/releases/tag/v0.3.2) | 2026-08-16 | feat | 上下文窗口压缩 + 增量规划 + 版本对比 + 收藏/通知/向导 |
| [v0.3.1](https://github.com/Dest1ny-Sec/dhunter/releases/tag/v0.3.1) | 2026-08-16 | perf | Token 优化 + 跨运行去重 + 预算可视化 |
| [v0.3.0](https://github.com/Dest1ny-Sec/dhunter/releases/tag/v0.3.0) | 2026-08-16 | feat | 安全加固 + 契约修复 + 前端完善（首个公开版） |

## 版本约定

- **v0.x.y**：当前迭代阶段，breaking change 不保证不发生
- **v1.0.0**：API + 部署方式稳定后冻结
- 每次大版本（v_minor=0 → v_minor+1=0）会发 GitHub Release + 标注 breaking changes

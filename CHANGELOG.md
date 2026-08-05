# Changelog

## Unreleased — v3

### Added

- Schema v3 与 identity version 2。
- 稳定 `session_key`、`event_id`、`content_sha256`、identity strategy/scope、parser version 和 event granularity。
- `import_runs.events_rejected` 与 `completed_with_warnings` 冲突摘要。
- 通用事务内 reconcile，供普通 import 和 `.aldb` merge 共用。
- `report sources`、`report providers`、source-product filter、Session 专用聚合及 API 分页。
- `/api/v2/*` 只读 API；summary 增加 distinct Session 数。
- 基于 IANA timezone 的逐事件 SQLite bucket function，正确处理历史 DST。
- 配置级 pricing profile、即时 estimated cost、coverage、`policy_zero` 和稳定 unavailable error code。

### Changed

- 多设备聚合只依赖 v3 `.aldb` 的稳定事件 identity，不保存 device。
- exact duplicate 零写入；兼容补充只做 missing-fill 或 `unknown/fallback → direct`；冲突拒绝。
- merge 只接受 schema v3 / identity v2，先全量 preflight，任一冲突整事务回滚。
- 默认 redacted export 只清空路径和 import warning，不改变 identity/totals。
- Web 以 Sessions 为主要分析页，并分开展示 channel、source product 和 provider。
- pricing rule 现在正确匹配 provider/channel，并拒绝非法日期、负费率和不支持的费率。

### Removed

- Schema v2 compatibility/migration 和 v2 `.aldb` merge。
- `dedupe_key`、`source_agent`、`request_count`、所有 request timing/TTFT/TPS 字段、`recorded_cost_usd`、`raw_usage_json`。
- `report slow`、`compact-raw`、recorded/both cost modes。
- `/api/v1/*` 与 `/analytics/slow`。
- `cleanup.*`、`import.single_thread`、`reports.currency`、`privacy.mode`/envelope alias。
- 设备、source checkpoint、observation/conflict/merge ledger 和持久化 Session 表。

## v2

v2 建立了三表本地 usage analytics 基线以及 Claude、Codex、Copilot、Gemini、WorkBuddy adapter。v3 不迁移 v2 行；升级必须保留 exact backup，并从原始日志 clean rebuild。

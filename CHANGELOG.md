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
- 内置 pricing profile 增加 Grok 4.6 短上下文/长上下文估算规则。
- 内置 pricing profile 增加 `origin-deepseek-v4-flash`、`origin-deepseek-v4-1-flash`、`gemini-3.8-flash`：按用户网关价格表 CNY ÷ 6.8 换算，v4-flash 为用户确认的 origin 档推导价（origin = 2× zj 档）。
- 增加纯本地只读 Cursor Agent Exec adapter：导入显式 request usage、拆分 raw input/cache，并提供 `doctor cursor` 对账。
- 增加纯本地只读 ZCode adapter：导入 `~/.zcode/cli/db/db.sqlite` 的 `model_usage` request usage，schema fail-closed，拆分 cache/reasoning 并验证 total 守恒；不读取正文、`model-io` 或 raw usage。
- `/api/v2/analytics/timeseries` 支持 `cost=estimated|none`；Web Overview 的每日 Tokens 图默认 `cost=none`。
- Web Overview 增加模型成本占比、计价覆盖，以及图表加载/失败状态。
- Schema v4：`usage_events` 增加可空列 `cache_creation_1h_tokens`，记录 Claude 缓存写入中 1 小时 TTL 的部分（来自 `usage.cache_creation.ephemeral_1h_input_tokens`）。`import`/`init` 在事务内把 v3 库自动迁移到 v4，旧行为 `NULL` 并在下次 import 时补齐；只读命令与 merge 来源库仍可直接读取 legacy v3；merge 目标库与 `vacuum` 要求 v4。升级后旧版本无法打开该数据库。
- 缓存写入按 TTL 分档估算：有拆分时 1 小时部分按 `cache_write_1h`、其余按 `cache_write_5m` 计价；没有拆分时沿用 `cache_write_assumption`。
- 内置 pricing profile 增加 Claude Opus 5.5、Sonnet 5.5、Fable 5.1 / Mythos 5.1、Opus 4.1 / 4 legacy 以及 Opus 5.5 / 5 / 4.8 fast mode 规则（`-fast` 模型后缀）。

### Changed

- 多设备聚合只依赖 v3 `.aldb` 的稳定事件 identity，不保存 device。
- exact duplicate 零写入；兼容补充只做 missing-fill 或 `unknown/fallback → direct`；冲突拒绝。
- merge 目标库须为 schema v4，来源库接受 schema v4 或 legacy v3（identity v2），先全量 preflight，任一冲突整事务回滚。
- 默认 redacted export 只清空路径和 import warning，不改变 identity/totals。
- Web 以 Sessions 为主要分析页，并分开展示 channel、source product 和 provider。
- pricing rule 现在正确匹配 provider/channel，并拒绝非法日期、负费率和不支持的费率。
- 逐事件 timezone bucket 缓存 IANA location，避免重复 `LoadLocation`。

### Fixed

- Claude 缓存写入此前全部按 5 分钟价估算，订阅主对话等 1 小时 TTL 写入（2× input）被低估 0.75× input；现在逐事件按真实 TTL 计价。
- Claude Sonnet 5 价格改为官方标准价 $2 / $10（原定 2026-09-01 涨价到 $3 / $15 已取消），并补齐 5m/1h 缓存写入价；此前缓存写入按 input 价计。
- Claude Opus 5 补齐 1 小时缓存写入价；Claude Fable 5.1 缓存读取改为官方 $0.25（此前被 `claude-fable-5-*` 按 Fable 5 的 $1 匹配）；`claude-opus-4-1`、`claude-opus-4-2025*` 不再被 Opus 4.5+ 规则按 $5 计价；`claude-opus-4-8-fast` 不再按标准价计价。

- Codex 累计用量在源文件重写后因行号变化产生新 `event_id` 时，import 与 merge 共用受限语义去重：仅双方均为无 native request/message ID 的 `session_record`，且 Session/turn identity、完整 content facts（含来源累计总量、TTL、accounting 与模型证据）一致时跳过。不同原生 ID、其他来源、非累计或缺证据的样本保持独立；同 ID 的 TTL 补齐及冲突拒绝不变。merge 预检查包含本批次新增/更新，仍保证冲突整批回滚。
- model ID 尾部括号（如 `gpt-5.6-sol(max)`）和明确的 1M context 标记（如 `gpt-5.6-sol[1m]`）会统一剥掉后再识别和计价，思考档位与 context 标记不再影响价格规则；`import` 会按同一规则改写已入库的 `model_normalized` 并重算 content hash。
- Codex append-only 日志后补 `task_complete` 不再改写既有 event identity；Claude optional/null 字段不再让合法 usage 静默丢失。
- reconcile 从最终 canonical row 重算 `content_sha256`，相同 content 可补回 locator metadata；merge 会拒绝 malformed identity hash 或 content hash 不自洽的 v3 行。
- 全部缺价的聚合金额保持 `null`，partial event 的 token coverage 只统计实际进入价格 bucket 的分项；Codex/Copilot/WorkBuddy 的 reasoning token 按各自 accounting contract 计价。
- 只读 API 不再返回 native Session ID 或 HOME 外完整目录；Web 区分日历日期与按 report timezone 格式化的 timestamp；writer、read-only 和 export 统一使用会转义保留字符的 SQLite file URI。

### Removed

- Schema v2 compatibility/migration 和 v2 `.aldb` merge。
- `dedupe_key`、`source_agent`、`request_count`、所有 request timing/TTFT/TPS 字段、`recorded_cost_usd`、`raw_usage_json`。
- `report slow`、`compact-raw`、recorded/both cost modes。
- `/api/v1/*` 与 `/analytics/slow`。
- `cleanup.*`、`import.single_thread`、`reports.currency`、`privacy.mode`/envelope alias。
- 设备、source checkpoint、observation/conflict/merge ledger 和持久化 Session 表。
- TRAE Work CN experimental direct runtime adapter，以及对应的 config、import、doctor 与 API snapshot 入口。
- `agents.*.experimental` 配置字段。

## v2

v2 建立了三表本地 usage analytics 基线以及 Claude、Codex、Copilot、Gemini、WorkBuddy adapter。v3 不迁移 v2 行；升级必须保留 exact backup，并从原始日志 clean rebuild。

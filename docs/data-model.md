# Data Model

AgentLedger v2 使用 SQLite 保存事件级 usage 数据。当前 schema 在 `internal/db/schema.go`，初始化由 `db.Open()` 自动执行。

v2 的设计目标是简单的本地统计分析，而不是多表账本或审计系统。数据库只保留三张表：

| Table | 用途 |
|---|---|
| `meta` | 保存 schema version 和创建时间。 |
| `import_runs` | 记录每次导入运行的文件数、insert/update/skip 结果和状态。 |
| `usage_events` | 扁平事实表，保存 token、timing、channel、provider、model、session 和来源定位信息。 |

v1 的 `devices`、`sources`、`source_files`、`raw_records`、`merge_runs`、`event_observations`、`event_conflicts` 已从 schema 中删除。

## 兼容边界

当前 schema version 是 `2`。

`db.Open()` 会读取现有数据库的 `meta.schema_version`：

- 空库或新库：初始化为 v2。
- v2 库：正常打开。
- 非 v2 库：返回 incompatible schema 错误，并提示运行 `agent-ledger init --reset`。

v2 不迁移旧本地数据。需要保留旧 `.db` / `.aldb` 时，请先手动备份，再 reset。

## 数据库位置

默认数据库路径由配置文件中的 `[database].path` 决定。配置文件和默认数据库所在的数据目录选择顺序：

1. 如果设置了 `AGENT_LEDGER_DATA_DIR`，使用该目录。
2. 如果当前工作目录或可执行文件所在目录的上级能找到 `go.mod`，使用 `<repo-root>/local/data`。
3. 否则使用 `~/.local/share/agent-ledger`。

源码仓库内运行时，默认数据库通常是：

```text
<repo-root>/local/data/agent-ledger.db
```

## 连接行为

SQLite DSN 当前包含：

```text
_journal_mode=WAL
_synchronous=NORMAL
_busy_timeout=5000
_foreign_keys=ON
```

`db.Open(path)` 会创建数据库目录，打开连接，设置 `SetMaxOpenConns(1)`，然后执行 schema 初始化。

## Table: `meta`

| Column | Type | Constraint |
|---|---|---|
| `key` | `TEXT` | `PRIMARY KEY` |
| `value` | `TEXT` | `NOT NULL` |

初始化时会写入：

| Key | Value |
|---|---|
| `schema_version` | `2` |
| `created_at` | `datetime('now')` |

## Table: `import_runs`

| Column | Type | Constraint / Default | 语义 |
|---|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` | import run id。 |
| `started_at_ms` | `INTEGER` | `NOT NULL` | 开始时间。 |
| `finished_at_ms` | `INTEGER` | nullable | 结束时间。 |
| `status` | `TEXT` | `NOT NULL DEFAULT 'running'` | `running` / `completed`。 |
| `files_scanned` | `INTEGER` | `DEFAULT 0` | 实际处理的源文件数量。 |
| `events_added` | `INTEGER` | `DEFAULT 0` | 新插入事件数。 |
| `events_updated` | `INTEGER` | `DEFAULT 0` | 因更完整而更新的重复事件数。 |
| `events_skipped` | `INTEGER` | `DEFAULT 0` | 重复且不更完整的跳过事件数。 |
| `error` | `TEXT` | nullable | 失败原因，当前主成功路径为空。 |

## Table: `usage_events`

`usage_events` 是 v2 唯一事实表。

| Column | Type | Constraint / Default | 语义 |
|---|---|---|---|
| `event_id` | `TEXT` | `PRIMARY KEY` | 稳定事件 ID，由 fingerprint 计算。 |
| `dedupe_key` | `TEXT` | `NOT NULL` | 去重 key。 |
| `dedupe_strategy` | `TEXT` | `NOT NULL` | `message_id`、`session_token`、`raw_hash` 或 `fallback`。 |
| `channel` | `TEXT` | `NOT NULL` | Agent 来源，例如 `claude`、`codex`、`copilot`、`gemini`。 |
| `provider` | `TEXT` | nullable | 模型或日志 provider，例如 `anthropic`、`openai`、`google`。Codex 当前归一为 `openai`，不按 session `model_provider` 拆分。 |
| `model_raw` | `TEXT` | nullable | 日志中的原始模型名。 |
| `model_normalized` | `TEXT` | nullable | 归一化后的模型名。 |
| `source_agent` | `TEXT` | nullable | 解析来源 agent，通常与 `channel` 一致。 |
| `source_product` | `TEXT` | nullable | 更具体的来源形态，例如 `claude-code`、`codex-cli`、`copilot-otel`、`copilot-session-state`。 |
| `observability_level` | `TEXT` | nullable | 来源完整度，例如 `full`、`session_summary`、`inferred`。 |
| `model_is_fallback` | `INTEGER` | `NOT NULL DEFAULT 0` | 模型名是否来自 fallback。 |
| `source_total_tokens` | `INTEGER` | nullable | 源日志中的 raw cumulative / envelope total，用于排查，不直接求和。 |
| `raw_input_tokens` | `INTEGER` | nullable | source 原始 input token；Codex 和 Copilot 中可能包含 cached/cache read input。 |
| `token_accounting_method` | `TEXT` | nullable | token envelope 解析方法，例如 `codex_last_token_usage`、`copilot_session_model_metrics`。 |
| `accounting_profile` | `TEXT` | nullable | 统计口径，例如 Codex 的 `ledger` / `ccusage_compatible`，或 Copilot 的 `input_includes_cache_read`。 |
| `timestamp_ms` | `INTEGER` | `NOT NULL` | 事件时间戳，毫秒。 |
| `session_id` | `TEXT` | nullable | 会话 ID。 |
| `session_path_id` | `TEXT` | nullable | 相对源路径的 session ID；Codex 用于对齐 ccusage 的 session 粒度，Copilot session-state 使用目录 ID。 |
| `turn_id` | `TEXT` | nullable | 明确存在时的 turn ID；Codex 目前主要来自 `task_complete`。 |
| `project_path` | `TEXT` | nullable | adapter 能解析到的项目路径；报表/API 会从它派生项目标签用于按项目筛选和聚合，不代表客户端产品。 |
| `message_id` | `TEXT` | nullable | 日志中的 message id。 |
| `request_id` | `TEXT` | nullable | 日志中的 request id。 |
| `source_file` | `TEXT` | nullable | 来源文件路径。 |
| `line_number` | `INTEGER` | nullable | JSONL 行号。 |
| `raw_sha256` | `TEXT` | nullable | 原始 usage envelope hash。 |
| `input_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | 非缓存输入 token；source 把 cached input 包含在 input 内时，adapter 入库前会拆分。 |
| `output_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | 输出 token。 |
| `reasoning_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | reasoning token。 |
| `cache_creation_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | cache creation token。 |
| `cache_read_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | cache read token。 |
| `total_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | 总 token。source 未提供时由分项计算。 |
| `request_started_at_ms` | `INTEGER` | nullable | 请求开始时间。 |
| `first_token_at_ms` | `INTEGER` | nullable | 首 token 时间。 |
| `completed_at_ms` | `INTEGER` | nullable | 完成时间。 |
| `total_duration_ms` | `INTEGER` | nullable | 总耗时。 |
| `ttft_ms` | `INTEGER` | nullable | time to first token。 |
| `output_duration_ms` | `INTEGER` | nullable | 从首 token 到完成的输出耗时。 |
| `output_tps` | `REAL` | nullable | 输出 TPS。 |
| `recorded_cost_usd` | `REAL` | nullable | 来源明确给出的 USD cost；v2 不计算价格，Copilot `requests.cost` 不写入此列。 |
| `raw_usage_json` | `TEXT` | nullable | 解析到的原始 usage envelope。 |
| `imported_at_ms` | `INTEGER` | `NOT NULL` | 首次导入时间。 |
| `updated_at_ms` | `INTEGER` | `NOT NULL` | 最近更新时间。 |

## 派生规则

v2 不从文本长度、相邻 timestamp 或文件顺序推断 token / 耗时。只有日志明确提供相关字段时才写入 timing。

固定派生规则：

```text
total_tokens = adapter-specific fallback from explicit token parts
ttft_ms = first_token_at_ms - request_started_at_ms
output_duration_ms = completed_at_ms - first_token_at_ms
total_duration_ms = completed_at_ms - request_started_at_ms
output_tps = output_tokens / (output_duration_ms / 1000.0)
```

边界：

- `total_tokens` 仅当 source 没给 `total_tokens` 时由分项计算；Codex 的 reasoning token 是 output token 的子项，fallback 不会把 reasoning 再额外加一次。
- timing 计算要求参与字段存在。
- `output_tps` 要求 `output_duration_ms > 0`。
- 缺失 timing 时对应列保持 `NULL`。

## Upsert 完整度规则

`import` 不再使用 `INSERT OR IGNORE`。重复事件会按完整度决定是否覆盖旧记录；对于 Codex，如果 parser 或 fingerprint 规则修正导致 `event_id` 改变，但 `source_file + line_number + raw_sha256` 仍指向同一原始 JSONL 行，upsert 会把当前 `event_id` 精确匹配行和同来源历史 sibling 放进同一个候选集合。即使精确匹配行来自默认脱敏导出（`source_file = NULL`）或另一条绝对路径，也不会妨碍当前本地来源行参与收敛。

收敛时，incoming candidate 和候选集合共同选择最完整的 token、timing、cost 用量 winner；删除 sibling 前，还会从 token 六分项完全相同的候选中补齐 winner 缺失的 `source_total_tokens`、`raw_input_tokens`、`token_accounting_method` 和 `accounting_profile`，已有值不会被冲突值覆盖。`session_path_id`、`turn_id`、`project_path` 等 source metadata 则遍历全部候选，按原有 missing、稳定性和路径具体度规则保留互补字段，不与 usage winner 绑定。最终记录使用当前解析得到的 identity、provider、model 和本地 source envelope，保留获胜的用量 bundle，并根据最终实际落库字段重新计算 `event_id`、`dedupe_key` 和 `dedupe_strategy`。因此在 `session_token` 策略下，如果保留了历史上更完整的 token 用量，最终 ID 可能不同于 incoming candidate 最初计算的 ID；重复导入同一 canonical 内容会返回 `skipped`。其他 adapter 的单行日志可能拆出多个合法事件，因此不使用这一 Codex 专用兼容身份。

优先级：

1. 有 timing。
2. 有 `recorded_cost_usd`。
3. 有 model。
4. `total_tokens` 更高。

结果会记录为 inserted、updated 或 skipped。

## 索引

固定索引：

| Index | Columns |
|---|---|
| `idx_usage_events_timestamp` | `timestamp_ms` |
| `idx_usage_events_channel` | `channel` |
| `idx_usage_events_provider` | `provider` |
| `idx_usage_events_model` | `model_normalized` |
| `idx_usage_events_session` | `session_id` |
| `idx_usage_session_path` | `session_path_id` |
| `idx_usage_events_output_tps` | `output_tps` |
| `idx_usage_events_total_duration` | `total_duration_ms` |
| `idx_usage_events_channel_time` | `channel, timestamp_ms` |
| `idx_usage_events_model_time` | `model_normalized, timestamp_ms` |
| `idx_usage_source_identity` | `source_file, line_number, raw_sha256, channel, imported_at_ms, event_id` |

## 命令读写矩阵

| Command | 写入表 | 读取表 | 说明 |
|---|---|---|---|
| `init` | `meta` | config | 初始化 v2 schema；`--reset` 删除本地 DB/WAL/SHM 后重建。 |
| `import` | `import_runs`、`usage_events` | configured source paths | 遍历启用 adapter，解析 usage record，按 fingerprint upsert；Codex 同一来源行可用于兼容更新旧 fingerprint。 |
| `export` | 无 | 当前 SQLite 数据库文件 | 直接复制数据库文件。 |
| `merge` | `usage_events` | incoming `.aldb` 的 `usage_events` | 只接受 schema v2，插入未见事件。 |
| `status` | 无 | `meta`、`usage_events`、`import_runs` | 输出 v2 统计。 |
| `report *` | 无 | `usage_events` | 使用 SQLite 聚合输出 text 或 JSON。 |
| `serve` | 无 | `meta`、`import_runs`、`usage_events` | 只读 API 和 Web 面板。 |
| `doctor` | 无 | config 与 source paths | 扫描 configured paths 统计源文件数量；`doctor codex` 会额外输出 Codex token/timing/口径覆盖诊断。 |
| `verify` | 无 | 当前 SQLite 数据库 | 执行 `PRAGMA integrity_check`。 |
| `vacuum` | SQLite 内部重写 | 当前 SQLite 数据库 | 执行 `VACUUM`。 |

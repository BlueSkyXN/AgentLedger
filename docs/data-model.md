# Data Model

AgentLedger 使用 SQLite 保存事件级 usage 数据。当前 schema 在 `internal/db/schema.go`，初始化由 `db.Open()` 自动执行。

本文档描述当前 Go CLI 的真实数据库结构和写入边界。部分表已经在 schema 中存在，但仍属于后续能力预留；不要把“表已存在”理解为“当前命令已经完整写入”。

## 设计必要性结论

当前 CLI 的核心目标是：导入本机 agent usage 日志、按事件去重、生成报表、跨设备合并 SQLite 导出文件。围绕这个目标，推荐把 schema 收敛成“当前最小闭环 + 明确条件触发的未来扩展”，不要把长期设想提前做进产品承诺。

### 当前最小闭环

当前最小可用数据库只需要四类数据：

| 数据 | 表 | 为什么必要 |
|---|---|---|
| schema 标识 | `meta` | 记录当前 schema version，给后续兼容和升级留最低限度入口。 |
| 本机设备身份 | `devices` | import/merge/report 需要区分事件来自哪台设备。 |
| 导入运行记录 | `import_runs` | 记录本次扫描了多少文件、新增多少事件、跳过多少重复事件，便于解释导入结果。 |
| usage 事件 | `usage_events` | 核心事实表，支撑去重、报表、export/merge。 |

这四类表已经能覆盖当前已实现命令的主路径：`init`、`import`、`status`、`report`、`export`、`merge`、`verify`、`vacuum`。

### 暂不扩展的表

下面这些表不是当前 MVP 必需表。它们只有在对应功能真正进入实现时才应该升级为正式设计：

| 表 | 触发条件 | 当前建议 |
|---|---|---|
| `merge_runs` | 需要审计每次 merge 来源、结果和失败原因。 | 可保留为轻量预留，但短期不要围绕它扩展。 |
| `sources` | 需要把 adapter/base path 持久化为一等对象。 | 暂不使用；当前配置文件已经足够表达 source path。 |
| `source_files` | 需要增量导入、文件级导入状态或 cleanup eligibility。 | cleanup 未实现前不要启用。 |
| `raw_records` | 需要逐行 raw JSON 审计、parse error 追踪或重放解析。 | 当前直接保存 usage envelope 已足够。 |
| `event_observations` | 需要记录同一事件被多台设备观察到。 | 当前 merge 只关心最终去重结果，不需要。 |
| `event_conflicts` | 需要处理同一 fingerprint 下字段不一致。 | 当前 `INSERT OR IGNORE` 策略不处理冲突，不需要。 |

### 收敛原则

- 当前阶段不新增表，除非某个命令已经明确需要它读写。
- 当前阶段不把预留表写进用户使用路径、产品卖点或示例流程。
- 当前阶段优先保持 `usage_events` 作为单一事实表，报表和 merge 都围绕它实现。
- cleanup/quarantine 没进入实现前，不启用 `sources`、`source_files`、`raw_records`。
- observation/conflict 没进入实现前，不启用 `event_observations`、`event_conflicts`。
- 如果后续做 schema slimming，优先考虑删除长期不用的预留表，或者把它们延后到真正需要的 migration 中再创建。

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

`Database.Open(path)` 会创建数据库目录，打开连接，设置 `SetMaxOpenConns(1)`，然后执行 schema 初始化。schema 初始化使用 `CREATE TABLE IF NOT EXISTS` 和 `CREATE INDEX IF NOT EXISTS`，当前没有独立 migration 文件。

## 表概览

| Table | 当前用途 | 当前写入状态 | 必要性 |
|---|---|---|---|
| `meta` | 保存 `schema_version=1` 和 `created_at`。 | schema 初始化写入 | 当前必需 |
| `devices` | 保存当前设备信息。 | `init`/`import`/`merge` upsert 当前设备 | 当前必需 |
| `import_runs` | 保存导入运行统计。 | `import` 开始和结束时写入 | 当前必需 |
| `merge_runs` | 保存合并运行统计。 | schema 预留；当前 `merge` 未写入 | 条件性未来表 |
| `sources` | 保存 source adapter 与 base path。 | schema 预留；当前导入路径未写入 | 条件性未来表 |
| `source_files` | 保存被发现的源日志文件状态。 | schema 预留；当前导入路径未写入，所以 `status` 中该计数通常为 0 | 条件性未来表 |
| `raw_records` | 保存源文件中的原始 JSON 行和解析状态。 | schema 预留；当前导入路径未写入 | 条件性未来表 |
| `usage_events` | 核心事件表。 | 当前主要数据写入点 | 当前必需 |
| `event_observations` | 保存同一事件被哪些设备观察到。 | schema 预留；当前导入/merge 未写入 | 条件性未来表 |
| `event_conflicts` | 保存同一 fingerprint 下字段冲突。 | schema 预留；当前未实现冲突记录 | 条件性未来表 |

## 命令读写矩阵

| Command | 写入表 | 读取表 | 说明 |
|---|---|---|---|
| `init` | `meta`、`devices` | `config.toml`、`device_id` | 打开数据库会初始化 schema；随后 upsert 当前设备。 |
| `import` | `meta`、`devices`、`import_runs`、`usage_events` | configured source paths | 遍历启用 adapter，解析 usage record，按 fingerprint 插入事件。 |
| `export` | 无 | 当前 SQLite 数据库文件 | 直接复制数据库文件，不脱敏、不过滤、不压缩。 |
| `merge` | `meta`、`devices`、`usage_events` | incoming `.aldb` 的 `usage_events` | attach incoming 数据库，按 `event_fingerprint` 做 `INSERT OR IGNORE`。 |
| `status` | 无 | `usage_events`、`devices`、`import_runs`、`source_files` | `source_files` 当前通常为 0，因为导入主路径未写入。 |
| `report *` | 无 | `usage_events`，`report devices` 还读取 `devices` | 使用 SQLite 聚合输出 text 或 JSON。 |
| `doctor` | 无 | config 与 source paths | 扫描 configured paths 统计源文件数量，不写数据库。 |
| `verify` | 无 | 当前 SQLite 数据库 | 执行 `PRAGMA integrity_check`。 |
| `vacuum` | SQLite 内部重写 | 当前 SQLite 数据库 | 执行 `VACUUM`，会重写数据库文件。 |

## Table: `meta`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `key` | `TEXT` | `PRIMARY KEY` | metadata key |
| `value` | `TEXT` | `NOT NULL` | metadata value |

初始化时会写入：

| Key | Value |
|---|---|
| `schema_version` | `1` |
| `created_at` | `datetime('now')` |

当前没有 migration runner；后续 schema 变更需要补版本升级策略。

## Table: `devices`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `device_id` | `TEXT` | `PRIMARY KEY` | 本机持久化设备 ID，来自 `<data-dir>/device_id`。 |
| `device_name` | `TEXT` | nullable | 当前使用 hostname。 |
| `hostname` | `TEXT` | nullable | OS hostname。 |
| `os` | `TEXT` | nullable | Go `runtime.GOOS`。 |
| `arch` | `TEXT` | nullable | Go `runtime.GOARCH`。 |
| `app_version` | `TEXT` | nullable | 当前硬编码为 `0.1.0`。 |
| `created_at_ms` | `INTEGER` | `NOT NULL` | 设备记录创建时间；当前 upsert 时传入当前时间。 |
| `last_seen_at_ms` | `INTEGER` | `NOT NULL` | 当前设备最后出现时间。 |

`init`、`import`、`merge` 都会 upsert 当前设备。冲突时当前只更新 `last_seen_at_ms` 和 `app_version`。

## Table: `import_runs`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` | import run ULID。 |
| `device_id` | `TEXT` | `NOT NULL`, FK `devices(device_id)` | 执行导入的本机设备。 |
| `started_at_ms` | `INTEGER` | `NOT NULL` | 开始时间。 |
| `finished_at_ms` | `INTEGER` | nullable | 结束时间。 |
| `status` | `TEXT` | `NOT NULL DEFAULT 'running'` | 当前成功结束时更新为 `completed`。 |
| `files_scanned` | `INTEGER` | `DEFAULT 0` | 实际处理的源文件数量，不含 grace period 内被跳过的近期文件。 |
| `events_added` | `INTEGER` | `DEFAULT 0` | 本次新插入事件数。 |
| `events_skipped` | `INTEGER` | `DEFAULT 0` | 因主键重复跳过的事件数。 |
| `error` | `TEXT` | nullable | schema 预留；当前失败路径没有系统写入错误。 |

当前 `import` 会在开始时插入 `running` 记录，结束时写入统计并标记 `completed`。

## Table: `merge_runs`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` | schema 预留。 |
| `device_id` | `TEXT` | `NOT NULL`, FK `devices(device_id)` | schema 预留。 |
| `source_path` | `TEXT` | `NOT NULL` | schema 预留，计划记录 incoming `.aldb` 路径。 |
| `started_at_ms` | `INTEGER` | `NOT NULL` | schema 预留。 |
| `finished_at_ms` | `INTEGER` | nullable | schema 预留。 |
| `status` | `TEXT` | `NOT NULL DEFAULT 'running'` | schema 预留。 |
| `events_merged` | `INTEGER` | `DEFAULT 0` | schema 预留。 |
| `events_skipped` | `INTEGER` | `DEFAULT 0` | schema 预留。 |
| `error` | `TEXT` | nullable | schema 预留。 |

当前 `merge` 命令不写 `merge_runs`。它只返回 inserted/skipped 数量到 stdout。

## Table: `sources`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` | schema 预留。 |
| `agent` | `TEXT` | `NOT NULL` | 计划记录 adapter 名称，例如 `claude`、`codex`。 |
| `channel` | `TEXT` | `NOT NULL` | 计划记录来源渠道，例如 `local`。 |
| `base_path` | `TEXT` | `NOT NULL` | 计划记录 configured base path。 |

当前导入主路径不写 `sources`。

## Table: `source_files`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `rowid` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | source file 内部行 ID。 |
| `source_id` | `TEXT` | `NOT NULL`, FK `sources(id)` | schema 预留。 |
| `file_path` | `TEXT` | `NOT NULL UNIQUE` | 计划记录源日志文件路径。 |
| `file_size` | `INTEGER` | nullable | 计划记录文件大小。 |
| `file_mtime_ms` | `INTEGER` | nullable | 计划记录 mtime。 |
| `content_sha256` | `TEXT` | nullable | 计划记录文件内容 hash。 |
| `import_status` | `TEXT` | `NOT NULL DEFAULT 'pending'` | 计划记录导入状态。 |
| `cleanup_status` | `TEXT` | `NOT NULL DEFAULT 'none'` | 计划记录 cleanup/quarantine 状态。 |
| `quarantined_path` | `TEXT` | nullable | 计划记录隔离后的路径。 |
| `last_import_ms` | `INTEGER` | nullable | 计划记录最近导入时间。 |

当前导入主路径不写 `source_files`，所以 `status` 中的 source file 计数通常为 0。

## Table: `raw_records`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `rowid` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | raw record 内部行 ID。 |
| `source_file_id` | `INTEGER` | `NOT NULL`, FK `source_files(rowid)` | schema 预留。 |
| `line_number` | `INTEGER` | nullable | 计划记录 JSONL 行号。 |
| `raw_json` | `TEXT` | `NOT NULL` | 计划记录原始 JSON。 |
| `raw_sha256` | `TEXT` | `NOT NULL` | 计划记录原始 JSON hash。 |
| `parsed_ok` | `INTEGER` | `NOT NULL DEFAULT 0` | 计划记录解析是否成功。 |
| `parse_error` | `TEXT` | nullable | 计划记录解析错误。 |

当前导入主路径不写 `raw_records`。可解析 usage envelope 会直接进入 `usage_events.raw_usage_json`。

## `usage_events`

`usage_events.event_fingerprint` 是主键。主要字段组：

- provenance: `origin_device_id`、`first_seen_device_id`、`last_seen_device_id`
- source: `agent`、`provider`、`client_name`、`source_channel`、`billing_channel`、`source_kind`
- model: `model_raw`、`model_normalized`、`model_provider`、`model_family`
- time: `timestamp_ms`、`timestamp_text`、`source_timezone`、`timestamp_offset_minutes`
- session/project: `session_id`、`conversation_id`、`project`、`workspace_key`
- identifiers: `message_id`、`request_id`
- tokens: input/output/cache/reasoning/tool/extra/source/total token fields
- cost: `cost_usd`、`cost_source`、`pricing_source`、`pricing_version`
- raw: `raw_usage_json`、`raw_meta_json`、`raw_sha256`

当前实现会写入 adapter 能解析出来的字段。成本和 pricing 字段存在，但没有价格表计算逻辑；很多事件的 `cost_usd` 会是默认值。

### `usage_events` columns

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `event_fingerprint` | `TEXT` | `PRIMARY KEY` | 事件稳定主键，由 fingerprint 策略计算。 |
| `dedupe_key` | `TEXT` | nullable | 当前等于 `event_fingerprint`。 |
| `fingerprint_strategy` | `TEXT` | `NOT NULL` | `message_id`、`session_token`、`raw_hash` 或 `fallback`。 |
| `origin_device_id` | `TEXT` | `NOT NULL` | 事件最初被当前数据库记录时的来源设备。 |
| `first_seen_device_id` | `TEXT` | `NOT NULL` | 当前实现写入本机设备 ID；merge 时保留 incoming 值。 |
| `last_seen_device_id` | `TEXT` | `NOT NULL` | import 写入本机设备 ID；merge 插入 incoming 事件时写入执行 merge 的设备 ID。 |
| `agent` | `TEXT` | `NOT NULL` | adapter 名称，例如 `claude`、`codex`、`gemini`、`qwen`。 |
| `provider` | `TEXT` | nullable | adapter 解析得到的 provider，例如 `anthropic`、`openai`、`google`、`alibaba`。 |
| `client_name` | `TEXT` | nullable | schema 预留；当前 adapter 通常不填。 |
| `source_channel` | `TEXT` | nullable | 当前 import 写入 `local`。 |
| `billing_channel` | `TEXT` | nullable | schema 预留。 |
| `source_kind` | `TEXT` | nullable | 当前 import 写入 `log`。 |
| `model_raw` | `TEXT` | nullable | 日志中的原始模型名。 |
| `model_normalized` | `TEXT` | nullable | `adapters.NormalizeModelName` 归一化后的模型名。 |
| `model_provider` | `TEXT` | nullable | 根据模型名推断出的模型 provider。 |
| `model_family` | `TEXT` | nullable | 根据模型名推断出的模型 family。 |
| `is_fallback_model` | `INTEGER` | `DEFAULT 0` | schema 预留。 |
| `speed_label` | `TEXT` | nullable | schema 预留。 |
| `service_tier` | `TEXT` | nullable | schema 预留。 |
| `speed_multiplier` | `REAL` | `DEFAULT 1.0` | schema 预留。 |
| `is_fast_mode` | `INTEGER` | `DEFAULT 0` | schema 预留。 |
| `timestamp_ms` | `INTEGER` | `NOT NULL` | 事件时间戳，毫秒。 |
| `timestamp_text` | `TEXT` | nullable | schema 预留或 adapter 可选字段。 |
| `source_timezone` | `TEXT` | nullable | schema 预留。 |
| `timestamp_offset_minutes` | `INTEGER` | nullable | schema 预留。 |
| `session_id` | `TEXT` | nullable | adapter 解析或从路径推断的 session。 |
| `conversation_id` | `TEXT` | nullable | schema 预留。 |
| `project` | `TEXT` | nullable | schema 预留。 |
| `project_path_raw` | `TEXT` | nullable | schema 预留。 |
| `project_path_normalized` | `TEXT` | nullable | schema 预留。 |
| `workspace_key` | `TEXT` | nullable | schema 预留。 |
| `message_id` | `TEXT` | nullable | adapter 解析出的 message id。 |
| `request_id` | `TEXT` | nullable | adapter 解析出的 request id。 |
| `input_tokens` | `INTEGER` | `DEFAULT 0` | 输入 token。 |
| `output_tokens` | `INTEGER` | `DEFAULT 0` | 输出 token。 |
| `cache_creation_tokens` | `INTEGER` | `DEFAULT 0` | cache creation token。 |
| `cache_read_tokens` | `INTEGER` | `DEFAULT 0` | cache read token。 |
| `reasoning_tokens` | `INTEGER` | `DEFAULT 0` | reasoning token。 |
| `tool_tokens` | `INTEGER` | `DEFAULT 0` | schema 预留。 |
| `extra_total_tokens` | `INTEGER` | `DEFAULT 0` | schema 预留。 |
| `source_total_tokens` | `INTEGER` | `DEFAULT 0` | schema 预留。 |
| `total_tokens` | `INTEGER` | `DEFAULT 0` | adapter 提供的总 token；如果为 0，import 会用输入、输出、cache、reasoning token 求和。 |
| `cost_usd` | `REAL` | `DEFAULT 0.0` | 当前通常为 0；尚未实现价格表计算。 |
| `cost_source` | `TEXT` | nullable | schema 预留。 |
| `pricing_source` | `TEXT` | nullable | schema 预留。 |
| `pricing_version` | `TEXT` | nullable | schema 预留。 |
| `credits` | `REAL` | `DEFAULT 0.0` | schema 预留。 |
| `message_count` | `INTEGER` | `DEFAULT 0` | schema 预留。 |
| `raw_usage_json` | `TEXT` | nullable | adapter 提取到的 usage envelope 原始 JSON。 |
| `raw_meta_json` | `TEXT` | nullable | schema 预留。 |
| `raw_sha256` | `TEXT` | nullable | adapter 解析出的 raw hash。 |
| `created_at_ms` | `INTEGER` | `NOT NULL` | 本数据库插入时间。 |
| `updated_at_ms` | `INTEGER` | `NOT NULL` | 当前 import 写入插入时间；merge 插入 incoming 事件时写入 merge 时间。 |

## Fingerprint 策略

`internal/fingerprint.Compute` 使用四级优先级：

1. `message_id`: `agent + provider + message_id`
2. `session_token`: `agent + provider + session_id + timestamp + input_tokens + output_tokens`
3. `raw_hash`: canonical raw JSON
4. `fallback`: source file + line number + raw sha256

这些策略让同一事件在重复导入或跨设备 merge 时落到同一个主键。

## Table: `event_observations`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `rowid` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | schema 预留。 |
| `event_fingerprint` | `TEXT` | `NOT NULL`, FK `usage_events(event_fingerprint)` | 计划关联事件。 |
| `device_id` | `TEXT` | `NOT NULL`, FK `devices(device_id)` | 计划记录观察到事件的设备。 |
| `import_run_id` | `TEXT` | nullable | 计划关联 import run。 |
| `observed_at_ms` | `INTEGER` | `NOT NULL` | 计划记录观察时间。 |
| `source_file_path` | `TEXT` | nullable | 计划记录来源文件。 |

当前导入和 merge 都不写 `event_observations`。

## Table: `event_conflicts`

| Column | Type | Constraint / Default | 当前语义 |
|---|---|---|---|
| `rowid` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | schema 预留。 |
| `event_fingerprint` | `TEXT` | `NOT NULL`, FK `usage_events(event_fingerprint)` | 计划关联冲突事件。 |
| `field_name` | `TEXT` | `NOT NULL` | 计划记录发生冲突的字段。 |
| `old_value` | `TEXT` | nullable | 计划记录旧值。 |
| `new_value` | `TEXT` | nullable | 计划记录新值。 |
| `resolution` | `TEXT` | nullable | 计划记录冲突处理结果。 |
| `resolved_at_ms` | `INTEGER` | nullable | 计划记录处理时间。 |

当前没有冲突检测或 resolution 逻辑。

## Indexes

| Index | Table | Column | Purpose |
|---|---|---|---|
| `idx_events_agent` | `usage_events` | `agent` | 加速按 agent 过滤或聚合。 |
| `idx_events_timestamp` | `usage_events` | `timestamp_ms` | 加速时间范围过滤和报表。 |
| `idx_events_model` | `usage_events` | `model_normalized` | 加速模型报表。 |
| `idx_events_session` | `usage_events` | `session_id` | 加速 session 报表。 |
| `idx_events_device` | `usage_events` | `origin_device_id` | 加速设备报表。 |

## 数据生命周期

### Import

```text
configured paths
  -> adapter.Discover(paths)
  -> adapter.ParseFile(path)
  -> fingerprint.Compute(record)
  -> db.InsertEvent(usage_events)
  -> import_runs completed
```

当前 import 不会移动、删除、隔离或改写原始 agent 日志。

### Export

```text
current SQLite database
  -> copy file to output .aldb
```

当前 export 不脱敏、不压缩、不按时间范围过滤。`.aldb` 应视为私有 SQLite 数据库副本。

### Merge

```text
incoming .aldb
  -> validate regular file and SQLite header
  -> ATTACH DATABASE incoming
  -> INSERT OR IGNORE incoming.usage_events
```

当前 merge 只合并 `usage_events`，不会写 `merge_runs`、`event_observations` 或 `event_conflicts`。

## 当前限制

- 当前导入没有增量记录 source file 状态；重复导入主要依赖 `usage_events` 主键去重。
- 当前 merge 只插入未见 `usage_events`，不会记录 observation 或 conflict。
- schema 版本固定为 `1`；如果后续改 schema，需要补迁移策略和兼容测试。
- schema 初始化是 idempotent create，不是 migration system；对公开 `.aldb` 做兼容升级前，需要先定义 migration 文件位置、版本推进方式、旧库检测和失败回滚策略。

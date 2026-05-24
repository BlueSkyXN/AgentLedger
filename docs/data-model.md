# Data Model

AgentLedger 使用 SQLite 保存事件级 usage 数据。当前 schema 在 `internal/db/schema.go`，初始化由 `db.Open()` 自动执行。

## 连接行为

SQLite DSN 当前包含：

```text
_journal_mode=WAL
_synchronous=NORMAL
_busy_timeout=5000
_foreign_keys=ON
```

`Database.Open(path)` 会创建数据库目录，打开连接，设置 `SetMaxOpenConns(1)`，然后执行 schema 初始化。

## 表概览

| Table | 当前用途 |
|---|---|
| `meta` | 保存 `schema_version=1` 和 `created_at`。 |
| `devices` | 保存当前设备信息，`init`/`import`/`merge` 会 upsert 当前设备。 |
| `import_runs` | `import` 开始和结束时写入运行统计。 |
| `merge_runs` | schema 预留；当前 `merge` 命令未写入。 |
| `sources` | schema 预留；当前导入路径未写入。 |
| `source_files` | schema 预留；当前导入路径未写入，所以 `status` 中该计数通常为 0。 |
| `raw_records` | schema 预留；当前导入路径未写入。 |
| `usage_events` | 核心事件表，当前主要数据写入点。 |
| `event_observations` | schema 预留；当前导入/merge 路径未写入。 |
| `event_conflicts` | schema 预留；当前未实现冲突记录。 |

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

## Fingerprint 策略

`internal/fingerprint.Compute` 使用四级优先级：

1. `message_id`: `agent + provider + message_id`
2. `session_token`: `agent + provider + session_id + timestamp + input_tokens + output_tokens`
3. `raw_hash`: canonical raw JSON
4. `fallback`: source file + line number + raw sha256

这些策略让同一事件在重复导入或跨设备 merge 时落到同一个主键。

## 当前限制

- 当前导入没有增量记录 source file 状态；重复导入主要依赖 `usage_events` 主键去重。
- 当前 merge 只插入未见 `usage_events`，不会记录 observation 或 conflict。
- schema 版本固定为 `1`；如果后续改 schema，需要补迁移策略和兼容测试。

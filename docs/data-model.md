# Data Model — schema v3

AgentLedger v3 只保留 `meta`、`import_runs`、`usage_events` 三张表。`db.OpenReadOnly()` 只验证普通 SQLite 可读；`db.OpenReadOnlyV3()` 和 `db.OpenReadWriteV3()` 还要求 schema v3、identity v2 与完整必需列。

v3 不迁移 v2 行，不接受 v2 `.aldb` merge。已有数据通过原始日志 clean rebuild。

## `meta`

| key | value |
|---|---|
| `schema_version` | `3` |
| `identity_version` | `2` |
| `created_at` | 数据库创建时间 |

## `import_runs`

```text
id
started_at_ms
finished_at_ms
status
files_scanned
events_added
events_updated
events_skipped
events_rejected
error
```

`status` 为 `running`、`completed` 或 `completed_with_warnings`。`error` 保存脱敏 warning 摘要，不保存真实日志内容、Session ID、token 值或 source path。

## `usage_events`

### Identity

```text
event_id
identity_version
identity_strategy
identity_scope
content_sha256
parser_version
event_granularity
```

`identity_strategy` 仅使用 `native_event`、`native_message`、`native_request`、`session_turn`、`session_record`、`content_fallback`。

### 来源与模型

```text
channel
source_product
provider
model_raw
model_normalized
model_resolution
model_is_fallback
```

`model_normalized` 缺失时保存 `unknown`。channel、source product 和 provider 是独立维度。

### 时间、Session 与 locator

```text
timestamp_ms
session_key
session_id
session_path_id
turn_id
project_path
message_id
request_id
source_file
line_number
raw_sha256
```

`timestamp_ms > 0`，`session_key` 必填。`project_path`、`source_file` 是本机私有 locator，默认 export 清空；它们不参与 event/session/content identity。

### Token 与 accounting

```text
input_tokens
output_tokens
reasoning_tokens
cache_creation_tokens
cache_read_tokens
total_tokens
source_total_tokens
raw_input_tokens
token_accounting_method
accounting_profile
observability_level
```

所有 token 字段非负。reasoning、cache、total 的包含关系由 adapter/accounting profile 验证，不使用一个跨产品通用求和公式；报表只汇总 canonical `total_tokens`，不汇总 `source_total_tokens`。

### 导入时间

```text
imported_at_ms
updated_at_ms
```

exact duplicate 不更新这两个字段。

## 不存在的 v2 字段

```text
dedupe_key
source_agent
request_count
request_started_at_ms
first_token_at_ms
completed_at_ms
total_duration_ms
ttft_ms
output_duration_ms
output_tps
recorded_cost_usd
raw_usage_json
```

## 约束与索引

- `event_id`、`content_sha256`、`channel`、`source_product`、`session_key` 非空。
- identity version 固定为 2。
- timestamp 必须大于 0，token 必须非负。
- 索引：`timestamp_ms`、`session_key`、`channel + timestamp_ms`、`source_product + timestamp_ms`、`model_normalized + timestamp_ms`。

## Reconcile 决策

| 条件 | 结果 |
|---|---|
| event ID 不存在 | insert |
| content hash 相同 | skip，零写入 |
| 同 timestamp/session/token，仅补 missing 元数据 | update |
| fallback/unknown 模型升级为直接证据，token 相同 | update |
| 两条直接模型证据冲突 | reject |
| timestamp、session、token bucket/total 冲突 | reject |
| 已知 accounting method/profile 冲突 | reject |

拒绝不新增行、不覆盖原行。merge 的任一拒绝会回滚整次 merge。

## Session summary

数据库没有 `sessions` 表。Session summary 从当前筛选窗口内的事件实时聚合：日期范围、event count、主模型、模型数、token buckets 和即时 estimated cost。

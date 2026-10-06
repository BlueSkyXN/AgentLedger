# Data Model — schema v4

AgentLedger 只保留 `meta`、`import_runs`、`usage_events` 三张表。schema v4 在 v3 基础上只给 `usage_events` 增加一列可空的 `cache_creation_1h_tokens`，表集合与 identity v2 不变。

- `db.Open()`（`import`、`init` 使用）遇到 schema v3 数据库时，在单个事务内执行 `ALTER TABLE ... ADD COLUMN` 并把 `schema_version` 改为 `4`；迁移前先校验完整的 v3 结构，不完整或含未知对象的库不会被升级。迁移后旧行的 `cache_creation_1h_tokens` 为 `NULL`，下一次 `import` 重新解析来源日志时补齐。
- `db.OpenReadOnly()` 只验证普通 SQLite 可读；`db.OpenReadOnlyV3()`（`serve`、`report`、`status`、`export`、merge 来源库）接受 schema v4 与 legacy v3，legacy v3 的 TTL 拆分按未知读取，不写库。
- `db.OpenReadWriteV3()`（merge 目标库、`vacuum`）只接受 schema v4；遇到 v3 时提示先运行一次 `import` 或 `init` 完成迁移。
- 升级到 v4 后，旧版本 AgentLedger 因列校验无法再打开该数据库。

v2 行不迁移，不接受 v2 `.aldb` merge。已有 v2 数据通过原始日志 clean rebuild。

## `meta`

| key | value |
|---|---|
| `schema_version` | `4`（legacy `3` 仅只读兼容，`db.Open()` 会自动迁移） |
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

`model_normalized` 缺失时保存 `unknown`。尾部括号和明确的 1M context 标记 `[1m]` 会从 model ID 上剥掉，例如 `gpt-5.6-sol(max)`、`gpt-5.6-sol[1m]` 都保存为 `gpt-5.6-sol`；`model_raw` 仍保留来源原值。其他方括号后缀不会被泛化删除。思考档位和 context 标记不参与价格规则匹配。`import` 会按同一规则改写已入库的同形记录并重算 `content_sha256`。channel、source product 和 provider 是独立维度。

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
cache_creation_1h_tokens
token_accounting_method
accounting_profile
observability_level
```

所有 token 字段非负。reasoning、cache、total 的包含关系由 adapter/accounting profile 验证，不使用一个跨产品通用求和公式；报表只汇总 canonical `total_tokens`，不汇总 `source_total_tokens`。

`cache_creation_1h_tokens` 是 `cache_creation_tokens` 中 1 小时 TTL 缓存写入的部分，5 分钟部分为两者之差，不单独存储。`NULL` 表示来源没有提供 TTL 拆分（非 Claude 来源、Claude 旧记录或迁移后尚未重新 import 的行），`0` 表示已知全部为 5 分钟写入。约束为 `0 <= cache_creation_1h_tokens <= cache_creation_tokens`。该字段只在非 `NULL` 时进入 content hash，因此没有拆分的事件保持原有 `content_sha256`。reconcile 时 `NULL` 可被已知值补齐（`updated`），两个不同的已知值是 `token_conflict`；缺少拆分、其余事实相同的观测（旧 parser 或 legacy v3 来源库）按 `skipped` 处理，不会抹掉已知值。

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
- `cache_creation_1h_tokens` 为 `NULL` 或介于 0 与 `cache_creation_tokens` 之间。
- 索引：`timestamp_ms`、`session_key`、`session_key + timestamp_ms`、`channel + timestamp_ms`、`source_product + timestamp_ms`、`model_normalized + timestamp_ms`。

## Reconcile 决策

| 条件 | 结果 |
|---|---|
| event ID 不存在，且不满足下述受限语义重复条件 | insert |
| event ID 不存在，双方均为 Codex 累计用量的 `session_record`，且 Session/turn identity 与完整 content facts 相同 | skip，零写入；import 与 merge 使用同一判定 |
| content hash 相同且无缺失 metadata | skip，零写入 |
| content hash 相同且仅补本机 locator metadata | update |
| 同 timestamp/session/token，仅补 missing 元数据 | update |
| fallback/unknown 模型升级为直接证据，token 相同 | update |
| 两条直接模型证据冲突 | reject |
| timestamp、session、token bucket/total 冲突 | reject |
| 已知 accounting method/profile 冲突 | reject |
| merge 输入的 event/session hash 格式或 content hash 不自洽 | reject |

跨 event ID 的语义去重只接受 `channel=codex`、`source_product=codex-cli`、`identity_strategy=session_record`、`identity_scope=session`、request 粒度且非零的记录；双方均不能带 native request/message ID，accounting method 必须为 `codex_total_delta`，并提供正数 `source_total_tokens` 与已知 `raw_input_tokens`。匹配比较 Session key、Session ID、Session path ID、turn ID，以及从完整持久化事实重算的 content hash（含 timestamp、模型证据、provider、observability、全部 token 分项、来源累计总量、TTL 和 accounting）。源文件位置、raw hash 与导入时间不参与比较。

不同原生事件/消息/请求 ID 不会因同一毫秒和相同 token 被合并，其他来源与非累计样本也不适用此 fallback。跨 ID 的 TTL 或累计值不同，或不满足上述证据条件时，不猜测为同一事件，保留独立记录；同 ID 仍按正常 reconcile 补齐或拒绝冲突，不绕过 `timestamp_conflict`。此规则不重写旧 event ID，也不清理已经存在的重复行。

拒绝不新增行、不覆盖原行。merge 在 preflight 的虚拟状态中同步维护语义索引，包含本批次已接受的新增/更新；所有校验与去重完成后才写入，任一拒绝会回滚整次 merge。

## Session summary

数据库没有 `sessions` 表。Session summary 从当前筛选窗口内的事件实时聚合：日期范围、event count、主模型、模型数、token buckets 和即时 estimated cost。

# Architecture

本文档描述当前 Go 实现，不等同于早期长期账本设计。

## 项目定位

AgentLedger v2 是本地优先的 usage 统计分析器。它把不同 AI Coding Agent 的本机 usage 日志解析为统一事件，存入 SQLite 后提供去重、报表、只读 API 和 Web 分析面板。

v2 不再把设备、source file、raw record、merge observation 或 conflict 作为一等账本对象。核心事实表只有 `usage_events`。

## 代码结构

```text
.
├── main.go
├── cmd/
│   ├── root.go
│   ├── init.go
│   ├── import.go
│   ├── export.go
│   ├── merge.go
│   ├── report.go
│   ├── status.go
│   ├── doctor.go
│   ├── verify.go
│   ├── vacuum.go
│   └── serve.go
├── internal/
│   ├── adapters/
│   ├── analytics/
│   ├── config/
│   ├── control/
│   ├── db/
│   ├── fingerprint/
│   ├── model/
│   └── report/
└── web/
```

核心职责：

- `cmd/`: Cobra CLI 命令入口。
- `internal/config`: 默认配置、TOML 读写、`~` 路径展开。
- `internal/adapters`: Claude/Codex/Copilot/Gemini 文件发现和 usage 解析。
- `internal/fingerprint`: 事件 fingerprint 策略和 JSON canonicalization。
- `internal/db`: SQLite 连接、schema 初始化、事件 upsert、数据库合并和统计查询。
- `internal/analytics`: 面板和 API 使用的只读 SQL 聚合。
- `internal/control`: 本机 HTTP server、只读 API、React 静态面板托管。
- `internal/report`: CLI 报表聚合和 text/JSON 输出。
- `web/`: React 只读分析面板。

## 数据流

```text
local agent logs
  -> adapter Discover(paths)
  -> adapter ParseFile(path)
  -> fingerprint.Compute(record)
  -> adapters.NormalizeModelName(model)
  -> derive token/timing fields when explicit data exists
  -> db.UpsertEvent(event)
  -> report / analytics / API / web dashboard
```

`import` 命令会读取配置并打开 SQLite，随后遍历所有启用 adapter。最近修改时间晚于 `now - gracing_minutes` 的文件会先做一次短暂的 size / mtime 稳定性检查；稳定则按快照解析，不稳定才跳过，以降低读取仍在写入文件的风险。

`serve` 命令会打开同一个 SQLite 数据库，提供只读 `/api/v1/*` JSON API，并托管 `web/dist` 中的 React 面板。API 查询实时读取当前 SQLite 状态，但不做 WebSocket 或数据库变更推送。

## SQLite

数据库默认路径由数据目录和 `[database].path` 决定。源码仓库内运行时通常是：

```text
<repo-root>/local/data/agent-ledger.db
```

连接参数：

```text
_journal_mode=WAL
_synchronous=NORMAL
_busy_timeout=5000
_foreign_keys=ON
```

当前 schema version 为 `2`，表包括：

- `meta`: schema version 和创建时间。
- `import_runs`: 每次导入的运行记录。
- `usage_events`: 核心扁平事实表，`event_id` 为主键。

`usage_events` 对 `timestamp_ms`、`channel`、`provider`、`model_normalized`、`session_id`、`output_tps`、`total_duration_ms` 建有索引，并有 `(channel, timestamp_ms)`、`(model_normalized, timestamp_ms)` 组合索引。

## 事件 fingerprint

`internal/fingerprint.Compute` 按优先级返回 ID 和策略：

1. `message_id`: `agent + provider + message_id`
2. `session_token`: `agent + provider + session_id + model + timestamp + canonical token fields + optional source_total_tokens`
3. `raw_hash`: agent + provider + canonical raw JSON
4. `fallback`: source file + line number + raw sha256

这些策略用于让同一事件在重复导入或 v2 数据库合并时保持稳定主键。对于一行只产生一个 usage event 的 Codex 日志，`import` 还会用 `source_file + line_number + raw_sha256` 识别同一原始 JSONL 行，并把当前 `event_id` 精确匹配行与同来源 sibling 联合收敛；存在 exact match 时，默认脱敏导出中 `source_file = NULL` 的候选会额外用 session、timestamp、line 和 raw hash 受限匹配，因此无论脱敏 sibling 在本地 canonical row 前后 merge，都能在后续本地 import 时参与去重。exact-row 查询会在同一事务快照中同时比较 raw envelope，并用同一组受限 identity 检查是否真的存在脱敏候选；只有该快照确认无匹配候选时才跳过额外的 redacted lookup，不缓存可能被其他数据库连接或进程写旧的状态。对于当前 source identity 可证明一致的 exact row，provider 或 model classification bundle 即使没有改变 fingerprint，也会触发同一 canonical reconciliation；当前明确解析出的 Codex classification 可以修正历史值，stored `openai` 和 explicit model 优先于 legacy/fallback/`unknown`。weak incoming 如果遇到多个冲突 explicit model，会保留全部 sibling 并等待后续明确 model，而不是使用 `imported_at_ms` / `updated_at_ms` 猜测赢家后删除数据。没有 exact event anchor、也不匹配当前非空 `source_file` 的 changed-fingerprint 跨路径记录不会仅凭弱 identity 主动折叠。收敛先选择最完整的用量 bundle，再从 token 完全相同且与 winner 已有 accounting 字段相容的单一 donor 补齐缺失 accounting metadata；source metadata 使用最早历史候选作为稳定基准，再按 missing 和路径具体度规则吸收全部候选中的互补字段。最终记录保留当前本地 source 和 raw envelope，并按实际落库字段重新计算 fingerprint。其他 adapter 可能从一行拆出多个合法事件，不使用这一兼容身份。

## Token 和 timing

Token 字段来自日志的 explicit usage envelope；当 source 把 cached input 包在 raw input 里时，adapter 入库前拆成非缓存输入和 cache read。只有 source 没给 `total_tokens` 时，才使用 adapter-specific fallback：

```text
total_tokens = fallback(input/output/cache/reasoning parts)
```

Codex 的 `reasoning_output_tokens` 是 `output_tokens` 的子项；Codex fallback 不会把 reasoning 再额外加进 total。

Timing 字段只在日志明确提供时记录或派生：

```text
ttft_ms = first_token_at_ms - request_started_at_ms
output_duration_ms = completed_at_ms - first_token_at_ms
total_duration_ms = completed_at_ms - request_started_at_ms
output_tps = output_tokens / (output_duration_ms / 1000.0)
```

不从文本长度、相邻普通 timestamp 或文件顺序推断耗时。Codex 仅在 `task_complete` 明确提供 turn timing 时关联到同 session 内上一条 usage，并保留 `turn_id`；缺失 timing 时保持 `NULL`。

## Adapter 边界

| Adapter | 默认路径 | 文件类型 | 关键解析字段 |
|---|---|---|---|
| Claude | `~/.config/claude/projects`, `~/.claude/projects` | `.jsonl` | assistant message, `message.usage`, `message.id`, `sessionId`, `requestId`, project path。 |
| Codex | `~/.codex/sessions` | `.jsonl` | `usage`, `response.usage`, `payload.info.last_token_usage`, `payload.info.total_token_usage`。 |
| GitHub Copilot | `~/.copilot/otel`, `~/.copilot/session-state` | `.jsonl` | 优先 OTel `gen_ai.usage.*`；没有 OTel 文件时回退到每条非空 `session.shutdown.data.modelMetrics` segment+model 汇总，并把包含 cache read 的 source input 拆成 `raw_input_tokens`、非缓存 `input_tokens` 和 `cache_read_tokens`。 |
| Gemini | `~/.gemini` | `.json`, `.jsonl` | `usageMetadata`, `promptTokenCount`, `candidatesTokenCount`, `totalTokenCount`。 |

所有 JSONL adapter 使用 10 MB scanner buffer，以适配较长单行日志。

## 报表

`internal/report` 通过 SQLite 聚合生成：

- `daily`: 按日期。
- `weekly`: 使用 SQLite `strftime('%Y-W%W')`。
- `monthly`: 按月。
- `models`: 按 normalized model。
- `channels`: 按 `channel`。
- `projects`: 按从 `project_path` 派生的项目标签。
- `sessions`: 按 session id。
- `slow`: 按低输出 TPS、高 TTFT 或高总耗时列出事件。

所有 report 支持 `--since`、`--until`、`--channel`、`--provider`、`--model`、`--session`、`--project`、`--json`。`daily`、`weekly`、`monthly` 额外支持 `--by channel|model|provider|session|project`，用于时间桶内维度拆分。

当前配置中的 timezone 已参与 daily / weekly / monthly 报表分桶和日期过滤；currency 尚未参与报表计算。

## 只读 API

`internal/control` 暴露：

- `/api/v1/analytics/summary`
- `/api/v1/analytics/timeseries?bucket=daily|weekly|monthly[&by=channel|model|provider|session|project]`
- `/api/v1/analytics/breakdown?by=channel|model|provider|session|project`
- `/api/v1/analytics/slow?sort=output_tps|ttft_ms|total_duration_ms&limit=50`
- `/api/v1/filter-options`
- `/api/v1/events`
- `/api/v1/import-runs`
- `/api/v1/status`
- `/api/v1/config`
- `/api/v1/health`

API 统一支持 `since`、`until`、`channel`、`provider`、`model`、`session`、`project` filters。非法日期、非法 breakdown 维度和非法 slow sort 会返回 400。

## Export / Merge

`export` 当前实现是复制本地 SQLite 数据库到目标 `.aldb` 文件。

`merge` 当前流程：

1. 展开并验证输入路径。
2. 要求输入是普通 SQLite 文件。
3. 检查 SQLite header。
4. 要求 incoming `meta.schema_version` 为 `2`。
5. `ATTACH DATABASE` 为 `incoming`。
6. 插入本地未见过的 `usage_events`。
7. 返回 inserted/skipped 数。

`merge` 仍是本地数据库文件合并工具，但 v2 不再记录设备观测历史或 conflict 审计表。

## 安全与隐私

当前工具不主动联网。隐私风险主要来自本地数据库和导出的 `.aldb` 文件。`usage_events.raw_usage_json` 会保存解析到的 usage envelope；在分享数据库、截图或面板结果前，需要把它当作私有使用数据处理。

当前 `export` 默认会在导出副本中清空路径字段和 raw usage envelope。设计中的 cleanup/quarantine、加密 raw archive 尚未落地到当前 CLI。

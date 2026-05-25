# Architecture

本文档描述当前 Go 实现，不等同于 `local/AgentLedger_design.md` 中的长期完整设计。

## 项目定位

AgentLedger 是本地优先的 CLI 账本工具，把不同 AI Coding Agent 的本机 usage 日志解析为统一事件，存入 SQLite 后提供去重、报表和跨设备合并。

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
│   └── vacuum.go
└── internal/
    ├── adapters/
    ├── config/
    ├── db/
    ├── fingerprint/
    ├── model/
    └── report/
```

核心职责：

- `cmd/`: Cobra CLI 命令入口。
- `internal/config`: 默认配置、TOML 读写、`~` 路径展开。
- `internal/adapters`: Claude/Codex/Gemini/Qwen 文件发现和 usage 解析。
- `internal/fingerprint`: 事件指纹策略和 JSON canonicalization。
- `internal/db`: SQLite 连接、schema 初始化、事件写入、数据库合并和统计查询。
- `internal/model`: 设备、事件和 source 数据结构。
- `internal/report`: SQL 聚合报表和 text/JSON 输出。

## 数据流

```text
local agent logs
  -> adapter Discover(paths)
  -> adapter ParseFile(path)
  -> fingerprint.Compute(record)
  -> adapters.NormalizeModelName(model)
  -> db.InsertEvent(event)
  -> report.Generate(...)
```

`import` 命令会先读取配置并打开 SQLite，再注册当前设备，随后遍历所有启用 adapter。最近修改时间晚于 `now - gracing_minutes` 的文件会被跳过，以降低读取仍在写入文件的风险。

## SQLite

数据库默认路径：

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

当前 schema version 为 `1`，主要表包括：

- `meta`: schema version 和创建时间。
- `devices`: 设备标识、hostname、OS/arch、版本和最近出现时间。
- `import_runs`: 每次导入的运行记录。
- `merge_runs`: 合并运行记录表，当前 merge 命令尚未写入该表。
- `sources` / `source_files` / `raw_records`: source tracking 设计表，当前导入主路径尚未填充。
- `usage_events`: 核心事件表，`event_fingerprint` 为主键。
- `event_observations` / `event_conflicts`: 观测和冲突记录设计表，当前导入主路径尚未填充。

`usage_events` 对 agent、timestamp、model、session、device 建有索引。

## 事件指纹

`internal/fingerprint.Compute` 按优先级返回指纹和策略：

1. `message_id`: `agent + provider + message_id`
2. `session_token`: `agent + provider + session_id + timestamp + input_tokens + output_tokens`
3. `raw_hash`: canonical raw JSON
4. `fallback`: source file + line number + raw sha256

这些策略用于让同一事件在重复导入或跨设备合并时保持稳定主键。

## Adapter 边界

| Adapter | 默认路径 | 文件类型 | 关键解析字段 |
|---------|----------|----------|--------------|
| Claude | `~/.claude` | `.jsonl` | assistant message, `usage` / `message.usage`, `sessionId`, `uuid`, `requestId` |
| Codex | `~/.codex` | `.jsonl` | `usage`, `response.usage`, or `payload.info.last_token_usage` |
| Gemini | `~/.gemini` | `.json`, `.jsonl` | `usageMetadata`, `promptTokenCount`, `candidatesTokenCount`, `totalTokenCount` |
| Qwen | `~/.qwen` | `.jsonl` | `usage`, `message_id`, token fields |

所有 JSONL adapter 使用 10 MB scanner buffer，以适配较长单行日志。

## 报表

`internal/report` 通过 SQLite 聚合生成：

- `daily`: 按 UTC 日期。
- `weekly`: 使用 SQLite `strftime('%Y-W%W')`。
- `monthly`: 按月，可用 `--by agent|model|provider` 组合分组。
- `models`: 按 normalized model。
- `channels`: 按 source channel。
- `devices`: join `devices`。
- `sessions`: 按 session id，当前按 `cost_usd DESC` 排序并限制 50 行。

当前配置中的 timezone/currency 尚未参与报表计算。

## 跨设备合并

`export` 当前实现是复制本地 SQLite 数据库到目标 `.aldb` 文件。

`merge` 当前流程：

1. 展开并验证输入路径。
2. 要求输入是普通文件。
3. 检查 SQLite header。
4. `ATTACH DATABASE` 为 `incoming`。
5. 统计 incoming `usage_events`。
6. `INSERT OR IGNORE` 写入本地 `usage_events`。
7. 返回 inserted/skipped 数。

合并依赖 `event_fingerprint` 主键去重。

## 安全与隐私

当前工具不主动联网。隐私风险主要来自本地数据库和导出的 `.aldb` 文件。`usage_events.raw_usage_json` 会保存解析到的 usage 原始 JSON envelope；在分享数据库前，需要把它当作私有使用数据处理。

设计中的路径脱敏、cleanup/quarantine、加密 raw archive 尚未落地到当前 CLI。

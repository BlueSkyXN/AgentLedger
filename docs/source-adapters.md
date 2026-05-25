# Source Adapters

AgentLedger v2 通过 adapter 读取本机 agent 日志，解析出统一的 `ParsedRecord`，再写入 `usage_events`。

## 支持来源

| Agent | 默认路径 | 文件类型 | 主要 usage 字段 |
|---|---|---|---|
| Claude Code | `~/.claude` | JSONL | `usage` / `message.usage`、`sessionId`、`uuid`、`requestId`、project path。 |
| Codex | `~/.codex` | JSONL | `usage`、`response.usage`、`payload.info.last_token_usage`。 |
| Gemini CLI | `~/.gemini` | JSON / JSONL | `usageMetadata`、`promptTokenCount`、`candidatesTokenCount`、`totalTokenCount`。 |
| Qwen | `~/.qwen` | JSONL | `usage`、`message_id`、token fields。 |

## Parsed fields

Adapter 会尽量提供：

- `Agent`: 写入 `channel`，例如 `claude`、`codex`、`gemini`、`qwen`。
- `Provider`
- `Model`
- `TimestampMs`
- `SessionID`
- `ProjectPath`
- `MessageID`
- `RequestID`
- token fields
- `CostUSD`
- explicit timing fields
- `SourceFile`
- `LineNumber`
- `RawSHA256`
- `RawUsageJSON`

## Timing 边界

Adapter 拿不到 explicit timing 时必须留空。AgentLedger 不从文本长度、相邻 timestamp 或文件顺序推断耗时。

可写入或派生的 timing 字段包括：

- `request_started_at_ms`
- `first_token_at_ms`
- `completed_at_ms`
- `total_duration_ms`
- `ttft_ms`
- `output_duration_ms`
- `output_tps`

## Source tracking 边界

v2 不再写入 `sources`、`source_files` 或 `raw_records` 表，因为这些表已经从 schema 删除。来源定位信息直接保存在 `usage_events.source_file`、`usage_events.line_number` 和 `usage_events.raw_sha256`。

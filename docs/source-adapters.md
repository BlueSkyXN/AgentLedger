# Source Adapters

AgentLedger v2 通过 adapter 读取本机 agent 日志，解析出统一的 `ParsedRecord`，再写入 `usage_events`。

## 支持来源

| Agent | 默认路径 | 文件类型 | 主要 usage 字段 |
|---|---|---|---|
| Claude Code | `~/.config/claude/projects`, `~/.claude/projects` | JSONL | `message.usage`、`message.id`、`requestId`、`sessionId`、project path。 |
| Codex | `~/.codex/sessions` | JSONL | `usage`、`response.usage`、`payload.info.last_token_usage`、`payload.info.total_token_usage`。 |
| GitHub Copilot | `~/.copilot/otel` | JSONL | OTel `gen_ai.usage.*` token telemetry。 |
| Gemini CLI | `~/.gemini` | JSON / JSONL | `usageMetadata`、`promptTokenCount`、`candidatesTokenCount`、`totalTokenCount`。 |
| Qwen | `~/.qwen` | JSONL | Experimental，默认关闭；`usage`、`message_id`、token fields。 |

## Source-specific accounting

### Claude Code

Claude Code 的 token 口径与 ccusage 对齐：单条 usage 的 `total_tokens` 按 `input_tokens + output_tokens + cache_creation_input_tokens + cache_read_input_tokens` 计算。

Claude Code 日志会把同一次 assistant message 以多条流式行写出。AgentLedger 以 `message.id + requestId` 作为自然事件 key，并在同 key 重复时保留 token total 更大的记录；sidechain replay 记录按 ccusage 口径优先保留非 sidechain 版本。缺少 `message.id` 的旧格式记录才回退到 `uuid`。

`model == "<synthetic>"` 且 token total 为 0 的记录不会写入统计；`usage.speed == "fast"` 时，模型名追加 `-fast` 后缀，避免和 standard model 混在同一 model 维度。

### Codex

Codex 默认只扫描 `~/.codex/sessions/**/*.jsonl`，与 ccusage 的默认范围保持一致；`~/.codex/archived_sessions` 不会自动导入。需要统计归档历史时，可以在 config 的 `agents.codex.paths` 中显式加入归档目录，或用符号链接把归档文件纳入扫描路径。

Codex 的 `total_token_usage` 是 cumulative counter。AgentLedger 优先使用 `last_token_usage` 作为单次用量；同时用 `last_token_usage + total_token_usage` 快照过滤同一 session 内重复写出的 token count 行，避免 rate-limit 刷新或重复事件被累计多次。`last_token_usage` 缺失时，按同一 session 的 `current_total - previous_total` 逐字段计算，并使用 saturating subtraction，counter reset 不会产生负数，也不会把当前 cumulative 值当成新的单次用量。

当 Codex 事件来自 `total_token_usage` delta 路径时，`usage_events.total_tokens` 是本次增量，`usage_events.source_total_tokens` 保留源日志里的 raw cumulative total，仅用于排查和交叉验证，不应对该列做 `SUM()` 作为用量报表。

### GitHub Copilot

GitHub Copilot token accounting requires local OTel JSONL telemetry via `~/.copilot/otel` or `COPILOT_OTEL_FILE_EXPORTER_PATH`.

Copilot 的 `session-state` / `session-store.db` 本轮不导入 usage report。原因是这些本地 activity 数据通常缺少完整的 input/cache/reasoning/total token envelope，直接纳入会污染标准 token 统计。未来如果需要，可以作为 partial activity ledger 单独支持。

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

新写入事件会同时写入 `channel` 和 `source_agent`，并保持二者一致。`source_product` 用于区分 `claude-code`、`codex-cli`、`copilot-otel` 等具体来源形态。

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

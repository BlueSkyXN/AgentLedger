# Source Adapters

AgentLedger v2 通过 adapter 读取本机 agent 日志，解析出统一的 `ParsedRecord`，再写入 `usage_events`。

## 支持来源

| Agent | 默认路径 | 文件类型 | 主要 usage 字段 |
|---|---|---|---|
| Claude Code | `~/.config/claude/projects`, `~/.claude/projects` | JSONL | `message.usage`、`message.id`、`requestId`、`sessionId`、project path。 |
| Codex | `~/.codex/sessions` | JSONL | `usage`、`response.usage`、`payload.info.last_token_usage`、`payload.info.total_token_usage`。 |
| GitHub Copilot | `~/.copilot/otel`, `~/.copilot/session-state` | JSONL | 优先 OTel `gen_ai.usage.*` token telemetry；没有 OTel 文件时回退到 `session.shutdown.data.modelMetrics` session+model 汇总。 |
| Gemini CLI | `~/.gemini` | JSON / JSONL | `usageMetadata`、`promptTokenCount`、`candidatesTokenCount`、`totalTokenCount`。 |
| Qwen | `~/.qwen` | JSONL | Experimental，默认关闭；`usage`、`message_id`、token fields。 |

## Source-specific accounting

### Claude Code

Claude Code 的 token 口径与 ccusage 对齐：单条 usage 的 `total_tokens` 按 `input_tokens + output_tokens + cache_creation_input_tokens + cache_read_input_tokens` 计算。

Claude Code 日志会把同一次 assistant message 以多条流式行写出。AgentLedger 以 `message.id + requestId` 作为自然事件 key，并在同 key 重复时保留 token total 更大的记录；sidechain replay 记录按 ccusage 口径优先保留非 sidechain 版本。缺少 `message.id` 的旧格式记录才回退到 `uuid`。

`model == "<synthetic>"` 且 token total 为 0 的记录不会写入统计；`usage.speed == "fast"` 时，模型名追加 `-fast` 后缀，避免和 standard model 混在同一 model 维度。

### Codex

Codex 默认只扫描 `~/.codex/sessions/**/*.jsonl`，与 ccusage 的默认范围保持一致；当配置路径写成 Codex home 根目录（例如 `~/.codex`）时，adapter 会优先收敛到其 `sessions` 子目录，避免把 history、临时文件或其他 JSONL 混入 usage。`~/.codex/archived_sessions` 不会自动导入。需要统计归档历史时，可以在 config 的 `agents.codex.paths` 中显式加入归档目录，或用符号链接把归档文件纳入扫描路径。

Codex 的 `total_token_usage` 是 cumulative counter。AgentLedger 优先使用 `last_token_usage` 作为单次用量；默认 `duplicate_policy = "ledger"` 会用 `last_token_usage + total_token_usage` 快照过滤同一 session 内重复写出的 token count 行，避免 rate-limit 刷新或重复事件被累计多次。这个去重口径会使 AgentLedger 低于 `ccusage codex`：ccusage 的 Codex 报表按 timestamp + model + token tuple 去重，同一快照只要 timestamp 不同仍会计入。需要对齐 ccusage 时，可在 `[agents.codex]` 下设置 `duplicate_policy = "ccusage_compatible"` 后重建或重新导入独立数据库。`last_token_usage` 缺失时，按同一 session 的 `current_total - previous_total` 逐字段计算，并使用 saturating subtraction，counter reset 不会产生负数，也不会把当前 cumulative 值当成新的单次用量。

Codex 日志里的 `input_tokens` 包含 cached input。入库时 AgentLedger 会拆成 `input_tokens = raw_input_tokens - cached_input_tokens` 和 `cache_read_tokens = cached_input_tokens`，使表内 token 分项和 Claude/ccusage 报表的非缓存输入口径一致；`raw_input_tokens` 保存源日志原始 input，`source_total_tokens` 仍保留源日志 raw cumulative total。

当 Codex 事件来自 `total_token_usage` delta 路径时，`usage_events.total_tokens` 是本次增量，`usage_events.source_total_tokens` 保留源日志里的 raw cumulative total，仅用于排查和交叉验证，不应对该列做 `SUM()` 作为用量报表。

Codex 的 `task_complete.duration_ms`、`task_complete.time_to_first_token_ms` 和 `turn_id` 会按同一 session 内紧邻的上一条 usage 记录落为 turn 级 timing。这个值包含 Codex turn 的端到端耗时边界，不等同于严格的单次模型 API latency。`session_path_id` 保存相对 `sessions` 的路径 ID，例如 `2026/05/27/rollout-...`，用于和 ccusage 的 session 粒度对齐。

`agent-ledger doctor codex` 会扫描 configured paths，输出 raw `token_count` 覆盖、`task_complete` timing 覆盖、默认 ledger 口径与 `ccusage_compatible` 口径的事件数/token 差异，以及模型分布。

### GitHub Copilot

GitHub Copilot 优先读取本地 OTel JSONL telemetry：`~/.copilot/otel` 或 `COPILOT_OTEL_FILE_EXPORTER_PATH`。OTel 事件按 `gen_ai.usage.*` 字段生成请求级记录，`source_product = copilot-otel`。只要发现 OTel usage 文件，默认不会再导入 `session-state` 的 shutdown 汇总，避免请求级数据和 session 汇总双计数。

没有 OTel 文件时，Copilot adapter 会读取 `~/.copilot/session-state/*/events.jsonl` 里的 `session.shutdown` 事件。该事件的 `data.modelMetrics.<model>.usage` 提供 `inputTokens`、`outputTokens`、`cacheReadTokens`、`cacheWriteTokens`、`reasoningTokens`，AgentLedger 会为每个 session+model 写入一条 aggregate usage event，`source_product = copilot-session-state`，`token_accounting_method = copilot_session_model_metrics`。未 shutdown 的活跃 session 不会产生这类汇总记录。

`assistant.message.outputTokens` 只提供输出 token，通常没有 input/cache/reasoning envelope；当前不会把它作为 per-request usage 导入，避免和 `session.shutdown` 汇总重复或污染标准 token 统计。

## Parsed fields

Adapter 会尽量提供：

- `Agent`: 写入 `channel`，例如 `claude`、`codex`、`copilot`、`gemini`、`qwen`。
- `Provider`
- `Model`
- `TimestampMs`
- `SessionID`
- `SessionPathID`
- `TurnID`
- `ProjectPath`
- `MessageID`
- `RequestID`
- token fields
- `RawInputTokens`
- `CostUSD`
- `TokenAccountingMethod`
- `AccountingProfile`
- explicit timing fields
- `SourceFile`
- `LineNumber`
- `RawSHA256`
- `RawUsageJSON`

新写入事件会同时写入 `channel` 和 `source_agent`，并保持二者一致。`source_product` 用于区分 `claude-code`、`codex-cli`、`copilot-otel`、`copilot-session-state` 等具体来源形态。

## Timing 边界

Adapter 拿不到 explicit timing 时必须留空。AgentLedger 不从文本长度或相邻普通 timestamp 推断耗时；Codex 仅在 `task_complete` 明确给出 turn timing 时做同 session 的上一条 usage 关联。

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

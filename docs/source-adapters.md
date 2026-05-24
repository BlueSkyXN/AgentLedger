# Source Adapters

AgentLedger 通过 adapter 发现并解析不同 AI Coding Agent 的本机 usage 日志。当前 adapter 接口在 `internal/adapters/adapter.go`：

```go
type Adapter interface {
    Name() string
    Discover(paths []string) ([]string, error)
    ParseFile(path string) ([]*fingerprint.ParsedRecord, error)
}
```

## 共同导入行为

- `Discover(paths)` 会递归扫描 configured paths。
- JSONL adapter 使用 10 MB scanner buffer，以处理较长单行日志。
- `import` 会跳过修改时间晚于 `now - gracing_minutes` 的文件。
- 每条 ParsedRecord 会计算 fingerprint，然后写入 `usage_events`。
- 当前导入路径不会写入 `source_files` 或 `raw_records` 表。

## Claude Code

| 项 | 当前实现 |
|---|---|
| Adapter name | `claude` |
| 默认路径 | `~/.claude` |
| 文件类型 | `.jsonl` |
| Provider | `anthropic` |

解析口径：

- 只处理 `type == "assistant"` 的 JSONL 行。
- usage 可来自顶层 `usage` 或 `message.usage`。
- model 可来自顶层 `model` 或 `message.model`。
- session 优先用 `sessionId`，缺失时从路径推断。
- message id 使用 `uuid`，request id 使用 `requestId`。
- cache token 字段读取 `cache_creation_input_tokens` 和 `cache_read_input_tokens`。

## Codex

| 项 | 当前实现 |
|---|---|
| Adapter name | `codex` |
| 默认路径 | `~/.codex` |
| 文件类型 | `.jsonl` |
| Provider | `openai` |

解析口径：

- usage 可来自顶层 `usage`、`response.usage`，或 `payload.info`。
- 当 `payload.info.last_token_usage` 存在时优先使用它，避免把 cumulative `total_token_usage` 当作单条事件。
- session 从文件名推断。
- input tokens 同时兼容 `input_tokens` 和 `prompt_tokens`。
- output tokens 同时兼容 `output_tokens` 和 `completion_tokens`。
- reasoning tokens 同时兼容 `reasoning_tokens` 和 `reasoning_output_tokens`。

## Gemini CLI

| 项 | 当前实现 |
|---|---|
| Adapter name | `gemini` |
| 默认路径 | `~/.gemini` |
| 文件类型 | `.json`, `.jsonl` |
| Provider | `google` |

解析口径：

- JSON 文件支持单个 object 或 object array。
- JSONL 文件逐行解析。
- usage 可来自顶层 `usageMetadata` 或 `response.usageMetadata`。
- token 字段读取 `promptTokenCount`、`candidatesTokenCount`、`totalTokenCount`。

## Qwen

| 项 | 当前实现 |
|---|---|
| Adapter name | `qwen` |
| 默认路径 | `~/.qwen` |
| 文件类型 | `.jsonl` |
| Provider | `alibaba` |

解析口径：

- 逐行读取 JSONL。
- usage 来自顶层 `usage`。
- session 从文件名推断。
- message id 使用 `message_id`。
- input/output tokens 兼容 `input_tokens`/`prompt_tokens` 和 `output_tokens`/`completion_tokens`。

## 新增 adapter 的最低要求

- 先用真实样例或 fixture 确认日志格式，不要凭字段名猜测。
- 明确 provider、model、timestamp、session、message/request id 和 token 字段映射。
- 补 fingerprint 或 adapter 单元测试，覆盖重复导入和关键字段缺失。
- 不要在 adapter 里实现报表、merge 或 cleanup；adapter 只负责发现和解析。

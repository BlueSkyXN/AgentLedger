# Reports and Merge

本文档覆盖当前已实现的报表与 schema v2 数据库合并行为。

## 通用 report flags

所有 report 子命令都暴露：

```bash
--since YYYY-MM-DD
--until YYYY-MM-DD
--channel string
--provider string
--model string
--session string
--json
```

日期过滤使用事件 `timestamp_ms`，并按 `[reports].timezone` 解释 `--since` / `--until` 的本地日期边界。daily / weekly / monthly 分桶也使用同一时区配置。

## Report types

| Command | 分组 / 行 | 排序 |
|---|---|---|
| `report daily` | 日期 | date desc |
| `report weekly` | `strftime('%Y-W%W')` | week desc |
| `report monthly` | 月份 | month desc |
| `report models` | `model_normalized` / `model_raw` | total tokens desc |
| `report channels` | `channel` | total tokens desc |
| `report sessions` | `session_id` | total tokens desc |
| `report slow` | 单个 timed event | sort flag 决定 |

聚合报表输出：

```text
Label, Events, Tokens, Input, Output, Cache Create, Cache Read, Reasoning, Avg Duration, Avg TTFT, Avg TPS, Recorded Cost
```

JSON 输出使用同一语义字段：

```json
[
  {
    "label": "claude",
    "events": 10,
    "total_tokens": 12345,
    "input_tokens": 8000,
    "output_tokens": 3000,
    "cache_creation_tokens": 200,
    "cache_read_tokens": 145,
    "reasoning_tokens": 1000,
    "avg_total_duration_ms": 12000,
    "avg_ttft_ms": 900,
    "avg_output_tps": 42.5,
    "recorded_cost_usd": 0.12
  }
]
```

Timing 平均值只统计非 `NULL` 指标。没有 explicit timing 的事件不会被硬推断。

## Slow report

```bash
agent-ledger report slow
agent-ledger report slow --sort output_tps --limit 50
agent-ledger report slow --sort ttft_ms --limit 20
agent-ledger report slow --sort total_duration_ms --channel codex
```

Sort allowlist：

| Sort | 语义 |
|---|---|
| `output_tps` | 输出 TPS 升序，越低越慢。 |
| `ttft_ms` | TTFT 降序，越高越慢。 |
| `total_duration_ms` | 总耗时降序，越高越慢。 |

## Export

```bash
agent-ledger export --output usage.aldb
```

当前 export 是简单文件复制：

- 源文件是当前配置指向的 SQLite 数据库。
- 输出为空时默认 `agent-ledger-export.aldb`。
- 不按时间过滤。
- 不脱敏。
- 不压缩。

## Merge

```bash
agent-ledger merge usage.aldb
```

当前 merge 流程：

1. 解析输入路径为 absolute path。
2. 确认路径存在且不是目录。
3. 读取 SQLite header，要求是 SQLite database。
4. `ATTACH DATABASE` 为 `incoming`。
5. 要求 `incoming.meta.schema_version` 为 `2`。
6. 统计 incoming `usage_events`。
7. 插入本地未见过的 `usage_events`。
8. 返回 inserted 和 duplicate skipped 数量。

去重依据是 `usage_events.event_id` 主键。

## Merge 限制

- 当前只合并 `usage_events`。
- 当前不会记录设备级 observation history。
- 当前不会记录 conflict 审计。
- 输入文件必须是 schema v2 AgentLedger SQLite 数据库。

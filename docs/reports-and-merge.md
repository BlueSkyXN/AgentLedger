# Reports and Merge

本文档覆盖当前已实现的报表与跨设备合并行为。

## 通用 report flags

所有 report 子命令都暴露：

```bash
--since YYYY-MM-DD
--until YYYY-MM-DD
--json
--by agent|model|provider
```

当前只有 `report monthly` 实际使用 `--by`。其它 report 子命令会读取该 flag，但不会改变分组。

日期过滤使用 SQLite：

```sql
date(timestamp_ms/1000, 'unixepoch')
```

当前 `[reports].timezone` 尚未参与计算。

## Report types

| Command | 分组 | 排序 |
|---|---|---|
| `report daily` | UTC date | date desc |
| `report weekly` | `strftime('%Y-W%W')` | week desc |
| `report monthly` | month 或 `agent/model/provider + month` | label desc |
| `report models` | `model_normalized` / `model_raw` | total tokens desc |
| `report channels` | `source_channel` | total tokens desc |
| `report devices` | `origin_device_id` joined with `devices` | total tokens desc |
| `report sessions` | `session_id` | cost desc, limit 50 |

输出列：

```text
Label, Events, Tokens, Input, Output, Cost(USD)
```

JSON 输出使用同一数据结构：

```json
[
  {
    "label": "example",
    "events": 1,
    "total_tokens": 100,
    "input_tokens": 60,
    "output_tokens": 40,
    "cost_usd": 0
  }
]
```

## Monthly grouping

```bash
agent-ledger report monthly
agent-ledger report monthly --by agent
agent-ledger report monthly --by model
agent-ledger report monthly --by provider
```

非法 `--by` 值会返回错误。当前 allowlist 是 `agent`、`model`、`provider`。

## Export

```bash
agent-ledger export --output device-a.aldb
```

当前 export 是简单文件复制：

- 源文件是当前配置指向的 SQLite 数据库。
- 输出为空时默认 `agent-ledger-export.aldb`。
- 不按时间过滤。
- 不脱敏。
- 不压缩。

## Merge

```bash
agent-ledger merge device-a.aldb
```

当前 merge 流程：

1. 解析输入路径为 absolute path。
2. 确认路径存在且不是目录。
3. 读取 SQLite header，要求是 SQLite database。
4. `ATTACH DATABASE` 为 `incoming`。
5. 统计 `incoming.usage_events`。
6. `INSERT OR IGNORE INTO usage_events ... SELECT ... FROM incoming.usage_events`。
7. 返回 inserted 和 duplicate skipped 数量。

去重依据是 `usage_events.event_fingerprint` 主键。

## Merge 限制

- 当前只合并 `usage_events`。
- 当前不写 `merge_runs`。
- 当前不写 `event_observations`，因此不会记录“同一事件被哪些设备观察到”的多行历史。
- 当前不会自动合并 source file tracking 表。
- 输入文件必须是当前 schema 可读的 AgentLedger SQLite 数据库。

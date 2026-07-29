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
--project string
--cost recorded|estimated|both|none
--pricing path/to/pricing.json
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
| `report projects` | 从 `project_path` 派生的项目标签 | total tokens desc |
| `report sessions` | `session_id` | total tokens desc |
| `report slow` | 单个 timed event | sort flag 决定 |

`report daily`、`report weekly`、`report monthly` 可加 `--by channel|model|provider|session|project`，在每个时间桶内继续拆分 token 分项，适合做按时间的模型/渠道/项目用量趋势。

聚合报表输出：

```text
Label, Events, Tokens, Input, Output, Cache Create, Cache Read, Reasoning, Avg Duration, Avg TTFT, Avg TPS, Recorded Cost
```

默认 `--cost recorded` 显示 `Recorded Cost(USD)`。`--cost estimated` 显示 `Estimated Cost(USD)`、pricing coverage 和 pricing confidence；`--cost both` 同时显示 recorded 与 estimated；`--cost none` 隐藏成本列。estimated cost 使用 `pricing/pricing.v1.json` 或 `--pricing` 指定的 JSON profile，按 input/output/cache creation/cache read token bucket 估算；默认 profile 的 reasoning policy 是 `included_in_output`，不会再对独立 `reasoning_tokens` 重复收费。估算不使用 `total_tokens * 单价`，也不会写回 `usage_events`。

Pricing rule 只按配置中的 model ID exact/glob pattern、时间窗口和显式 condition 判断，不依赖 Fast/Standard service tier。匹配前会 trim、转小写，并为结尾的 `(reasoning=...)` / `(effort=...)` 生成去后缀 alias；不会自行剥离 provider/tenant 前缀。新同步的 catalog model 和 alias 使用显式 exact pattern；`kimi-k3`、`k3`、`k3-256` 和 `k3-256k` 使用同一条 Kimi K3 价格规则。`hy3` 和 `tencent/hy3` 使用 OpenRouter 页面 Tencent Cloud provider 行的价格，不使用页面顶部可能变化的折扣价。`claude-opus-5` 和 `anthropic/claude-opus-5` 使用 Anthropic 官方标准 API 价格；当前事件不携带 cache TTL，因此 cache creation 按官方 5 分钟 write 价格估算，Fast mode 不纳入。普通未命中 ID 保持 `missing_pricing_rule`；来源没有 model 时由 adapter 明确写入 `unknown`，内置 profile 对精确 `unknown` 使用 `policy_zero`：estimated cost 明确显示为 `$0`，事件和 Tokens 单独披露，且不计入真实价格覆盖。已核验的显式免费模型仍属于正常 `priced`，不能与 `policy_zero` 混淆。

内置 profile 对 GPT-5.4、GPT-5.5、GPT-5.6 的精确 canonical ID 在完整 input side 达到 `272000` tokens 时切换到 long-context rule；Grok 4.5、Grok 4.3 及其明确 alias 的阈值是 `200000`。完整 input side 是 `input_tokens + cache_creation_tokens + cache_read_tokens`。达到阈值后，该事件的 input、cache read、cache creation 和 output 全部按 long rule 计价，不是只对超过阈值的 token 累进加价。宽泛或未核实的 ID（例如带租户前缀的模型名）不会自动继承这些长上下文价格。

`claude-sonnet-5` 不使用长上下文溢价，而是按事件日期选择价格：截至 `2026-08-31` 使用 Intro 规则，自 `2026-09-01` 起使用 Standard 规则。当前来源没有提供明确的 cache-write TTL 单价，因此 cache creation 暂按普通 Input 单价估算；Standard Cached Input 暂按 Input 的 10% 估算。这两项在 profile 中保留 `estimated` confidence 和说明，不把推测写成精确账单价格。

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
    "recorded_cost_usd": 0.12,
    "estimated_cost_usd": 0.34,
    "estimated_cost_micro_usd": 340000,
    "pricing": {
      "profile_id": "agentledger-pricing-2026-07-25",
      "currency": "USD",
      "priced_events": 10,
      "total_events": 10,
      "priced_tokens": 12345,
      "total_tokens": 12345,
      "coverage_ratio": 1,
      "confidence": "estimated"
    }
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

当前 export 使用 SQLite `VACUUM INTO` 生成 `.aldb` 副本：

- 源文件是当前配置指向的 SQLite 数据库。
- 输出为空时默认 `agent-ledger-export.aldb`。
- 不按时间过滤。
- 默认按 `[privacy].redact_paths_on_export = true` 清空 `project_path`、`source_file` 和 `raw_usage_json`；关闭该配置时导出未脱敏副本。
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
7. 在同一个 destination transaction 中插入本地未见过的 `usage_events`；所有 incoming `raw_usage_json` 均省略并写为 `NULL`。
8. 返回 inserted、duplicate skipped 和 `Raw evidence omitted` 数量。

去重依据是 `usage_events.event_id` 主键。

重复 `event_id` 不覆盖本地结构化统计事实。merge 保持规范化 usage event 的插入/跳过契约，不持久化 incoming `raw_usage_json`；SQL/锁错误会回滚整个 destination transaction。

## Merge 限制

- 当前只合并 `usage_events`。
- 当前不会记录设备级 observation history。
- 当前不会记录 conflict 审计。
- 输入文件必须是 schema v2 AgentLedger SQLite 数据库。

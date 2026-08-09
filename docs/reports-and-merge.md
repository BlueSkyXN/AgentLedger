# Reports and Merge

## 指标口径

所有聚合来自 `usage_events`：

- `events` / `event_count`：筛选窗口内 canonical usage event 行数。
- `total_sessions`：distinct `session_key`。
- `input/output/reasoning/cache_creation/cache_read/total_tokens`：各事实列求和。
- `first_date/last_date`：筛选事件的最早/最晚日历日期，不是工作时长。
- `primary_model`：Session 内 total token 最大；并列按 normalized model ID 字典序。

`source_total_tokens` 是来源诊断，不作为 canonical total 汇总。

## 日期

数据库存 UTC epoch ms。daily/weekly/monthly 使用 `reports.timezone` 的历史 IANA offset：

- daily：本地日历日。
- weekly：本地周一日期。
- monthly：本地 `YYYY-MM`。

`--since YYYY-MM-DD` 包含该 timezone 当日 00:00；`--until YYYY-MM-DD` 包含整日并在下一日 00:00 排他结束。

## Estimated cost

cost 不落库。每次查询用当前 profile 和事件的 provider、channel、model、timestamp、token buckets 估算。

- `priced`：规则匹配并计算金额。
- `unpriced`：模型/规则不匹配，金额为 null，不伪造 0。
- `policy_zero`：内置 unknown policy 的零值，单独标记，不等同于官方免费。
- `unavailable`：配置 profile 无效或估算失败；usage/tokens 仍返回。

`token_coverage_ratio` 只计算实际进入价格 bucket 的 tokens。规则虽匹配但事件使用的全部非零 bucket 都没有对应 rate 时，仍按 unpriced/null 处理。对于 `observability_level=partial` 且 source total 大于已知分项的事件，金额和 coverage 都只代表已知下界，confidence 为 `partial`；不会把无法归入价格 bucket 的 tokens 计成已计价。Codex output 侧按 `max(output, reasoning)`，Copilot 的独立 reasoning 计入 output rate，WorkBuddy completion 已包含 reasoning，不重复计价。

显式 `--pricing` 无效时 CLI 失败；配置 `reports.pricing_path` 无效时查询成功但 `pricing.error_code=pricing_profile_invalid`。

## `.aldb` merge

输入必须：

```text
meta.schema_version = 3
meta.identity_version = 2
完整 v3 三表和 usage_events 必需列
event_id/session_key 为 lowercase 64-hex SHA-256 encoding
content_sha256 可由当前持久化 usage facts 重算
```

流程：

1. 只读打开 incoming 并验证 schema/identity、hash encoding 和逐行 content hash。
2. 在 destination transaction 中读取已有事件。
3. 对全部 incoming event 运行与 import 相同的 reconcile preflight。
4. 若有任一 token/session/time/content/直接模型/accounting 冲突，rollback 并返回按 reason code 汇总的数量。
5. 无冲突才执行 insert/update/skip 并 commit。

不合并 incoming `import_runs`，也不创建 `merge_runs` 或 conflict table。

v3 行没有持久化生成 `event_id` 的完整 native identity tuple，因此 merge gate 能验证 hash encoding 和结构化 content 自洽，但不把 `.aldb` 当作对抗恶意伪造的签名容器。只合并由受信任 AgentLedger 实例生成和保管的本地 `.aldb`；若要从行内完整重算 event identity，需要另行升级 schema 并重建数据库。

## Redacted roundtrip

默认 export 清空：

```text
project_path
source_file
import_runs.error
```

必须保持：

```text
event_id
session_key
content_sha256
timestamp/model/token/accounting facts
```

重复 merge 同一 export 应为 inserted 0、updated 0、全部 skipped；本机完整事件重新出现时，可以只补回 export 清空的 locator metadata，而不改变 identity 或 token facts。

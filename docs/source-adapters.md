# Source Adapters

每个 adapter 必须输出：稳定 Session、事件 identity、有效 timestamp、模型证据、token buckets、accounting method/profile、parser version 和 event granularity。Adapter 只选择权威 usage 来源，不把 request event 与 session summary 同时导入造成重复计数。

## 公共规则

- timestamp 必须有效，token 必须非负。
- 无 native Session ID 时只允许 source-root-relative Session path；不能用 absolute source file。
- 稳定 native event identity 不含 model、token、timestamp、provider、cost 或设备。
- 一条 source record 拆多个 model/segment 时必须提供稳定 `identity_subkey`。
- 没有 native event ID 时优先使用 Session 内稳定的 `session_record(line + subkey)`；line 只在 source-root-relative Session 内解释，不与 absolute path 组合。
- 最后才用 `content_fallback`；它只保证完全相同内容重复 skip。
- 完整 source JSON 只在解析期存在；不保存正文或 raw usage。
- `raw_sha256` 是原始记录诊断 hash，不等于结构化 `content_sha256`。

## Claude Code

```text
channel = claude
source_product = claude-code
provider = anthropic（来源模型正常化可修正）
parser_version = claude-v1
event_granularity = request
```

Session 优先原生 session ID，回退到 Claude source-root-relative project/session path。事件优先 message ID，再用 request ID；message 内多 segment 通过 subkey 分开。optional/null 字段用结构化 JSON 类型判断，不把字符串匹配当 schema。

Token 使用 `claude_usage_sum`，包括 input、output、cache creation、cache read。来源 cost 不落库。

## Codex

```text
channel = codex
source_product = codex-cli
provider = openai（可由明确来源证据覆盖 unknown）
parser_version = codex-v1
event_granularity = request
```

Session 优先原生 session ID，回退到 `sessions`/`archived_sessions` root-relative path。事件优先明确 event/message/request ID；都缺失时使用 `session_record(line + subkey)`。紧随 usage 的 `task_complete.turn_id` 会把该事件 identity 升级为 `session_turn`，不保存 timing。

`ledger` 对累计 `total_token_usage` 做 per-session reset-aware delta；`ccusage_compatible` 优先 `last_token_usage`，缺失时用累计 delta。source total 是权威 `total_tokens`；缓存 input 从 raw input 分离，reasoning 可能包含在 output 中。当累计 counter 局部回退导致已知分项不能完整解释 source total 时，保留 source total、把分项限制在 total 内并标记 `observability_level=partial`，不会把分项缺口伪造成某个 token bucket。该情况按 `codex_accounting_partial` diagnostic 汇总。replay matcher 继续 fail-closed。

## GitHub Copilot

OTel 存在时选择 OTel request events，不再同时导入 session-state summary：

```text
source_product = copilot-otel
parser_version = copilot-otel-v1
event_granularity = request
identity = trace+span → response ID → interaction ID
```

没有 OTel usage 文件时使用每条非空 `session.shutdown.data.modelMetrics.<model>`：

```text
source_product = copilot-session-state
parser_version = copilot-session-state-v1
event_granularity = session_model
identity = shutdown ID + model subkey
```

OTel 已有 trace/span identity 时，后补 response/interaction ID 只作为关联 metadata，不改变 event identity。Copilot input/cache 按 `input_includes_cache_read` profile 归一化。`requests.count`、request cost、premium/nano 指标和 duration 不落库，也不形成跨 Agent KPI。session-state 无 shutdown ID 时使用 `session_record(line + model)`；不能制造统一 shutdown ID，也不能把 absolute `path:line` 放入 identity。`raw_sha256` 对完整 source record 求值。

## Gemini CLI

```text
channel = gemini
source_product = gemini-cli
provider = google
parser_version = gemini-v1
event_granularity = request
```

从 root/response 的 usage metadata 读取 prompt、cache、candidates、thoughts/reasoning、tool-input 和 total；按 `gemini_usage_v1` 验证包含关系。扩展原生 Session/event/message/request/response candidate；无稳定 event ID 时使用 `session_record(line + subkey)`。缺稳定 Session、timestamp，token 为负或 total 不守恒的记录拒绝。

## WorkBuddy

```text
channel = workbuddy
source_product = workbuddy
parser_version = workbuddy-v1
event_granularity = request
identity = root id + native session
```

`rawUsage.prompt_tokens` 拆成非缓存 input、cache read、cache creation；completion 包含 reasoning，`total_tokens` 使用来源总量并由 `workbuddy_raw_usage_v1` 验证。`auto` 是路由状态，保存 `model_normalized=unknown`、fallback/policy-zero；credit、正文、URL、key 和完整 providerData 不落库。

## Parser contract 测试

每个 adapter 使用 synthetic fixture 覆盖：identity precedence、稳定 Session、同 native ID 下 model/token/path 变化不改变 event ID、subkey 拆分、非法 timestamp/token、accounting 守恒和二次 import 幂等。fixture 不包含真实 Session、路径或客户数据。

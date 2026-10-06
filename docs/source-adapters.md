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

Session 优先原生 session ID，回退到 `sessions`/`archived_sessions` root-relative path。事件优先明确 event/message/request ID；都缺失时使用 `session_record(line + subkey)`。后到的 `task_complete.turn_id` 只补关联 metadata，不重写已经可导入的 usage event identity，也不保存 timing。

`ledger` 对累计 `total_token_usage` 做 per-session reset-aware delta；`ccusage_compatible` 优先 `last_token_usage`，缺失时用累计 delta。source total 是权威 `total_tokens`；缓存 input 从 raw input 分离，reasoning 可能包含在 output 中。当累计 counter 局部回退导致已知分项不能完整解释 source total 时，保留 source total、把分项限制在 total 内并标记 `observability_level=partial`，不会把分项缺口伪造成某个 token bucket。该情况按 `codex_accounting_partial` diagnostic 汇总。replay matcher 继续 fail-closed。

## Cursor

```text
channel = cursor
source_product = cursor-agent-exec
provider = unknown
parser_version = cursor-agent-exec-v1
event_granularity = request
```

只扫描 `exthost/anysphere.cursor-agent-exec/Cursor Agent Exec*.log` 中的 `Setting token details for client token ring` usage 行。`usedTokens`、`inputTokens`、`outputTokens`、`cacheReadTokens`、`cacheWriteTokens` 必须全部存在、非负，并满足：

```text
usedTokens = inputTokens + outputTokens
cacheReadTokens + cacheWriteTokens <= inputTokens
```

Cursor 的 `inputTokens` 是包含 cache 的 raw input。canonical `input_tokens` 会减去 cache read/write，`source_total_tokens` 保留 `usedTokens`；全零行跳过，非法行聚合 warning 后跳过。来源未提供 provider、request ID 或 conversation/composer ID，因此 provider 保持 `unknown`，Session 使用 `cursor-agent-exec/YYYY-MM-DD` 本地日期日志桶，不能解释成 Cursor UI 对话。日期桶不依赖文件名，日志轮转或移动不会改变 Session key。

事件使用 global `content_fallback`，语义包含毫秒 timestamp、action、model 与完整 token 分项，因此日志复制、轮转和重复 import 不重复累计。若两个真实调用在同一毫秒具有完全相同的上述字段，来源证据无法区分，adapter 会按同一事件处理。

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

## ZCode

```text
channel = zcode
source_product = zcode-cli-db
parser_version = zcode-model-usage-v1
event_granularity = request
identity = model_usage.id + native session
```

只读 `~/.zcode/cli/db/db.sqlite` 的 `model_usage` 表（默认路径 `~/.zcode/cli/db`，可显式配置数据库文件或目录）。schema 探测 fail-closed：`model_usage`/`session` 缺列或缺表时整库拒绝，不做猜测式解析。

Session 使用 `session_id`（join `session` 取 `path`，缺失回退 `directory` 作为 project path）；事件使用 `model_usage.id`，同一 `logical_request_id` 的多次 attempt 各自成事件，不折叠。`query_source`（main_turn/subagent/…）只保留在解析期 envelope，不参与 identity。

Token 按 `zcode_model_usage_v1` 归一化：来源 `input_tokens` 包含 cache read 与 cache creation，拆成非缓存 input、cache read、cache creation；`output_tokens` 包含 reasoning；total 优先 `provider_total_tokens`，缺失回退 `computed_total_tokens`，两者不一致或与分项不守恒的行拒绝。全零 usage 行跳过；error/cancelled 但有真实 token 的调用导入。

`model-io-*.jsonl`、`turn_usage`、`raw_usage_json`、`provider_metadata_json`、message 正文与 error 文本不读取也不落库；`provider_id` 列是路由 plan/account 而非模型厂商，事件 provider 由模型家族推导（glm→zai 等）。

## Parser contract 测试

每个 adapter 使用 synthetic fixture 覆盖：identity precedence、稳定 Session、同 native ID 下 model/token/path 变化不改变 event ID、subkey 拆分、非法 timestamp/token、accounting 守恒、二次 import 幂等，以及 append-only 文件分阶段补 metadata 时不产生第二个 event。fixture 不包含真实 Session、路径或客户数据。

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

## TRAE Work CN

```text
channel = trae-work-cn
source_product = trae-work-cn
parser_version = trae-work-cn-v1
event_granularity = message
identity = native chat_session_id + message_id
token_accounting_method = trae_work_cn_message_usage
accounting_profile = trae_work_cn_message_usage_v1
```

当前 adapter 是 opt-in、macOS-only 的 experimental direct runtime collector。`import` 自己发现已经运行的 `TRAE SOLO CN.app` 主进程，向 Electron main 发送 Node inspector 信号，再调用应用自带的 `ahaDebugger.startRemoteDebugging` 打开一个临时 loopback renderer CDP 端口。随后，它在 renderer 中使用现有 `IICubeAiChatConnectionService` 调用本地 `lite/list_chat_sessions` 与 `lite/get_messages`。这条链路依赖 TRAE 自带的 Electron/Node/CDP runtime contract，但不要求安装或执行外部 `node`、TRAE CLI 或 exporter；AgentLedger 不调用远程 TRAE API，也不读取/破解 SQLCipher `database.db`。TRAE 未运行、平台不支持、9229 被其它进程占用或 runtime contract 不匹配时，adapter fail closed。

这条链路已对 TRAE SOLO CN `0.1.50` 的本机 contract 验证，但不是厂商承诺的稳定外部 API。每轮采集设置 timeout、数量和 payload 上限；CDP/WebSocket endpoint 必须是预期端口上的数字 loopback IP。无论成功失败，collector 都调用 `ahaDebugger.stopRemoteDebugging`，关闭自己打开的 Node inspector，并检查临时端口已关闭。若用户原本已为同一 main process 打开 9229，AgentLedger 会复用但不会替用户关闭；若 9229 属于其它进程，则拒绝附加。

隐私边界位于 renderer 内。原始 `get_messages` response 不跨越 CDP；表达式只返回以下 allowlist：

```text
chat_session_id
message_id
created_at
session.mode
message.agent_type
message.model_smart_selection_meta.config_name
token_usage.prompt_tokens
token_usage.completion_tokens
token_usage.total_tokens
token_usage.cache_creation_input_tokens
token_usage.cache_read_input_tokens
token_usage.reasoning_tokens
token_usage.prompt_tokens_total
token_usage.completion_tokens_total
token_usage.last_turn_total_tokens
token_usage.max_tokens
```

`content`、`query`、title、user/account、project/worktree、credential、URL、原始 response 和其它 message 字段不会返回给 Go collector，也不会写库、warning 或 fingerprint。运行时投影再由 Go 的 `DisallowUnknownFields` schema 复验。测试 fixture 只能使用 synthetic identity/token；真实 Session/message ID 和 runtime response 不得进入 commit、PR、日志或公开文档。

只有 assistant message 上显式且可解析的 `token_usage` 才形成 event。`chat_session_id`、`message_id`、`created_at` 和 `total_tokens` 必须存在；timestamp 和 token 必须是安全整数，token 非负且 total 大于 0，`prompt_tokens + completion_tokens` 不得超过 `total_tokens`。缺少 usage 的 assistant message 只计入 `trae_work_cn_unmetered_assistant_messages` 聚合诊断，不按文本估算，也不把正常 import 标成 warning；identity、timestamp 或 usage 非法时才产生脱敏 warning。

`total_tokens` 是 canonical 权威总量，同时写入 source total。`prompt_tokens` 和 `completion_tokens` 保存为当前可确认的 input/output 分项；cache、reasoning、累计 total、last-turn total 和 `max_tokens` 只进入脱敏 fingerprint envelope，不进入 canonical bucket。原因是当前 contract 尚未证明 cache 是否已包含在 prompt、reasoning 是否已包含在 completion，以及带 `_total` 的字段是否为逐消息值。事件固定标记 `observability_level=partial`。`model_smart_selection_meta.config_name` 在 TRAE 自身 telemetry/parser 中被作为本轮实际 model 使用；只有通过长度与字符 allowlist 后才作为 direct event model，缺失或异常仍回退 `unknown`。由于 bucket 包含关系尚未完全证明，任何匹配到价格规则的金额仍是 provisional estimate，不是账单或严格下界。

默认配置 `enabled = false`、`experimental = true`、`paths = []`。空 paths 自动发现运行进程；非空 paths 只作为允许连接的 `.app` bundle allowlist，不是日志或 snapshot 目录。`doctor trae-work-cn` 只做平台与进程探测，真正的 runtime query 只发生在显式启用后的 `import`。

## Parser contract 测试

每个 adapter 使用 synthetic fixture 覆盖：identity precedence、稳定 Session、同 native ID 下 model/token/path 变化不改变 event ID、subkey 拆分、非法 timestamp/token、accounting 守恒、二次 import 幂等，以及 append-only 文件分阶段补 metadata 时不产生第二个 event。fixture 不包含真实 Session、路径或客户数据。

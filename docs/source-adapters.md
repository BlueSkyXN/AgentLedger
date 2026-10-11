# Source Adapters

每个 adapter 必须输出：稳定 Session、事件 identity、有效 timestamp、模型证据、token buckets、accounting method/profile、parser version 和 event granularity。Adapter 只选择权威 usage 来源，不把 request event 与 session summary 同时导入造成重复计数。

## 公共规则

- timestamp 必须有效，token 必须非负。
- 无 native Session ID 时只允许 source-root-relative Session path；不能用 absolute source file。
- 稳定 native event identity 不含 model、token、provider、cost 或设备。Claude 缺 request ID 的 message ID 可能被网关复用，此类消息额外以来源 timestamp 区分调用；不能用导入时间或文件修改时间代替。
- 一条 source record 拆多个 model/segment 时必须提供稳定 `identity_subkey`。
- 没有 native event ID 时优先使用 Session 内稳定的 `session_record(line + subkey)`；line 只在 source-root-relative Session 内解释，不与 absolute path 组合。
- 最后才用 `content_fallback`；它只保证完全相同内容重复 skip。
- 完整 source JSON 只在解析期存在；不保存正文或 raw usage。
- `raw_sha256` 是原始记录诊断 hash，不等于结构化 `content_sha256`。
- JSONL 来源（Claude、Codex、Gemini、Copilot、Cursor、WorkBuddy）逐行读取，单行上限 64 MiB。超过上限的行只跳过该行，不缓存整行；行号照常计数，因此依赖行号的 identity 不变；同一文件其余记录照常导入，并输出 `skipped N oversized line(s)` parse warning。此前超长行会让整个文件解析失败。

## Claude Code

```text
channel = claude
source_product = claude-code
provider = anthropic（来源模型正常化可修正）
parser_version = claude-v2
event_granularity = request
```

Session 优先原生 session ID，回退到 Claude source-root-relative project/session path。事件优先 message ID，再用 request ID；message 内多 segment 通过 subkey 分开。optional/null 字段用结构化 JSON 类型判断，不把字符串匹配当 schema。

Token 使用 `claude_usage_sum`，包括 input、output、cache creation、cache read。来源 cost 不落库。缺 request ID 时按 Session、message ID 与来源 timestamp 去重，并把 timestamp 写入 identity subkey；相同时间的重复写入保留较完整用量，sidechain 重放只在对应 Session 内匹配。旧 `claude-v1` 无 request ID 记录按原始 timestamp 升级 identity，避免再次导入产生重复；已被旧逻辑丢掉的调用需要从保留的源日志补回。

`usage.iterations` 中的 `advisor_message` 按自己的模型和 token 分项形成独立事件，使用 iteration 位置作为稳定子标识；普通 `message` iteration 不再次累计。嵌套 AgentProgress 包装同样支持，顾问缓存 TTL 分项独立读取。

缓存写入的 TTL 拆分读取 `usage.cache_creation.ephemeral_1h_input_tokens` / `ephemeral_5m_input_tokens`，写入 `cache_creation_1h_tokens`；只有 5m 字段时 1h 记为 0，`cache_creation` 缺失或为空对象时为 `NULL`（未知），1h 超过 `cache_creation_input_tokens` 时截断到总量。TTL 不能由订阅/API、主对话/subagent 推断：同一订阅主对话在超出套餐额度后会从 1h 降为 5m，各模型的 subagent 行为也不同，因此必须逐事件读取。

## Codex

```text
channel = codex
source_product = codex-cli
provider = openai（可由明确来源证据覆盖 unknown）
parser_version = codex-v2
event_granularity = request
```

Session 优先原生 session ID，回退到 `sessions`/`archived_sessions` root-relative path。事件优先明确 event/message/request ID；都缺失时使用 `session_record(line + subkey)`。后到的 `task_complete.turn_id` 只补关联 metadata，不重写已经可导入的 usage event identity，也不保存 timing。

`ledger` 对累计 `total_token_usage` 做 per-session reset-aware delta；`ccusage_compatible` 优先 `last_token_usage`，缺失时用累计 delta。source total 是权威 `total_tokens`；缓存 input 从 raw input 分离，reasoning 可能包含在 output 中。当累计 counter 局部回退导致已知分项不能完整解释 source total 时，保留 source total、把分项限制在 total 内并标记 `observability_level=partial`，不会把分项缺口伪造成某个 token bucket。该情况按 `codex_accounting_partial` diagnostic 汇总。replay matcher 继续 fail-closed。

默认发现 `sessions` 与同级 `archived_sessions`；同一 Codex home 下相同相对路径优先活动副本，不跨不同 home 按路径去重。Codex `local_thread_store_compression` 冷压缩产生的 `<name>.jsonl.zst` 同样被发现和流式解压（zstd，多帧兼容），session 兜底身份剥掉完整 `.jsonl.zst` 后缀，压缩前后同一 session 身份不变；同基名 `.jsonl` 与 `.jsonl.zst` 共存时按未压缩副本去重，损坏的压缩流按普通 parse error 隔离。

独立压缩调用通过 `token_usage_record.payload.response_id` 与 `compacted.payload.compaction_response_id` 配对，支持两种记录顺序；仅补计尚未被相邻普通累计观测覆盖的用量。不配对的请求记录不额外计入，配对 response ID 采用全局 request identity，复制到子会话不会重复统计。压缩记录不推进普通累计基线，也不覆盖当前主请求模型。

`thread_settings_applied.thread_settings.service_tier` 的 priority/fast、default/standard、flex、batch 被规范化后保存在 `accounting_profile` 的 `;service_tier=...` 后缀中，schema v4 不扩列。缺少该键保持原值，明确但未知值清空；价格匹配读取此后缀，未记录档位时不猜测。旧记录可补齐未知档位，两个已知档位冲突仍拒绝。

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

同时发现 OTel 与 session-state，再按有效用量协调；空 OTel 文件不会排除历史 session-state。OTel request events：

```text
source_product = copilot-otel
parser_version = copilot-otel-v1
event_granularity = request
identity = trace+span → response ID → interaction ID
```

每条非空 `session.shutdown.data.modelMetrics.<model>` 作为该运行分段的模型用量：

```text
source_product = copilot-session-state
parser_version = copilot-session-state-v2
event_granularity = session_model
identity = shutdown ID + model subkey
```

同一 Session/规范化模型的 shutdown 覆盖 start/resume 至 shutdown 区间内的 OTel；无开始记录时使用来源 `sessionStartTime`，也缺失时仅以 shutdown 为上界。其他 Session、模型或 shutdown 之后的 OTel 保留。已入库且本次源记录再次证明被覆盖的 OTel，与汇总写入在同一事务中替换；不是依据文件存在与否清空数据。多个 shutdown 仍按分段相加，不盲目做跨 resume 累计差分。

OTel 已有 trace/span identity 时，后补 response/interaction ID 只作为关联 metadata，不改变 event identity。OTel 保持 `input_includes_cache_read` 口径；session-state 使用 `input_includes_cache_read_write_output_includes_reasoning`：input 同时减去缓存读/写，reasoning 仅为 output 子项，total = raw input + output。旧版 session-state 行仅在字段符合旧算法且分项可解释时自动规范化；其他冲突仍拒绝。`requests.count`、request cost、premium/nano 指标和 duration 不落库，也不形成跨 Agent KPI。session-state 无 shutdown ID 时使用 `session_record(line + model)`；不能制造统一 shutdown ID，也不能把 absolute `path:line` 放入 identity。`raw_sha256` 对完整 source record 求值。

## Gemini CLI

```text
channel = gemini
source_product = gemini-cli
provider = google
parser_version = gemini-v1（usageMetadata）/ gemini-v2（聊天与 stats）
event_granularity = request / session_model
```

从 root/response 的 usage metadata 读取 prompt、cache、candidates、thoughts/reasoning、tool-input 和 total；按 `gemini_usage_v1` 验证包含关系。扩展原生 Session/event/message/request/response candidate；无稳定 event ID 时使用 `session_record(line + subkey)`。缺稳定 Session、timestamp，token 为负或 total 不守恒的记录拒绝。

同时支持聊天 JSON 的 `messages[].tokens`、JSONL metadata 加 `type=gemini` 消息，以及 `stats.models.<model>.tokens` / `result.stats`。会话与模型来自显式记录上下文，时间仅使用来源 message/created_at/startTime，不使用文件修改时间。重复消息按原生 identity 合并，同批同会话/模型存在有效明细时不再叠加 stats 汇总；stats 以会话、模型和来源时间区分。JSONL `$set` 中的 metadata/旧版 messages checkpoint 会解析；不从正文、工具显示文本或 `$patch` 推断 token。

## WorkBuddy

```text
channel = workbuddy
source_product = workbuddy
parser_version = workbuddy-v1
event_granularity = request
identity = root id + native session
```

`rawUsage.prompt_tokens` 拆成非缓存 input、cache read、cache creation；completion 包含 reasoning，`total_tokens` 使用来源总量并由 `workbuddy_raw_usage_v1` 验证。部分 Gemini 路由把 thinking 记在顶层 `rawUsage.reasoning_tokens`、不计入 completion，此时来源总量等于 prompt + completion + reasoning，reasoning 并入 output；`completion_tokens_details.reasoning_tokens` 超过 completion 时视为拆分未知（reasoning=0、`partial`），保留来源总量而不丢弃整条请求。`auto` 是路由状态，保存 `model_normalized=unknown`、fallback/policy-zero；credit、正文、URL、key 和完整 providerData 不落库。

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

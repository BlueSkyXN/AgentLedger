# Source Adapters

AgentLedger v2 通过 adapter 读取本机 agent 日志，解析出统一的 `ParsedRecord`，再写入 `usage_events`。

## 支持来源

| Agent | 默认路径 | 文件类型 | 主要 usage 字段 |
|---|---|---|---|
| Claude Code | `~/.config/claude/projects`, `~/.claude/projects` | JSONL | `message.usage`、`message.id`、`requestId`、`sessionId`、project path。 |
| Codex | `~/.codex/sessions` | JSONL | `usage`、`response.usage`、`payload.info.last_token_usage`、`payload.info.total_token_usage`。 |
| GitHub Copilot | `~/.copilot/otel`, `~/.copilot/session-state` | JSONL | 优先 OTel `gen_ai.usage.*` token telemetry；没有 OTel 文件时回退到 `session.shutdown.data.modelMetrics` session+model 汇总，其中 `requests.count` 可提供模型 API 请求次数。 |
| Gemini CLI | `~/.gemini` | JSON / JSONL | `usageMetadata`、`promptTokenCount`、`candidatesTokenCount`、`totalTokenCount`。 |
| WorkBuddy | `~/.workbuddy/projects` | JSONL | `providerData.usage`、`providerData.rawUsage`、根级 source event ID、session/request/project metadata。 |

## Source-specific accounting

### Claude Code

Claude Code 的 token 口径与 ccusage 对齐：单条 usage 的 `total_tokens` 按 `input_tokens + output_tokens + cache_creation_input_tokens + cache_read_input_tokens` 计算。

Claude 兼容 JSONL 当前统一作为 Claude Code 形态导入：`channel = claude`、`source_agent = claude`、`source_product = claude-code`。如果记录发生在 Open-Cowork 等项目目录下，项目路径写入 `project_path`，报表/API/Web 会派生项目标签，可通过 `--project open-cowork`、`report projects` 或 `by=project` 单独统计。AgentLedger 不会仅凭项目目录把客户端产品改写为 Open-Cowork，也不会解析 Open-Cowork app DB、缓存或运行日志作为 usage 来源。

Claude Code 日志会把同一次 assistant message 以多条流式行写出。AgentLedger 以 `message.id + requestId` 作为自然事件 key，并在同 key 重复时保留 token total 更大的记录；sidechain replay 记录按 ccusage 口径优先保留非 sidechain 版本。缺少 `message.id` 的旧格式记录才回退到 `uuid`。

`model == "<synthetic>"` 且 token total 为 0 的记录不会写入统计；`usage.speed == "fast"` 时，模型名追加 `-fast` 后缀，避免和 standard model 混在同一 model 维度。

Claude 完整 source object 只在解析期间用于结构化字段、`raw_sha256` 和罕见 `raw_hash` fingerprint fallback。SQLite 只保存规范化 token、model、cost、identity 和 accounting 字段，`raw_usage_json` 保持 `NULL`；不持久化 usage 子树、`content`、thinking、tool 参数、cwd、Git 或完整 message。

### Codex

Codex 默认只扫描 `~/.codex/sessions/**/*.jsonl`，与 ccusage 的默认范围保持一致；当配置路径写成 Codex home 根目录（例如 `~/.codex`）时，adapter 会优先收敛到其 `sessions` 子目录，避免把 history、临时文件或其他 JSONL 混入 usage。`~/.codex/archived_sessions` 不会自动导入。需要统计归档历史时，可以在 config 的 `agents.codex.paths` 中显式加入归档目录，或用符号链接把归档文件纳入扫描路径。

Codex 的 provider 归一为 `openai` 做合并统计，不按 session 级 `model_provider` 拆账；`model_provider` 可能反映本机路由或兼容网关，不作为事件身份或默认报表拆分维度。

Codex 模型状态按同一 session/thread 的 JSONL 物理顺序更新：`thread_settings_applied.payload.thread_settings.model` 和 `turn_context.model` 都只影响其后的 usage，后出现的声明覆盖先前状态；usage 自身明确携带的 model 仅作为该事件的 `direct_event` 证据。模型证据来源写入 `model_resolution = direct_event | thread_settings | turn_context | unknown`。父任务模型不会自动继承给 fork/subagent，也不会用 usage 之后才出现的模型倒灌前序事件。

导入 Codex 前，AgentLedger 会先形成 import 稳定文件集：mtime 位于 grace period 内的近期文件额外执行 100 ms size/mtime 检查，再基于同一文件集建立 fork replay plan 并保持逐文件流式解析。显式 fork 的 parent ID 优先读取 `forked_from_id`，其次读取 `source.subagent.thread_spawn.parent_thread_id`；parent 候选优先使用 active `sessions`，再使用显式配置的 `archived_sessions`。parent prefix 只取 timestamp 能证明不晚于 fork boundary 的 usage；缺失 fork timestamp，或 parent usage 缺失 timestamp 而无法证明位于 fork 前时，不会把完整 parent stream 当成 prefix。

Replay comparison stream 与最终 accounting policy 独立，也不直接比较累计 `total_token_usage` snapshot。累计值推进时优先使用非零 `last_token_usage`；`last` 缺失或四个主要 token 分量全零时，使用 reset-aware cumulative delta；累计未推进时不产生 comparison event。parent 与 child 都经过同一规范化，再按 input、clamped cached input、output、reasoning、derived-or-recorded total 五字段连续匹配。发布验证可以在私有冻结源上额外做 total-snapshot A/B，但它只是该时间点的证据，不属于长期产品契约；正式 matcher 保持 `last/delta`，便于继续和 ccusage 主路径交叉核对。

若 fork 时已证明 parent prefix 为空，第一条 child usage 直接保留，不启用 burst；非空 prefix 的首条即不匹配，或 parent/boundary 无法提供 exact prefix 时，只有 child 开头前两条“累计已推进且非零、能够进入 comparison stream”的 usage 时间差位于 `0..=1000 ms`，才把它们视为 rewritten replay burst；累计未推进的冗余重发行不单独构成 burst 证据。burst 建立后也只在每个相邻差值仍位于该区间时继续过滤。parent 已唯一解析、fork boundary 可证明、且 exact 与 opening burst 都不成立时，表示 child 从第一条开始就是自身 usage，全部保留；非 fork 文件永远不启用 burst 检测。这里有意比 ccusage 更保守：ccusage 的 burst detector 会把仅仅带有 last/total 字段的冗余重发行也作为开头证据，而 AgentLedger 在 parent 缺失时宁可 quarantine，也不据此跳过可能是真实的第一条 child usage。

Replay usage 会更新 child 的 cumulative accounting baseline，但不会生成 `ParsedRecord`、fingerprint 或 SQLite event，也不计入普通的 duplicate `Events skipped`。Replay 阶段的 model declaration 与 `task_complete` timing 不进入正式 attribution state；只有 matcher 遇到第一条真实 child usage 后，replay timestamp 单调且有效，并且 declaration timestamp 能证明比最后一条 replay usage 晚超过 1000 ms 时，最后一条同 key model declaration 才会生效。任一已跳过 replay usage 的 timestamp 缺失或倒序都会使这段 attribution boundary 不可信，全部 replay-local model buffer 随即丢弃。若 matcher 证明 child 根本没有 replay，则开头缓冲的 model declaration 按原有物理顺序正常生效。无法唯一解析 parent、文件在 preparation 后发生变化，或缺少足够 replay 证据时，只隔离该 fork child 并把 import 标为 `completed_with_warnings`；全局 replay plan 构建失败时，本轮 Codex adapter fail closed，其他 adapter 继续。

这里的“隔离/quarantine”只表示本轮 import 在内存中跳过该 child 的全部 usage；AgentLedger 不移动、不重命名、不删除源 JSONL，也不创建持久 quarantine 目录。配置里的 cleanup/quarantine 仍是未实现占位，不能用于恢复 replay child。

Codex token event 本身没有 model、且此前也没有可用的 `thread_settings` / `turn_context` 时，模型统一记录为 `unknown`，同时设置 `model_is_fallback = true` 和 `model_resolution = unknown`。不要用具体 GPT 型号充当未知模型占位符。默认 pricing profile 对精确 `unknown` 使用本地零值政策，但不把它算作真实价格覆盖；重新导入时，同一来源行中由旧 parser 硬编码生成的 `gpt-5` fallback 会收敛为当前可证明的上下文模型或 `unknown`，但新的 fallback 不会覆盖历史 explicit model。

Codex 的 `total_token_usage` 是 session 级 cumulative counter，是最权威的用量来源。两个观测事实决定了计量口径：(1) 每次真实调用后，日志通常会**冗余重发**一条内容相同的 `token_count`（累计 total 不变）；(2) `last_token_usage` 只记录最后一次 API 调用，当同一区间内发生多次调用时会**漏记**中间的量。

默认 `duplicate_policy = "ledger"` 据此采用最准口径：以 `total_token_usage` 做 per-session **望远镜 delta**——累计不变时 delta 为 0、自动跳过冗余重发；累计回落（compact 压缩上下文导致 counter reset）时整段计入、不丢量；`last_token_usage` 缺失（无累计）的旧记录才回退为单条直接计入。本机 1151 个 session 实测，该口径与「逐 session 累计值金标准」一致到 99.96%。

作为对照，`duplicate_policy = "ccusage_compatible"` 对齐当前 ccusage 在正常单 session Codex JSONL 上的单次 usage 选择：`total_token_usage` 累计推进时优先使用 `last_token_usage`，`last` 缺失时使用逐字段 saturating cumulative delta，累计未推进时忽略该行。它适合在独立数据库或重建后做数值交叉核对，但仍会继承 `last_token_usage` 对同一区间多次调用只保留最后一次的可观测性限制；默认 `ledger` 口径仍以累计 delta 为权威。该 profile 与 replay comparison 共用同一 skip plan，二者只在最终写入的 accounting token 数上可能不同。多 session 混写同一文件、缺失或零值 `total_tokens` 等非标准格式边界不承诺与 ccusage 逐 bit 相同，AgentLedger 仍优先保持 per-session baseline 和非负 token 不变量。

Codex 日志里的 `input_tokens` 包含 cached input。入库时 AgentLedger 会拆成 `input_tokens = raw_input_tokens - cached_input_tokens` 和 `cache_read_tokens = cached_input_tokens`，使表内 token 分项和 Claude/ccusage 报表的非缓存输入口径一致；`raw_input_tokens` 保存源日志原始 input，`source_total_tokens` 仍保留源日志 raw cumulative total。

默认口径下 `usage_events.total_tokens` 是望远镜 delta 还原出的单次增量，`usage_events.source_total_tokens` 保留源日志里的 raw cumulative total，仅用于排查和交叉验证，不应对该列做 `SUM()` 作为用量报表。

Codex 的 `task_complete.duration_ms`、`task_complete.time_to_first_token_ms` 和 `turn_id` 会按同一 session 内紧邻的上一条 usage 记录落为 turn 级 timing。这个值包含 Codex turn 的端到端耗时边界，不等同于严格的单次模型 API latency。`session_path_id` 保存相对 `sessions` 的路径 ID，例如 `2026/05/27/rollout-...`，用于和 ccusage 的 session 粒度对齐。

`agent-ledger doctor codex` 会扫描 configured paths，输出 raw `token_count` 覆盖、`task_complete` timing 覆盖、fork/parent/exact/rewritten/quarantine replay diagnostics、默认（准确）口径与 `ccusage_compatible` 口径的事件数/token 差异，以及模型分布。两种口径共享同一 replay plan 和文件身份 snapshot，并逐文件核对 quarantine、`file_changed` 与 exact/rewritten replay event decisions；snapshot drift 或 outcome 不一致会让 comparison 明确返回 inconclusive。Replay diagnostic 的文件/child 数使用 `count`；`tokens` 表示 ledger 口径下被 replay filter 避免写入的 token impact，不用于 policy parity，因为两个 accounting policy 可对同一 replay event 计算出不同 token 数。

Codex 完整 source object 同样只在解析期间用于累计 delta、结构化字段、`raw_sha256` 和 fingerprint。`total_token_usage` / `last_token_usage` 的计算结果写入 token 分项、`source_total_tokens`、`raw_input_tokens`、`token_accounting_method` 和 `accounting_profile`，但 usage map 本身不落库；`raw_usage_json` 保持 `NULL`。`task_complete` 继续只更新独立 timing/turn 列，完整重放依赖源 JSONL。

### GitHub Copilot

GitHub Copilot 优先读取本地 OTel JSONL telemetry：`~/.copilot/otel` 或 `COPILOT_OTEL_FILE_EXPORTER_PATH`。OTel 事件按 `gen_ai.usage.*` 字段生成请求级记录，`source_product = copilot-otel`。Copilot 的 input 口径会把 cache read 包含在内，AgentLedger 入库时写 `raw_input_tokens = source input`，并把 `input_tokens` 归一化为扣除 `cache_read_tokens` 后的非缓存输入。只要发现 OTel usage 文件，默认不会再导入 `session-state` 的 shutdown 汇总，避免请求级数据和 session 汇总双计数。

没有 OTel 文件时，Copilot adapter 会读取 `~/.copilot/session-state/*/events.jsonl` 里的每条非空 `session.shutdown` 事件。该事件的 `data.modelMetrics.<model>.usage` 提供 `inputTokens`、`outputTokens`、`cacheReadTokens`、`cacheWriteTokens`、`reasoningTokens`，一条 `usage_events` 记录表示一个 shutdown/run-resume segment 下的一个 model，而不是整段 session 的最后累计值。字段口径为：`source_product = copilot-session-state`、`observability_level = session_summary`、`token_accounting_method = copilot_session_model_metrics`、`accounting_profile = input_includes_cache_read`。未 shutdown 的活跃 session 不会产生这类汇总记录。

同一 `modelMetrics.<model>` 下的 `requests.count` 是该模型的 API 请求次数，写入 `usage_events.request_count`。该 Copilot CLI session-state 字段目前属于 experimental schema：缺失、非十进制整数、负数、小数、指数、字符串、布尔值或 int64 overflow 时保持 `request_count = NULL`，不从 token、event 或 session 数推断；源日志明确提供 `0` 时才写入 `0`。token 分项全为零但 `requests.count` 为合法非负整数（含显式 `0`）时，仍生成零-token event，作为 source-backed request fact，不算 synthetic usage。

本期只为 `session.shutdown.modelMetrics.<model>.requests.count` 增加 request count。OTel 的 `request_count` 映射，以及 OTel 与 session-state 同时存在时的 request-count 协调，均不在本期范围内；现有“发现 OTel usage 文件即优先 OTel、避免 token 双计数”的来源选择规则不因此改变。

`requests.cost`、`totalPremiumRequests`、`totalNanoAiu` 等 Copilot 本地指标不会写入 `recorded_cost_usd` 或 `raw_usage_json`；这些值不是可直接和 Claude/Codex 拉通的 USD 成本。`assistant.message.outputTokens`、`subagent.completed.totalTokens` 和 compaction token 字段只作为解析期校验线索，当前不会作为主 usage 导入，避免 partial envelope 和 `session.shutdown` 汇总重复计数。

### WorkBuddy

WorkBuddy 只扫描 `~/.workbuddy/projects/**/*.jsonl`，包括主会话和 subagent 记录；不会读取应用日志、trace、audit、file history、SQLite、blobs 或工具输出作为 usage 来源。adapter 只接受带根级 `id`、`timestamp`、`sessionId`、`cwd` 以及完整 `providerData.usage` / `providerData.rawUsage` 的单次调用记录，并以根级事件 ID 去重。`messageId` 只用于消息关联，`conversationRequestId` 作为 turn/run 分组，不参与去重。

WorkBuddy source input 包含 cache。入库时 `raw_input_tokens` 保存 `prompt_tokens`，`input_tokens` 扣除 `prompt_tokens_details.cached_tokens` 与明确的 cache write，`cache_read_tokens` / `cache_creation_tokens` 单独保存；`completion_tokens` 作为包含 reasoning 的 output，reasoning detail 仅作分析明细。`source_total_tokens` 与 `total_tokens` 使用来源明确的 `rawUsage.total_tokens`，不从 overlapping 明细重新推导。缺少 cache-write 明细的记录标记为 partial observability。

`model_raw` 保存 `providerData.model`，规范化仅使用 WorkBuddy exact aliases：`deepseek-v4-pro-202606 -> deepseek-v4-pro`、`k3 -> kimi-k3`、`kimi-k3-2 -> kimi-k3`。内置路由使用 `provider = workbuddy`，`custom-local:*` 使用 `provider = custom`；两者的 `channel`、`source_agent`、`source_product` 均为 `workbuddy`。`auto` 是路由选择状态而不是 resolved model ID，因此保留 `model_raw = auto` 供诊断，同时写入 `model_normalized = unknown`、`model_resolution = unknown` 和 `policy_zero`。

WorkBuddy 的 `rawUsage.credit` 不写入 `recorded_cost_usd` 或 `raw_usage_json`。estimated cost 只使用规范化模型、token 分项和 pricing profile；SQLite 不保存 usage envelope、正文、工具参数、`cwd`、URL、API key 或完整 `providerData`。`project_path` 单独保存在本地事实表中，并继续服从默认 export path redaction。

## Parsed fields

Adapter 会尽量提供：

- `Agent`: 写入 `channel`，例如 `claude`、`codex`、`copilot`、`gemini`、`workbuddy`。
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
- `FingerprintJSON`（仅在解析和 fingerprint 计算期间存在，不落库）

新写入事件会同时写入 `channel` 和 `source_agent`，并保持二者一致。`source_product` 用于区分 `claude-code`、`codex-cli`、`copilot-otel`、`copilot-session-state` 等具体来源形态；项目或工作目录维度保存在 `project_path`。

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

# Architecture

## 目标

AgentLedger v3 将本机 AI Coding Agent Session 日志转换为可重复导入、可跨设备合并的 usage 统计事实。重点是日期、Session、来源、provider、模型、project 和 token 分项，不追求完整审计或 request 性能分析。

## 数据流

```text
Claude/Codex/Copilot/Gemini/WorkBuddy logs
                 │
                 ▼
    Adapter discovery + parser + accounting
                 │ ParsedRecord
                 ▼
       fingerprint identity v2/content hash
                 │ UsageEvent
                 ▼
      SQLite schema v3 transactional reconcile
          │                     │
          ▼                     ▼
  CLI report/export/merge   read-only /api/v2
                                  │
                                  ▼
                              React Web
```

## 模块边界

- `internal/adapters`：发现和解析本机日志，选择每个产品的权威 usage 粒度。
- `internal/fingerprint`：生成稳定 Session/event identity 和结构化 content hash。
- `internal/model`：跨 package 的 `UsageEvent`。
- `internal/db`：schema v3、校验、reconcile、merge、统计与 IANA time bucket SQLite function。
- `internal/pricing`：只读 profile、provider/channel/model matching、即时估算与 coverage。
- `internal/analytics`：API/Web 聚合、Session summary、分页和 filters。
- `internal/report`：CLI 文本/JSON 报表。
- `internal/control`：loopback-only、GET-only `/api/v2` 和静态面板。
- `web`：只读 React/Vite 分析面板。

## Identity

Session identity 优先使用来源原生 Session ID，否则使用相对 Agent source root 的稳定 Session path。无法得到稳定 Session 时拒绝记录，不创建统一 `no-session`。

Event identity 优先级为 native event/message/request、session turn、session record，最后才是 content fallback。稳定 identity 不包含 model、token、timestamp、provider、设备、absolute path 或 import time，因此 parser 补模型和 token 元数据不会制造第二个 event ID。

`content_sha256` 包含 channel/source/provider、模型证据、timestamp、token、accounting、observability 和 granularity；排除 parser version、本机 locator、project、raw SHA、设备和导入时间。它用于区分 exact duplicate、兼容补充与冲突。

## Reconcile 与 merge

普通 import 和 merge 调用同一个纯决策规则。exact duplicate 零写入；兼容补充只做 missing-fill 或 fallback 到直接模型证据的升级；token/Session/time/直接模型/已知 accounting 冲突拒绝。

merge 在 destination transaction 中先读取并模拟全部 incoming event。存在任一冲突时 rollback；无冲突时按 preflight action 执行 insert/update/skip。不复制 incoming `import_runs`。

## 查询与时间

Session 不持久化，由 `usage_events` 实时聚合。primary model 是筛选窗口内 total token 最大的模型，并列按 model ID 字典序。first/last date 是筛选后的最早/最晚事件日期，不表示工作时长。

SQLite 只保存 UTC epoch milliseconds。`agentledger_time_bucket(timestamp_ms, timezone, bucket)` 由 Go 注册，逐事件使用 IANA timezone 历史 offset，避免固定当前 offset 导致 DST 错日。

## Pricing

金额永不写库。查询按当前 profile 使用 provider、channel、model、日期和 token bucket 即时估算。未匹配模型为 unpriced/null；内置 `unknown` 规则是 `policy_zero`，不冒充官方免费。配置 profile 无效只让 pricing unavailable，不阻断用量查询；显式 CLI override 无效直接失败。

## 明确不包含

设备表、source checkpoint、offset/tail reader、usage observation/conflict ledger、持久化 Session、正文/raw usage、金额、request count、duration、TTFT、TPS、Slow report、远程同步或 telemetry。

# AgentLedger

AgentLedger v3 是本地优先的 AI Coding Agent Session usage 统计器。它从 Claude Code、Codex、GitHub Copilot、Gemini CLI 和 WorkBuddy 的本机日志中提取日期、Session、通道、来源形态、provider、模型、project 与 token 分项，写入 SQLite，并通过 CLI、只读 HTTP API 和 React 面板查询。

## 产品边界

- 保留 `Adapter → UsageEvent → SQLite → CLI/API/Web` 主链。
- 只保存结构化 usage facts；不保存对话正文、完整源对象、设备信息或金额。
- 只统计事件日期以及日/周/月分桶；不提供 request duration、TTFT、TPS、Slow 或 request-count KPI。
- 多设备通过 schema v3 `.aldb` 合并；不保存或展示设备维度。
- import 可以重复扫描日志，事件级 identity 保证重复导入不增加总量；本版不做文件 offset/checkpoint。
- estimated cost 始终按当前 pricing profile 即时计算，数据库不保存金额。

## 快速开始

要求 Go 版本满足 `go.mod`，并具备 CGO 与 C toolchain。Web 需要 Node.js `^20.19.0` 或 `>=22.12.0`。

```bash
go build -o bin/agent-ledger .

# 新建 v3 配置和数据库
./bin/agent-ledger init

# 检查数据源并导入
./bin/agent-ledger doctor
./bin/agent-ledger import
./bin/agent-ledger verify
./bin/agent-ledger status

# 核心报表
./bin/agent-ledger report daily
./bin/agent-ledger report sessions
./bin/agent-ledger report sources
./bin/agent-ledger report models --since 2026-08-01 --cost estimated

# 本机只读面板/API
./bin/agent-ledger serve
```

v3 不打开、迁移或 merge v2 数据库。已有 v2 数据必须先做 exact backup，再在独立 data dir 中从原始 Session 日志重建 v3 candidate。不要在未验收 candidate 前对正式库运行 `init --reset`。

## CLI

保留的命令：

```text
init
import
status
doctor
verify
export
merge
vacuum
serve
report daily|weekly|monthly|models|channels|sources|providers|projects|sessions
```

所有 report 支持：

```text
--since --until --channel --source --provider --model
--session --project --cost estimated|none --pricing --json
```

`--cost` 默认 `estimated`。显式 `--pricing` 文件无效时命令失败；配置中的默认 pricing 文件无效时，用量查询仍成功，但 cost 为 `null`，并返回 `pricing.status=unavailable`。

## Schema v3 与去重

数据库只包含：

- `meta`：`schema_version=3`、`identity_version=2`、`created_at`。
- `import_runs`：本次 import 的 inserted/updated/skipped/rejected 和脱敏 warning。
- `usage_events`：事件 identity、Session、来源、模型、时间、token/accounting 与本机 source locator。

稳定 Session key 不包含 absolute path 或设备：

```text
hash("session:v1", source_product, native_session_id 或 source-root-relative session_path_id)
```

稳定 event ID 不包含 model、token、timestamp、provider、设备或 import time：

```text
hash("event:v2", source_product, identity_scope,
     session_key(仅 session scope), identity_kind,
     native_event_id, identity_subkey)
```

同 event ID 的处理固定为：

| 情况 | 结果 |
|---|---|
| event ID 不存在 | `inserted` |
| content hash 相同 | `skipped`，零写入 |
| token/session/time 相同，仅补空字段或 `unknown/fallback → direct` 模型证据 | `updated` |
| token、Session、timestamp、直接模型或已知 accounting 冲突 | `rejected`，不覆盖原行 |

普通 import 与 `.aldb` merge 共用同一 reconcile 规则。merge 会先全量 preflight；任一冲突会使整次 merge 零写入。

## 配置

```toml
[database]
path = "/path/to/agent-ledger.db"

[privacy]
redact_paths_on_export = true

[import]
gracing_minutes = 15

[reports]
timezone = "Asia/Shanghai"
pricing_path = ""

[agents.codex]
enabled = true
paths = ["~/.codex/sessions"]
duplicate_policy = "ledger"
```

`reports.timezone` 使用 IANA timezone 对每条历史事件分桶，DST 日期不会按“当前 offset”回算。

## API v2

`serve` 只允许 loopback，API 只读：

```text
GET /api/v2/health
GET /api/v2/status
GET /api/v2/config
GET /api/v2/analytics/summary
GET /api/v2/analytics/timeseries?bucket=daily|weekly|monthly
GET /api/v2/analytics/breakdown?by=channel|source_product|provider|model|project|session
GET /api/v2/filter-options
GET /api/v2/sessions?limit=50&offset=0
GET /api/v2/events?limit=200&offset=0
GET /api/v2/import-runs
```

`/api/v1/*` 不提供兼容层并返回 404。`/sessions` 和 `/events` 返回 `{items, limit, offset, total}`。

## Web

```bash
cd web
npm ci
npm run lint
npm run build
cd ..
./bin/agent-ledger serve
```

面板保留 Overview、Trends、Models、Channels、Sources、Projects、Sessions、Imports、Settings。Sessions 是主要分析页；estimated cost 明确标注为按当前 profile 即时估算。

## 隐私与导出

- 默认 redacted export 清空 `project_path`、`source_file` 和 import warning。
- `event_id`、`session_key`、`content_sha256` 与 token totals 在 redacted roundtrip 中保持不变。
- `.db`、`.aldb`、Session ID、模型、项目、路径和汇总结果仍属于本机私有数据。
- AgentLedger 不联网、不做 telemetry、不修改源 Session 日志。

详细说明见 [docs/README.md](docs/README.md)。

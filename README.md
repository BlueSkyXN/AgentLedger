# AgentLedger

**面向 AI Coding Agent 的本地 usage 统计分析器。**

AgentLedger 把 Claude Code、Codex、GitHub Copilot、Gemini CLI 等本机日志解析为统一 usage event，写入本地 SQLite，并提供按渠道、模型、provider、时间和 session 的筛选、聚合与慢请求分析。

## 当前定位

v2 已从“多表账本 / 审计系统”收敛为“本地 usage analytics”。核心目标是简单、可解释、可查询：

- 导入本机 agent 日志到 SQLite。
- 用稳定 fingerprint 做确定性去重。
- 重复事件使用 upsert，保留更完整记录。
- 围绕 `channel`、`provider`、`model`、`time`、`session` 做筛选和统计。
- 统计 token、耗时、TTFT、输出 TPS。
- timing 只在日志明确提供时记录，缺失保持 `NULL`。
- `recorded_cost_usd` 只表示来源日志明确给出的 USD 成本；CLI report 可选用标准 JSON pricing profile 做只读 estimated cost。

v2 不迁移旧 v1 本地数据库。打开旧 schema 会报错，并提示运行：

```bash
agent-ledger init --reset
```

`--reset` 会删除当前配置指向的本地数据库及 WAL/SHM 文件，然后初始化空的 v2 数据库。需要保留旧数据时，请先手动备份 `.db` / `.aldb`。

## 功能特性

- **多 agent 导入**：Claude Code、Codex、GitHub Copilot、Gemini CLI。
- **三表 SQLite schema**：只保留 `meta`、`import_runs`、`usage_events`。
- **扁平事实表**：`usage_events` 直接保存 channel、provider、model、time、session、token、timing、source line 和 raw usage envelope。
- **确定性去重 + 完整度 upsert**：重复事件优先保留有 timing、有 recorded cost、有 model、token 总量更高的记录。
- **常用报表**：`daily`、`weekly`、`monthly`、`models`、`channels`、`sessions`、`slow`。
- **只读 Web 面板**：Overview、趋势、渠道 / provider、模型、session、慢请求、导入 / 设置。
- **本地优先**：除非你明确复制、导出或截图，数据只保留在本机。

## 安装

### 从源码构建

前置条件：

- 本机 Go 版本需要与 `go.mod` 兼容。
- 本项目使用 `github.com/mattn/go-sqlite3`，本地构建通常需要 `CGO_ENABLED=1` 和可用的 C toolchain，例如 macOS Xcode Command Line Tools 或 Linux `gcc`。

```bash
git clone https://github.com/BlueSkyXN/AgentLedger.git
cd AgentLedger
mkdir -p bin
go build -o bin/agent-ledger .
./bin/agent-ledger --help
```

下文命令默认使用 `agent-ledger` 表示已经把二进制放入 `PATH`。源码开发时也可以直接运行：

```bash
go run . --help
go test ./...
go build ./...
```

前端面板构建：

```bash
cd web
npm install
npm run build
```

## 快速开始

```bash
# 初始化配置和 v2 数据库
agent-ledger init

# 如果已有旧 v1 数据库，直接重建本地 v2 空库
agent-ledger init --reset

# 从已启用的本机 agent 导入用量数据
agent-ledger import

# 查看数据库统计信息
agent-ledger status

# 常用报表
agent-ledger report daily
agent-ledger report weekly --channel claude
agent-ledger report monthly --model claude-sonnet-4
agent-ledger report models --json
agent-ledger report channels --since 2026-05-01
agent-ledger report sessions --provider anthropic
agent-ledger report slow --sort output_tps --limit 50

# 导出 / 合并 v2 SQLite 数据库
agent-ledger export --output usage.aldb
agent-ledger merge usage.aldb

# 维护命令
agent-ledger verify
agent-ledger vacuum
agent-ledger doctor
agent-ledger doctor codex

# 本地只读 Web 面板
agent-ledger serve
```

## 命令

| 命令 | 说明 |
|---|---|
| `init` | 创建配置和 v2 数据库；`--reset` 可重建本地空库。 |
| `import` | 从已配置的本机 agent 日志路径导入 usage events。 |
| `export` | 将当前 SQLite 数据库复制为可移植的 `.aldb` 文件。 |
| `merge [file.aldb]` | 合并另一个 schema v2 AgentLedger SQLite 导出文件。 |
| `report daily` | 按日聚合用量。 |
| `report weekly` | 按周聚合用量。 |
| `report monthly` | 按月聚合用量。 |
| `report models` | 按模型拆分 token / timing。 |
| `report channels` | 按 agent 来源渠道拆分用量。 |
| `report sessions` | 按 session 拆分用量。 |
| `report slow` | 慢请求列表，支持按低输出 TPS、高 TTFT 或高总耗时排序。 |
| `status` | 显示数据库统计信息。 |
| `doctor` | 显示配置、数据库路径和 agent 日志发现诊断；`doctor codex` 输出 Codex token/timing/口径覆盖诊断。 |
| `verify` | 运行 SQLite `PRAGMA integrity_check`。 |
| `vacuum` | 运行 SQLite `VACUUM`。 |
| `serve` | 启动本机只读 Web 面板和 `/api/v1/*` JSON API。 |
| `completion` | 通过 Cobra 生成 shell completion 脚本。 |

## 报表

所有 report 子命令统一支持：

```bash
--since YYYY-MM-DD
--until YYYY-MM-DD
--channel string
--provider string
--model string
--session string
--cost recorded|estimated|both|none
--pricing path/to/pricing.json
--json
```

`report daily`、`report weekly`、`report monthly` 额外支持：

```bash
--by channel|model|provider|session
```

`report slow` 额外支持：

```bash
--sort output_tps|ttft_ms|total_duration_ms
--limit 50
```

示例：

```bash
agent-ledger report daily --since 2026-05-01
agent-ledger report daily --by model --channel codex
agent-ledger report weekly --channel codex
agent-ledger report monthly --provider anthropic
agent-ledger report models --model gpt-5.5 --json
agent-ledger report models --cost estimated
agent-ledger report channels --cost both --pricing ./pricing/custom.json
agent-ledger report channels
agent-ledger report sessions --until 2026-05-31
agent-ledger report slow --sort ttft_ms --limit 20
```

报表会输出事件数、token 分项、平均总耗时、平均 TTFT、平均输出 TPS 和成本列。默认 `--cost recorded` 只显示来源明确记录的 `Recorded Cost(USD)`；`--cost estimated` 或 `--cost both` 会按 `pricing/pricing.v1.json` 或 `--pricing` 指定的 JSON profile 做只读估算，并输出 pricing coverage / confidence。estimated cost 不会写回 SQLite。没有 explicit timing 的事件不会参与 timing 平均值，相关字段保持空值。

## 本地 Web 面板

`serve` 会启动一个只读本地面板，实时从当前 SQLite 数据库查询聚合结果。不提供浏览器触发 `import`、`merge`、`vacuum` 或修改配置的能力。

```bash
agent-ledger serve
# 默认监听地址：127.0.0.1:54217
```

默认只允许 loopback 地址。可用参数：

```bash
# agent-ledger serve (默认监听 127.0.0.1:54217)
agent-ledger serve --addr 127.0.0.1:54217 --static-dir web/dist
```

面板 API 挂在 `/api/v1/*`，前端不直接读取 SQLite。`web/dist` 存在时会托管 React 面板；如果尚未构建，会显示内置 placeholder，并提示运行：

```bash
cd web
npm install
npm run build
```

面板不会返回 `raw_usage_json`。聚合数据、session id、模型名、项目路径和数据库路径仍属于本机私有使用数据，不应作为公开截图或附件传播。

## 只读 API

主要接口：

| Method | Path | 说明 |
|---|---|---|
| `GET` | `/api/v1/health` | 版本、数据库路径、数据库大小、面板资源模式。 |
| `GET` | `/api/v1/status` | schema version、事件数、导入次数、token 和 recorded cost 汇总。 |
| `GET` | `/api/v1/config` | 脱敏配置快照。 |
| `GET` | `/api/v1/analytics/summary` | 总览 KPI，支持统一 filters。 |
| `GET` | `/api/v1/analytics/timeseries?bucket=daily\|weekly\|monthly` | 时间趋势；可加 `by=channel\|model\|provider\|session` 返回时间 + 维度拆分。 |
| `GET` | `/api/v1/analytics/breakdown?by=channel\|model\|provider\|session` | 维度排行。 |
| `GET` | `/api/v1/analytics/slow?sort=output_tps\|ttft_ms\|total_duration_ms&limit=50` | 慢请求列表。 |
| `GET` | `/api/v1/filter-options` | 当前库中存在的 channel、provider、model、session 选项。 |
| `GET` | `/api/v1/events` | 最近 usage events，不返回 raw JSON。 |
| `GET` | `/api/v1/import-runs` | 最近 import runs。 |

统一 filters：

```text
since=YYYY-MM-DD
until=YYYY-MM-DD
channel=claude
provider=anthropic
model=claude-sonnet-4
session=<session-id>
```

## 支持的 Agent

| Agent | 默认路径 | 解析格式 | 说明 |
|---|---|---|---|
| Claude Code | `~/.config/claude/projects`, `~/.claude/projects` | JSONL | 读取带有 `message.usage` 的 assistant 消息；旧配置写 `~/.claude` 时会自动展开到 `projects`。 |
| Codex | `~/.codex/sessions` | JSONL | 读取 token count 记录；默认用 `total_token_usage` 的 per-session 累计 delta 还原真实增量，`last_token_usage` 仅用于旧记录或 `ccusage_compatible` 对照；配置写 `~/.codex` 时会自动收敛到 `sessions`。 |
| GitHub Copilot | `~/.copilot/otel`, `~/.copilot/session-state` | JSONL | 优先读取 OTel `gen_ai.usage.*`；没有 OTel 文件时回退到每条非空 `session.shutdown.data.modelMetrics` 的 segment+model 汇总。Copilot input 会拆成 `raw_input_tokens`、非缓存 `input_tokens` 和 `cache_read_tokens`。 |
| Gemini CLI | `~/.gemini` | JSON / JSONL | 读取 `usageMetadata`。 |

`channel` 固定表示 agent 来源，例如 `claude`、`codex`、`copilot`、`gemini`。

## 配置

当配置文件不存在时，`agent-ledger init` 和 `config.Load()` 会创建它。下面是默认配置的语义示例；实际生成的 `[database].path` 会基于运行时数据目录解析：

```toml
[database]
path = "local/data/agent-ledger.db"

[privacy]
mode = "envelope"
redact_paths_on_export = true

[import]
gracing_minutes = 15
single_thread = false

[cleanup]
default_mode = "quarantine"
older_than_days = 30
purge_after_days = 90

[reports]
timezone = "Local"
currency = "USD"

[agents.claude]
enabled = true
paths = ["~/.config/claude/projects", "~/.claude/projects"]

[agents.codex]
enabled = true
paths = ["~/.codex/sessions"]
duplicate_policy = "ledger"

[agents.gemini]
enabled = true
paths = ["~/.gemini"]

[agents.copilot]
enabled = true
paths = ["~/.copilot/otel", "~/.copilot/session-state"]
```

数据目录选择顺序：

1. 如果设置了 `AGENT_LEDGER_DATA_DIR`，使用该目录。
2. 如果当前工作目录或可执行文件所在目录的上级能找到 `go.mod`，使用 `<repo-root>/local/data`。
3. 否则使用 `~/.local/share/agent-ledger`。

重要路径：

- Config: `<data-dir>/config.toml`
- Database: 默认 `<data-dir>/agent-ledger.db`，也可通过 `[database].path` 修改

当前 `[reports].timezone` 已用于 daily / weekly / monthly 报表分桶和 `--since` / `--until` 日期过滤；支持 `Local`、`UTC`、固定偏移如 `+08:00`，以及 Go 可加载的 IANA 时区如 `Asia/Shanghai`。`[privacy].redact_paths_on_export = true` 时，`export` 会移除导出副本里的 `project_path`、`source_file` 和 `raw_usage_json`。`[cleanup]` 和 `[reports].currency` 仍是配置占位；现有命令尚未实现 cleanup 或 currency 转换。report 的 estimated cost 由 `pricing/pricing.v1.json` 或 `--pricing` 指定文件驱动，不使用 `[reports].currency` 做换算。

## 文档

- [文档索引](docs/README.md)
- [快速开始](docs/quickstart.md)
- [数据模型](docs/data-model.md)
- [CLI Reference](docs/cli-reference.md)

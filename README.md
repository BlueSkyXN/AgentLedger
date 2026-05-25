# AgentLedger

**面向 AI Coding Agent 的本地优先用量账本。**

AgentLedger 是一个本地优先的 AI Coding Agent 用量账本。它把 Claude Code、Codex、Gemini CLI、Qwen 等本机日志解析为统一事件，写入 SQLite，并提供跨设备合并、确定性去重、统计报表和基础维护命令。

## 当前状态

AgentLedger 当前是 Go CLI 项目，核心能力已落在 `cmd/` 和 `internal/`：

- 导入本机 agent 日志到 SQLite
- 使用事件指纹做确定性去重
- 导出 / 合并 `.aldb` SQLite 数据库
- 按日、周、月、模型、渠道、设备、session 输出报表
- 提供 `status`、`doctor`、`verify`、`vacuum` 维护命令

尚未实现的设计项包括：原始日志 cleanup/quarantine 命令、价格表驱动的成本估算、加密 raw archive、完整 source file 增量状态追踪。

## 功能特性

- **多 agent 导入**：Claude Code、Codex、Gemini CLI、Qwen
- **SQLite 存储**：以事件为粒度记录 token、model、session、timestamp、device 和原始 usage metadata 字段
- **确定性去重**：基于 message id、session-token 元组、规范化 raw JSON hash，以及兜底的 file-line hash
- **跨设备合并**：导出 / 导入可移植的 `.aldb` 文件，并且只写入未见过的事件
- **统计报表**：按日、周、月、模型、渠道、设备、session 查看用量
- **本地优先**：除非你明确复制或导出数据，否则数据只保留在本机

## 安装

### 从源码构建

前置条件：

- 本机 Go 版本需要与 `go.mod` 兼容。
- 本项目使用 `github.com/mattn/go-sqlite3`，本地构建通常需要 `CGO_ENABLED=1` 和可用的 C toolchain（例如 macOS Xcode Command Line Tools 或 Linux `gcc`）。

```bash
git clone https://github.com/BlueSkyXN/AgentLedger.git
cd AgentLedger
mkdir -p bin
go build -o bin/agent-ledger .
./bin/agent-ledger --help
```

下文命令默认使用 `agent-ledger` 表示已经把二进制放入 `PATH`。如果只是按上面的源码构建步骤运行，请把命令替换为 `./bin/agent-ledger ...`。

本地开发时，也可以不安装，直接运行以下命令：

```bash
go run . --help
go test ./...
go build ./...
```

## 快速开始

```bash
# 初始化配置、数据库和本机 device id
agent-ledger init

# 从已启用的本机 agent 导入用量数据
agent-ledger import

# 查看数据库统计信息
agent-ledger status

# 查看报表
agent-ledger report daily
agent-ledger report monthly --by agent
agent-ledger report models --json

# 导出用于跨设备合并
agent-ledger export --output my-device.aldb

# 合并另一台设备的导出文件
agent-ledger merge other-device.aldb

# 维护命令
agent-ledger verify
agent-ledger vacuum
agent-ledger doctor

# 本地只读 Web 面板
agent-ledger serve
```

## 命令

| 命令 | 说明 |
|---------|-------------|
| `init` | 如果配置 / 数据库不存在，则创建它们，并注册当前设备 |
| `import` | 从已配置的本机 agent 日志路径导入用量事件 |
| `export` | 将当前 SQLite 数据库复制为可移植的 `.aldb` 文件 |
| `merge [file.aldb]` | 将另一个 AgentLedger SQLite 导出文件合并到本地数据库 |
| `report daily` | 按日拆分用量 |
| `report weekly` | 按周汇总用量 |
| `report monthly` | 按月汇总，可选择按 `agent`、`model` 或 `provider` 分组 |
| `report models` | 按模型拆分 token / 事件用量 |
| `report channels` | 按来源渠道拆分用量 |
| `report devices` | 按设备拆分用量 |
| `report sessions` | session 列表；当前实现按成本排序 |
| `status` | 显示数据库统计信息 |
| `doctor` | 显示配置 / 数据库路径，以及发现的 source file 数量 |
| `verify` | 运行 SQLite `PRAGMA integrity_check` |
| `vacuum` | 运行 SQLite `VACUUM` |
| `serve` | 启动本机只读 Web 面板和 `/api/v1/*` JSON API |
| `completion` | 通过 Cobra 生成 shell completion 脚本 |

Cobra 也会提供生成的 `completion` 命令。

## 本地 Web 面板

`serve` 会启动一个只读本地面板，实时从当前 SQLite 数据库查询聚合结果。第一版只读展示，不提供浏览器触发 `import`、`merge`、`vacuum` 或修改配置的能力。

```bash
agent-ledger serve
# http://127.0.0.1:8765
```

默认只允许 loopback 地址。可用参数：

```bash
agent-ledger serve --addr 127.0.0.1:8765 --static-dir web/dist
```

面板 API 挂在 `/api/v1/*`，前端不直接读取 SQLite。`web/dist` 存在时会托管 React 面板；如果尚未构建，会显示内置 placeholder，并提示运行：

```bash
cd web
npm install
npm run build
```

面板不会返回 `raw_usage_json` 或 `raw_meta_json`，但聚合数据、session id、模型名和数据库路径仍属于本机私有使用数据，不应作为公开截图或附件传播。

## 报表

所有报表子命令都支持：

- `--since YYYY-MM-DD`
- `--until YYYY-MM-DD`
- `--json`

`report monthly` 还支持 `--by agent|model|provider`。

```bash
agent-ledger report daily --since 2026-05-01
agent-ledger report weekly
agent-ledger report monthly --by model
agent-ledger report models --json
agent-ledger report channels
agent-ledger report devices
agent-ledger report sessions --until 2026-05-31
```

## 支持的 Agent

| Agent | 默认路径 | 解析格式 | 说明 |
|-------|--------------|---------------|-------|
| Claude Code | `~/.claude` | JSONL | 读取带有 `usage` 或 `message.usage` 的 assistant 消息 |
| Codex | `~/.codex` | JSONL | 读取 token count 记录；存在 `last_token_usage` 时优先使用它 |
| Gemini CLI | `~/.gemini` | JSON / JSONL | 读取 `usageMetadata` |
| Qwen | `~/.qwen` | JSONL | 读取 `usage` |

可在 AgentLedger 配置文件中修改已配置路径。配置文件路径由数据目录决定，见下方“配置”。

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
timezone = "UTC"
currency = "USD"

[agents.claude]
enabled = true
paths = ["~/.claude"]

[agents.codex]
enabled = true
paths = ["~/.codex"]

[agents.gemini]
enabled = true
paths = ["~/.gemini"]

[agents.qwen]
enabled = true
paths = ["~/.qwen"]
```

数据目录选择顺序：

1. 如果设置了 `AGENT_LEDGER_DATA_DIR`，使用该目录。
2. 如果当前工作目录或可执行文件所在目录的上级能找到 `go.mod`，使用 `<repo-root>/local/data`。
3. 否则使用 `~/.local/share/agent-ledger`。

重要路径：

- Config: `<data-dir>/config.toml`
- Database: 默认 `<data-dir>/agent-ledger.db`，也可通过 `[database].path` 修改
- Device ID: `<data-dir>/device_id`

当前 `[cleanup]`、`[reports].timezone`、`[reports].currency` 和 `[privacy].redact_paths_on_export` 仍是 schema / config 占位配置；现有命令尚未实现 cleanup、timezone 转换、currency 转换或 export redaction。

## 跨设备工作流

```bash
# 设备 A
agent-ledger import
agent-ledger export --output device-a.aldb

# 设备 B
agent-ledger merge device-a.aldb
agent-ledger report monthly --by agent
```

合并时会先校验传入路径存在，并且是常规 SQLite 数据库文件；随后 attach 该数据库，并按主键插入本地未见过的 `usage_events`。已有事件会被跳过。

## 文档

- [文档索引](docs/README.md)
- [快速开始](docs/quickstart.md)
- [CLI 参考](docs/cli-reference.md)
- [配置](docs/configuration.md)
- [Source Adapter](docs/source-adapters.md)
- [数据模型](docs/data-model.md)
- [报表与合并](docs/reports-and-merge.md)
- [隐私与运维](docs/privacy-and-operations.md)
- [开发](docs/development.md)
- [路线图](docs/roadmap.md)

## 隐私说明

AgentLedger 是本地优先工具，但它会把从 source log 中解析出的 usage envelope 和原始 usage JSON 存入 SQLite。导出 `.aldb` 会复制该数据库。除非你已经审阅并脱敏，否则应把导出文件视为私有用量数据。

## 许可证

GPL-3.0

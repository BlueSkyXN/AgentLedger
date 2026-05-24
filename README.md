# AgentLedger

**A local-first usage ledger for AI coding agents.**

AgentLedger 是一个本地优先的 AI Coding Agent 用量账本。它把 Claude Code、Codex、Gemini CLI、Qwen 等本机日志解析为统一事件，写入 SQLite，并提供跨设备合并、确定性去重、统计报表和基础维护命令。

## 当前状态

AgentLedger 当前是 Go CLI 项目，核心能力已落在 `cmd/` 和 `internal/`：

- 导入本机 agent 日志到 SQLite
- 使用事件指纹做确定性去重
- 导出 / 合并 `.aldb` SQLite 数据库
- 按日、周、月、模型、渠道、设备、session 输出报表
- 提供 `status`、`doctor`、`verify`、`vacuum` 维护命令

尚未实现的设计项包括：原始日志 cleanup/quarantine 命令、价格表驱动的成本估算、加密 raw archive、完整 source file 增量状态追踪。

## Features

- **Multi-agent import**: Claude Code, Codex, Gemini CLI, Qwen
- **SQLite storage**: event-level usage records with token, model, session, timestamp, device and raw usage metadata fields
- **Deterministic deduplication**: message id, session-token tuple, canonical raw JSON hash, and fallback file-line hash
- **Cross-device merge**: export/import portable `.aldb` files and insert only unseen events
- **Reports**: daily, weekly, monthly, models, channels, devices, sessions
- **Local-first**: data stays on the local machine unless you explicitly copy/export it

## Installation

### Build from source

Prerequisite: Go version compatible with `go.mod`.

```bash
git clone https://github.com/BlueSkyXN/AgentLedger.git
cd AgentLedger
mkdir -p bin
go build -o bin/agent-ledger .
./bin/agent-ledger --help
```

For local development, the same commands can be run without installing:

```bash
go run . --help
go test ./...
go build ./...
```

## Quick Start

```bash
# Initialize config, database, and local device id
agent-ledger init

# Import usage data from enabled local agents
agent-ledger import

# Check database statistics
agent-ledger status

# View reports
agent-ledger report daily
agent-ledger report monthly --by agent
agent-ledger report models --json

# Export for cross-device merge
agent-ledger export --output my-device.aldb

# Merge another device export
agent-ledger merge other-device.aldb

# Maintenance
agent-ledger verify
agent-ledger vacuum
agent-ledger doctor
```

## Commands

| Command | Description |
|---------|-------------|
| `init` | Create config/database if missing and register the current device |
| `import` | Import usage events from configured local agent log paths |
| `export` | Copy the current SQLite database to a portable `.aldb` file |
| `merge [file.aldb]` | Merge another AgentLedger SQLite export into the local database |
| `report daily` | Daily usage breakdown |
| `report weekly` | Weekly usage summary |
| `report monthly` | Monthly summary, optionally grouped by `agent`, `model`, or `provider` |
| `report models` | Model-level token/event breakdown |
| `report channels` | Source-channel breakdown |
| `report devices` | Device-level breakdown |
| `report sessions` | Session listing, ordered by cost in the current implementation |
| `status` | Show database statistics |
| `doctor` | Show config/database paths and discovered source file counts |
| `verify` | Run SQLite `PRAGMA integrity_check` |
| `vacuum` | Run SQLite `VACUUM` |
| `completion` | Generate shell completion scripts from Cobra |

Cobra also provides the generated `completion` command.

## Reports

All report subcommands accept:

- `--since YYYY-MM-DD`
- `--until YYYY-MM-DD`
- `--json`

`report monthly` also uses `--by agent|model|provider`.

```bash
agent-ledger report daily --since 2026-05-01
agent-ledger report weekly
agent-ledger report monthly --by model
agent-ledger report models --json
agent-ledger report channels
agent-ledger report devices
agent-ledger report sessions --until 2026-05-31
```

## Supported Agents

| Agent | Default Path | Parsed Format | Notes |
|-------|--------------|---------------|-------|
| Claude Code | `~/.claude` | JSONL | Reads assistant messages with `usage` or `message.usage` |
| Codex | `~/.codex` | JSONL | Reads token count records and prefers `last_token_usage` when present |
| Gemini CLI | `~/.gemini` | JSON / JSONL | Reads `usageMetadata` |
| Qwen | `~/.qwen` | JSONL | Reads `usage` |

Configured paths can be changed in `~/.config/agent-ledger/config.toml`.

## Configuration

`agent-ledger init` and `config.Load()` create the config file when it does not exist:

```toml
[database]
path = "~/.local/share/agent-ledger/agent-ledger.db"

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

Important paths:

- Config: `~/.config/agent-ledger/config.toml`
- Database: `~/.local/share/agent-ledger/agent-ledger.db` by default
- Device ID: `~/.local/share/agent-ledger/device_id`

The `[cleanup]`, `[reports].timezone`, `[reports].currency`, and `[privacy].redact_paths_on_export` settings are schema/config placeholders today; the current commands do not yet implement cleanup, timezone conversion, currency conversion, or export redaction.

## Cross-Device Workflow

```bash
# Device A
agent-ledger import
agent-ledger export --output device-a.aldb

# Device B
agent-ledger merge device-a.aldb
agent-ledger report monthly --by agent
```

Merge validates that the incoming path exists, is a regular SQLite database file, then attaches it and inserts unseen `usage_events` by primary key. Existing events are skipped.

## Documentation

- [Docs Index](docs/README.md)
- [Quickstart](docs/quickstart.md)
- [CLI Reference](docs/cli-reference.md)
- [Configuration](docs/configuration.md)
- [Source Adapters](docs/source-adapters.md)
- [Data Model](docs/data-model.md)
- [Reports and Merge](docs/reports-and-merge.md)
- [Privacy and Operations](docs/privacy-and-operations.md)
- [Development](docs/development.md)
- [Roadmap](docs/roadmap.md)

## Privacy Notes

AgentLedger is local-first, but it stores parsed usage envelopes and raw usage JSON from source logs in SQLite. Exporting `.aldb` copies that database. Treat exported files as private usage data unless you have reviewed and redacted them.

## License

GPL-3.0

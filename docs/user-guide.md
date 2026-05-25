# User Guide

AgentLedger 是一个本地优先的 AI Coding Agent 用量账本。典型使用流程是：初始化本地配置和数据库，导入本机 agent 日志，查看统计报表，并在多设备之间导出 / 合并 `.aldb` 数据库。

## 安装与验证

```bash
git clone https://github.com/BlueSkyXN/AgentLedger.git
cd AgentLedger
mkdir -p bin
go build -o bin/agent-ledger .
./bin/agent-ledger --help
```

后续示例使用 `./bin/agent-ledger`，对应源码构建产物。如果你已经把二进制安装到 `PATH`，可以直接使用 `agent-ledger`。

开发环境也可以直接运行：

```bash
go run . --help
go test ./...
go build ./...
```

## 初始化

```bash
./bin/agent-ledger init
```

初始化会创建或复用：

- `<repo-root>/local/data/config.toml`
- `<repo-root>/local/data/agent-ledger.db`
- `<repo-root>/local/data/device_id`

以上路径是在源码仓库内运行时的默认位置。实际数据目录优先使用 `AGENT_LEDGER_DATA_DIR`；如果没有设置且运行环境找不到项目根目录，则使用 `~/.local/share/agent-ledger`。

`device_id` 使用 ULID 并持久化，后续运行会复用同一个设备标识。

## 配置数据源

默认启用四类 agent：

```toml
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

如果某个 agent 不需要导入，可以把对应 `enabled` 改成 `false`。如果日志在非默认目录，可以修改 `paths`。

## 导入

```bash
./bin/agent-ledger import
```

导入流程会：

- 按启用的数据源扫描 JSON/JSONL 文件。
- 跳过最近修改时间仍在 grace period 内的文件，默认 `15` 分钟。
- 解析 usage envelope，生成统一 `usage_events`。
- 计算事件指纹，并用 `INSERT OR IGNORE` 跳过重复事件。
- 写入一条 `import_runs` 记录。

当前实现不会移动、删除或改写原始 agent 日志。

## 查看状态与诊断

```bash
./bin/agent-ledger status
./bin/agent-ledger doctor
./bin/agent-ledger verify
./bin/agent-ledger vacuum
```

- `status` 显示数据库路径、事件数、设备数、导入次数、token 总数和成本字段汇总。
- `doctor` 显示配置路径、数据库是否存在，以及各 agent 可发现的源文件数量。
- `verify` 执行 SQLite `PRAGMA integrity_check`。
- `vacuum` 执行 SQLite `VACUUM` 回收空间。

## 报表

```bash
./bin/agent-ledger report daily
./bin/agent-ledger report weekly
./bin/agent-ledger report monthly
./bin/agent-ledger report monthly --by agent
./bin/agent-ledger report monthly --by model
./bin/agent-ledger report monthly --by provider
./bin/agent-ledger report models
./bin/agent-ledger report channels
./bin/agent-ledger report devices
./bin/agent-ledger report sessions
```

常用过滤和输出参数：

```bash
./bin/agent-ledger report daily --since 2026-05-01 --until 2026-05-31
./bin/agent-ledger report models --json
```

所有报表子命令都暴露 `--since`、`--until`、`--json`。当前只有 `report monthly` 使用 `--by` 分组。

## 跨设备合并

在设备 A：

```bash
./bin/agent-ledger import
./bin/agent-ledger export --output device-a.aldb
```

把 `device-a.aldb` 复制到设备 B 后：

```bash
./bin/agent-ledger merge device-a.aldb
./bin/agent-ledger report monthly --by agent
```

合并会验证输入文件是 SQLite 数据库，然后 attach 到当前数据库，通过 `usage_events.event_fingerprint` 主键跳过重复事件。

## 隐私边界

AgentLedger 不依赖云端服务，但 `.aldb` 是 SQLite 数据库副本，可能包含本机使用痕迹、模型名、session id、token 数和 raw usage JSON。对外发送 `.aldb` 前应按私有数据处理。

当前配置中的 `redact_paths_on_export` 是预留字段，导出命令只是复制数据库文件，并不会执行脱敏。

## 当前限制

- 成本字段当前通常为 `0`，尚未实现模型价格表估算。
- cleanup/quarantine 尚未实现为 CLI 命令。
- schema 中有 `sources`、`source_files`、`raw_records` 等表，但当前导入主路径只写 `usage_events` 和 `import_runs`。
- `[reports].timezone` 和 `[reports].currency` 当前未参与报表计算。

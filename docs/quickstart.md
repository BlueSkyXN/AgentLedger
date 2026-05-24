# Quickstart

本文档只覆盖当前 Go CLI 已实现能力。

## 1. 构建

Prerequisite: 使用 `go.mod` 声明的 Go 版本或兼容版本。

```bash
git clone https://github.com/BlueSkyXN/AgentLedger.git
cd AgentLedger
mkdir -p bin
go build -o bin/agent-ledger .
./bin/agent-ledger --help
```

开发时也可以直接运行：

```bash
go run . --help
go test ./...
go build ./...
```

## 2. 初始化

```bash
agent-ledger init
```

初始化会创建或复用本机配置、SQLite 数据库和持久化设备标识。该命令会写入用户主目录下的 AgentLedger 配置与数据目录；如果只是阅读文档或验证 help 输出，不需要运行它。

## 3. 导入本机日志

```bash
agent-ledger import
```

导入会扫描启用的 Claude Code、Codex、Gemini CLI、Qwen 路径，把可解析的 usage envelope 写入 `usage_events`。当前实现不会移动、删除或改写原始 agent 日志。

默认会跳过最近仍可能被写入的文件，grace period 来自 `[import].gracing_minutes`，默认 `15` 分钟。

## 4. 查看状态和报表

```bash
agent-ledger status
agent-ledger report daily
agent-ledger report weekly
agent-ledger report monthly --by agent
agent-ledger report models --json
agent-ledger report sessions
```

所有 report 子命令都暴露 `--since`、`--until`、`--json`。当前只有 `report monthly` 使用 `--by agent|model|provider` 改变分组。

## 5. 跨设备合并

在设备 A：

```bash
agent-ledger import
agent-ledger export --output device-a.aldb
```

把 `device-a.aldb` 复制到设备 B 后：

```bash
agent-ledger merge device-a.aldb
agent-ledger report monthly --by agent
```

`.aldb` 是 SQLite 数据库副本，包含本地 usage 数据。不要把真实导出文件当作普通公开附件传播。

## 6. 维护命令

```bash
agent-ledger doctor
agent-ledger verify
agent-ledger vacuum
```

- `doctor` 显示配置路径、数据库是否存在，以及各 adapter 可发现的源文件数量。
- `verify` 执行 SQLite `PRAGMA integrity_check`。
- `vacuum` 执行 SQLite `VACUUM` 回收空间。

## 当前没有的命令

当前 CLI 没有 `cleanup` 或 `restore` 命令。旧设计稿里的 quarantine、purge、restore 流程还没有落到 Go CLI，不要按这些命令操作真实日志。

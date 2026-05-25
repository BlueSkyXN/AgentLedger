# Quickstart

本文档只覆盖当前 Go CLI 已实现能力。

## 1. 构建

Prerequisite:

- 使用 `go.mod` 声明的 Go 版本或兼容版本。
- 本项目依赖 `github.com/mattn/go-sqlite3`，本地构建通常需要 `CGO_ENABLED=1` 和可用的 C toolchain（例如 macOS Xcode Command Line Tools 或 Linux `gcc`）。

```bash
git clone https://github.com/BlueSkyXN/AgentLedger.git
cd AgentLedger
mkdir -p bin
go build -o bin/agent-ledger .
./bin/agent-ledger --help
```

本文后续示例使用 `./bin/agent-ledger`，对应上面的源码构建产物。如果你已经把二进制安装到 `PATH`，可以直接使用 `agent-ledger`。

开发时也可以直接运行：

```bash
go run . --help
go test ./...
go build ./...
```

## 2. 初始化

```bash
./bin/agent-ledger init
```

初始化会创建或复用本机配置、SQLite 数据库和持久化设备标识。数据目录选择顺序是：`AGENT_LEDGER_DATA_DIR`、源码仓库内的 `<repo-root>/local/data`、最后才是 `~/.local/share/agent-ledger`；如果只是阅读文档或验证 help 输出，不需要运行它。

## 3. 导入本机日志

```bash
./bin/agent-ledger import
```

导入会扫描启用的 Claude Code、Codex、Gemini CLI、Qwen 路径，把可解析的 usage envelope 写入 `usage_events`。当前实现不会移动、删除或改写原始 agent 日志。

默认会跳过最近仍可能被写入的文件，grace period 来自 `[import].gracing_minutes`，默认 `15` 分钟。

## 4. 查看状态和报表

```bash
./bin/agent-ledger status
./bin/agent-ledger report daily
./bin/agent-ledger report weekly
./bin/agent-ledger report monthly --by agent
./bin/agent-ledger report models --json
./bin/agent-ledger report sessions
```

所有 report 子命令都暴露 `--since`、`--until`、`--json`。当前只有 `report monthly` 使用 `--by agent|model|provider` 改变分组。

## 5. 跨设备合并

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

`.aldb` 是 SQLite 数据库副本，包含本地 usage 数据。不要把真实导出文件当作普通公开附件传播。

## 6. 维护命令

```bash
./bin/agent-ledger doctor
./bin/agent-ledger verify
./bin/agent-ledger vacuum
```

- `doctor` 显示配置路径、数据库是否存在，以及各 adapter 可发现的源文件数量。
- `verify` 执行 SQLite `PRAGMA integrity_check`。
- `vacuum` 执行 SQLite `VACUUM` 回收空间。

## 7. 本地 Web 面板

```bash
./bin/agent-ledger serve
```

打开 `http://127.0.0.1:8765` 查看只读面板。面板实时读取当前 SQLite 聚合结果，但不会从浏览器触发导入、合并、清理或配置修改。

如果看到 placeholder，说明 React 面板还没有构建：

```bash
cd web
npm install
npm run build
cd ..
./bin/agent-ledger serve
```

## 当前没有的命令

当前 CLI 没有 `cleanup` 或 `restore` 命令。旧设计稿里的 quarantine、purge、restore 流程还没有落到 Go CLI，不要按这些命令操作真实日志。

# Quickstart

## 1. Build

```bash
mkdir -p bin
go build -o bin/agent-ledger .
./bin/agent-ledger --help
```

## 2. Init

```bash
./bin/agent-ledger init
```

如果当前数据目录里已有旧 v1 数据库，v2 会拒绝打开并提示 reset：

```bash
./bin/agent-ledger init --reset
```

`--reset` 会删除当前配置指向的本地数据库及 WAL/SHM 文件，然后创建空的 schema v2 数据库。需要保留旧数据时先手动备份。

## 3. Import

```bash
./bin/agent-ledger import
```

默认读取配置中启用的 agent 路径：

- Claude Code: `~/.config/claude/projects`, `~/.claude/projects`
- Codex: `~/.codex`
- Gemini CLI: `~/.gemini`
- Qwen: `~/.qwen`

## 4. Status

```bash
./bin/agent-ledger status
```

输出 schema version、事件数、导入次数、token 汇总和 recorded cost 汇总。

## 5. Reports

```bash
./bin/agent-ledger report daily
./bin/agent-ledger report weekly --channel claude
./bin/agent-ledger report monthly --provider anthropic
./bin/agent-ledger report models --json
./bin/agent-ledger report channels --since 2026-05-01
./bin/agent-ledger report sessions --model gpt-5.5
./bin/agent-ledger report slow --sort output_tps --limit 50
```

所有 report 子命令支持：

```bash
--since YYYY-MM-DD
--until YYYY-MM-DD
--channel string
--provider string
--model string
--session string
--json
```

## 6. Web panel

```bash
cd web
npm install
npm run build
cd ..
./bin/agent-ledger serve
```

打开：

```text
http://127.0.0.1:54217
```

Web 面板只读，不会从浏览器触发 import、merge、vacuum 或配置修改。

## 7. Export / merge

```bash
./bin/agent-ledger export --output usage.aldb
./bin/agent-ledger merge usage.aldb
```

`merge` 只接受 schema v2 AgentLedger SQLite 数据库，并只合并本地未见过的 `usage_events`。

# Quickstart

## 1. 构建

```bash
go test ./...
go build -o bin/agent-ledger .
```

## 2. 初始化新 v3 数据目录

```bash
export AGENT_LEDGER_DATA_DIR="$PWD/local/experiments/usage-v3"
./bin/agent-ledger init
```

如果指定目录里已有 schema v2 数据库，v3 会拒绝打开。不要直接 reset 正式库；先做 exact backup，再换独立 data dir clean rebuild。

## 3. 配置与导入

编辑 `$AGENT_LEDGER_DATA_DIR/config.toml` 中的 `agents.*.paths`、Codex `duplicate_policy` 与 `reports.timezone`，然后：

```bash
./bin/agent-ledger doctor
./bin/agent-ledger import
./bin/agent-ledger verify
./bin/agent-ledger status
```

第二次无源变化导入应满足：

```text
Events added:    0
Events updated:  0
Events rejected: 0
Events skipped:  第一次有效事件数
```

## 4. 报表

```bash
./bin/agent-ledger report daily
./bin/agent-ledger report weekly --since 2026-07-01
./bin/agent-ledger report models --provider openai
./bin/agent-ledger report sources
./bin/agent-ledger report sessions --cost estimated --json
```

## 5. 导出与合并

```bash
./bin/agent-ledger export -o device-a.aldb
./bin/agent-ledger merge device-a.aldb
```

merge 只接受 schema v3 / identity v2，先做全量 preflight。冲突时整次 merge 零写入。

## 6. Web/API

```bash
cd web
npm ci
npm run lint
npm run build
cd ..
./bin/agent-ledger serve
```

浏览器打开 `http://127.0.0.1:54217/`，健康检查：

```bash
curl -fsS http://127.0.0.1:54217/api/v2/health
```

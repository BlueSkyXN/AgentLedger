# Development

本文档面向 AgentLedger 的本地开发和维护。

## 环境

项目是 Go module：

```text
module github.com/BlueSkyXN/AgentLedger
```

主要依赖：

- `github.com/spf13/cobra`: CLI command framework
- `github.com/BurntSushi/toml`: config file encoding/decoding
- `github.com/mattn/go-sqlite3`: SQLite driver
- `github.com/oklog/ulid/v2`: import run id

由于 SQLite driver 使用 `github.com/mattn/go-sqlite3`，本地构建通常需要 `CGO_ENABLED=1` 和可用的 C toolchain。

## 常用命令

```bash
go test ./...
go build ./...
go vet ./...
gofmt -l .
go run . --help
go run . report monthly --help
go run . serve
```

构建本地二进制：

```bash
mkdir -p bin
go build -o bin/agent-ledger .
./bin/agent-ledger --help
```

## 仓库结构

```text
cmd/                  CLI commands
internal/adapters/    source log discovery and parsers
internal/analytics/   read-only SQL aggregations for API and panel
internal/config/      default config and TOML load/save
internal/control/     local HTTP API and web panel static hosting
internal/db/          SQLite connection, schema, insert, merge, stats
internal/fingerprint/ event fingerprinting
internal/model/       domain structs
internal/report/      report SQL and output formatting
docs/                 public documentation
local/                private notes, experiments, generated reports
```

`local/` 默认不应提交。它可以保存设计草稿、扫描结果、私有实验数据库和本机审计记录。

## 验证门槛

文档或小改动至少运行：

```bash
gofmt -l .
go test ./...
go vet ./...
```

涉及 CLI surface 时再运行：

```bash
go run . --help
go run . <command> --help
```

涉及 Web 面板时再运行：

Node.js 版本必须满足 `^20.19.0` 或 `>=22.12.0`。

```bash
cd web
npm ci
npm audit --audit-level=moderate
npm run lint
npm run build
cd ..
go run . serve
```

`serve` 第一版是只读面板。不要在没有明确产品决策前新增浏览器触发 `import`、`merge`、`vacuum` 或配置修改的 API。

涉及导入、合并或报表 SQL 时，建议使用临时数据库和样例日志，不要直接覆盖真实用户数据库。优先通过临时 `AGENT_LEDGER_DATA_DIR` 隔离 AgentLedger 数据目录，也可以配合测试 fixture 隔离源日志。

## 实现注意事项

- 不要猜测外部 agent 的日志格式；新增 adapter 前先保留真实样例或测试 fixture。
- 新增 report 参数时必须做 allowlist 校验，避免把未验证用户输入拼入 SQL。
- `merge` 涉及 SQLite `ATTACH DATABASE`，必须继续保留路径校验和 SQLite header 校验。
- v2 schema 只保留 `meta`、`import_runs`、`usage_events`；不要重新引入 source/observation/conflict 表，除非先完成新的 schema 设计和回归测试。
- 成本估算不要硬编码成不可追踪常量；应记录 pricing source/version，并明确价格更新时间。

## 发布前检查

发布前建议确认：

```bash
go test ./...
go build ./...
go run . --help
go run . doctor
go run . verify
```

如果使用真实本机日志做 smoke test，报告里只能公开汇总指标，不要公开 session id、raw usage JSON、私有路径或导出的 `.aldb`。

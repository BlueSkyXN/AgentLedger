# CLI Reference

## `init`

```bash
agent-ledger init
agent-ledger init --reset
```

创建配置和 schema v3 数据库。`--reset` 删除当前配置指向的 DB/WAL/SHM，属于破坏性操作；对正式库执行前必须有 exact backup 和明确授权。

## `import`

扫描启用 adapter 的稳定日志文件，解析 usage event，并按 identity v2 reconcile。输出固定计数：files、added、updated、skipped、rejected。存在 parse/reconcile warning 时 run 状态为 `completed_with_warnings`，命令仍处理其余有效记录。

import 会重新读取稳定文件；本版不保存 file offset/checkpoint。重复正确性来自 event identity，不来自“未变化文件跳过”。

## `status`

只读输出 DB path、schema version、event 数、import run 数和 total token，不输出金额、request count 或 timing。

## `doctor`

只读检查当前 config、启用 adapter 和 discovery 路径。输出可能含本机私有路径，公开分享前应脱敏。

## `verify`

通过普通只读 SQLite 连接执行 `PRAGMA integrity_check`。它验证物理 SQLite 完整性，不等于 schema v3 业务查询已验收；还需运行 `status`、report 和 API smoke。

## `report`

```text
report daily
report weekly
report monthly
report models
report channels
report sources
report providers
report projects
report sessions
```

所有 report 统一支持：

| Flag | 含义 |
|---|---|
| `--since` | 起始日期 `YYYY-MM-DD` 或 RFC3339。 |
| `--until` | 结束日期；日期值按所在 timezone 的下一日边界排他处理。 |
| `--channel` | channel filter。 |
| `--source` | source product filter。 |
| `--provider` | provider filter。 |
| `--model` | normalized model ID filter。 |
| `--session` | session key 或 native session ID。 |
| `--project` | 派生 project label。 |
| `--cost` | `estimated`（默认）或 `none`。 |
| `--pricing` | 显式 pricing profile override；无效则命令失败。 |
| `--json` | JSON 输出。 |

不存在 `report slow`、recorded/both cost、slow sort/limit。

## `export`

```bash
agent-ledger export -o device-a.aldb
```

输出可移植 schema v3 SQLite。默认 redacted export 清空 `project_path`、`source_file` 和 import warning，但保持 event/session/content identity 与 token totals。

## `merge`

```bash
agent-ledger merge device-a.aldb
```

只接受 schema v3 / identity v2。merge 不复制 incoming `import_runs`；先全量 preflight，任一冲突整事务零写入。成功输出 inserted/updated/skipped，重复 merge 应全部 skipped。

## `vacuum`

对已经存在且完整的 v3 数据库执行 SQLite `VACUUM`。它会重写 DB 文件；执行前停止 writer 和 serve，并保留可恢复备份。

## `serve`

```bash
agent-ledger serve --addr 127.0.0.1:54217 --static-dir web/dist
```

只接受 loopback host，提供 GET-only `/api/v2/*` 和静态面板。浏览器/API 不触发 import、merge、vacuum、reset 或配置写入。

## 已删除命令/接口

```text
compact-raw
report slow
/api/v1/*
/api/v2/analytics/slow
```

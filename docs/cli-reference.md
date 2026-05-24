# CLI Reference

本文档按当前 Cobra help 和 Go 代码整理。命令面以 `cmd/` 为准。

## Root

```bash
agent-ledger [command]
```

当前命令：

| Command | 当前状态 | 说明 |
|---|---|---|
| `init` | 已实现 | 创建或复用配置和数据库，并注册当前设备。 |
| `import` | 已实现 | 从启用的本机 agent 日志导入 usage events。 |
| `export` | 已实现 | 把当前 SQLite 数据库复制为 `.aldb` 文件。 |
| `merge [file.aldb]` | 已实现 | 合并另一个 AgentLedger SQLite export。 |
| `report` | 已实现 | 报表命令组。 |
| `status` | 已实现 | 输出数据库统计。 |
| `doctor` | 已实现 | 输出配置、数据库和源文件发现诊断。 |
| `verify` | 已实现 | 运行 SQLite integrity check。 |
| `vacuum` | 已实现 | 运行 SQLite vacuum。 |
| `completion` | 已实现 | Cobra 自动生成的 shell completion 命令。 |

当前没有 `cleanup`、`restore`、`pricing` 或 `workspace` 命令。

## `init`

```bash
agent-ledger init
```

行为：

- 加载或创建 TOML 配置。
- 打开并初始化 SQLite schema。
- 创建或复用本机持久化设备标识。
- upsert 当前设备记录。

## `import`

```bash
agent-ledger import
```

当前没有 CLI flags。导入行为由配置文件控制。

关键行为：

- 遍历启用 adapter。
- 使用 configured paths 发现 JSON/JSONL 文件。
- 跳过修改时间处于 grace period 内的文件。
- 解析 usage record，计算 fingerprint。
- `INSERT OR IGNORE` 写入 `usage_events`。

## `export`

```bash
agent-ledger export --output my-device.aldb
agent-ledger export -o my-device.aldb
```

Flags:

| Flag | 说明 |
|---|---|
| `-o, --output string` | 输出路径；为空时使用 `agent-ledger-export.aldb`。 |

当前 export 是数据库文件复制，不执行路径脱敏、时间范围过滤或压缩。

## `merge`

```bash
agent-ledger merge other-device.aldb
```

参数：

| Argument | 说明 |
|---|---|
| `file.aldb` | 必填，另一个 AgentLedger SQLite export。 |

当前 merge 会验证输入是普通 SQLite 文件，然后 attach 为 `incoming`，通过 `event_fingerprint` 主键插入未见事件。

## `report`

```bash
agent-ledger report [type]
```

Report types:

| Type | 说明 |
|---|---|
| `daily` | 按 UTC 日期聚合。 |
| `weekly` | 按 SQLite `%Y-W%W` 周聚合。 |
| `monthly` | 按月聚合，可用 `--by` 改变分组。 |
| `models` | 按 normalized model 聚合。 |
| `channels` | 按 source channel 聚合。 |
| `devices` | 按 origin device 聚合。 |
| `sessions` | 按 session 聚合，当前固定按 `cost_usd DESC` 排序并限制 50 行。 |

所有 report subcommand 暴露：

| Flag | 说明 |
|---|---|
| `--since string` | 开始日期，格式 `YYYY-MM-DD`。 |
| `--until string` | 结束日期，格式 `YYYY-MM-DD`。 |
| `--json` | 输出 JSON 数组。 |
| `--by string` | Cobra 暴露在所有 report 子命令上，但当前只有 `monthly` 使用，允许 `agent`、`model`、`provider`。 |

当前没有 `--order` 或 `--month` flag。

## `status`

```bash
agent-ledger status
```

输出数据库路径、事件数、设备数、导入次数、source file 计数、token 汇总和成本字段汇总。

## `doctor`

```bash
agent-ledger doctor
```

输出配置路径、数据库路径、数据库是否存在，以及每个启用 adapter 发现的源文件数量。该命令会读取配置并扫描 configured paths。

## `verify`

```bash
agent-ledger verify
```

执行：

```sql
PRAGMA integrity_check;
```

## `vacuum`

```bash
agent-ledger vacuum
```

执行：

```sql
VACUUM;
```

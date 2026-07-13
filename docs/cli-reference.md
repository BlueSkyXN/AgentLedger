# CLI Reference

本文档按当前 Cobra help 和 Go 代码整理。命令面以 `cmd/` 为准。

## Root

```bash
agent-ledger [command]
```

当前命令：

| Command | 当前状态 | 说明 |
|---|---|---|
| `init` | 已实现 | 创建或复用配置和 v2 数据库；支持 `--reset` 重建本地空库。 |
| `import` | 已实现 | 从启用的本机 agent 日志导入 usage events。 |
| `export` | 已实现 | 把当前 SQLite 数据库复制为 `.aldb` 文件。 |
| `merge [file.aldb]` | 已实现 | 合并另一个 schema v2 AgentLedger SQLite export。 |
| `report` | 已实现 | 报表命令组。 |
| `status` | 已实现 | 输出数据库统计。 |
| `doctor` | 已实现 | 输出配置、数据库和源文件发现诊断。 |
| `verify` | 已实现 | 运行 SQLite integrity check。 |
| `vacuum` | 已实现 | 运行 SQLite vacuum。 |
| `serve` | 已实现 | 启动本机只读 Web 面板和 `/api/v1/*` JSON API。 |
| `completion` | 已实现 | Cobra 自动生成的 shell completion 命令。 |

当前没有 `cleanup`、`restore`、`pricing` 或 `workspace` 命令。当前 `serve` 是只读面板，不提供浏览器触发 import/merge/vacuum 的写操作。

## `init`

```bash
agent-ledger init
agent-ledger init --reset
```

行为：

- 加载或创建 TOML 配置。
- 打开并初始化 SQLite schema v2。
- 如果检测到旧 schema，普通 `init` 会报错并提示 reset。
- `--reset` 会删除当前数据库、WAL、SHM 文件，然后重建空的 v2 数据库。

## `import`

```bash
agent-ledger import
```

当前导入行为由配置文件控制。

关键行为：

- 遍历启用 adapter。
- 使用 configured paths 发现 JSON/JSONL 文件。
- 对修改时间处于 grace period 内的文件做短暂稳定性检查；size / mtime 稳定则解析，不稳定才跳过。
- 解析 usage record，计算 fingerprint。
- upsert 写入 `usage_events`。
- 重复事件按完整度保留更完整记录。

完整度优先级：有 timing、有 recorded cost、有 model、token 总量更高。

## `export`

```bash
agent-ledger export --output usage.aldb
agent-ledger export -o usage.aldb
```

Flags:

| Flag | 说明 |
|---|---|
| `-o, --output string` | 输出路径；为空时使用 `agent-ledger-export.aldb`。 |

当前 export 使用 SQLite `VACUUM INTO` 生成 `.aldb` 副本。默认 `[privacy].redact_paths_on_export = true` 时会清空 `project_path`、`source_file` 和 `raw_usage_json`；当前不执行时间范围过滤或压缩。

## `merge`

```bash
agent-ledger merge usage.aldb
```

参数：

| Argument | 说明 |
|---|---|
| `file.aldb` | 必填，另一个 schema v2 AgentLedger SQLite export。 |

当前 merge 会验证输入是普通 SQLite 文件，并要求 incoming 数据库 `meta.schema_version` 为 `2`。合并只插入本地未见过的 `usage_events`。

## `report`

```bash
agent-ledger report [type]
```

Report types:

| Type | 说明 |
|---|---|
| `daily` | 按日期聚合。 |
| `weekly` | 按 SQLite `%Y-W%W` 周聚合。 |
| `monthly` | 按月聚合。 |
| `models` | 按 normalized model 聚合。 |
| `channels` | 按 agent 来源渠道聚合。 |
| `projects` | 按项目标签聚合。 |
| `sessions` | 按 session 聚合。 |
| `slow` | 慢请求列表。 |

所有 report subcommand 暴露：

| Flag | 说明 |
|---|---|
| `--since string` | 开始日期，格式 `YYYY-MM-DD`。 |
| `--until string` | 结束日期，格式 `YYYY-MM-DD`。 |
| `--channel string` | 过滤 agent 来源渠道。 |
| `--provider string` | 过滤 provider。 |
| `--model string` | 过滤 normalized model。 |
| `--session string` | 过滤 session id。 |
| `--project string` | 过滤项目标签或原始项目路径。 |
| `--cost string` | 成本显示模式：`recorded`、`estimated`、`both` 或 `none`；默认 `recorded`。 |
| `--pricing string` | estimated cost 使用的 JSON pricing profile；为空时使用内置 `pricing/pricing.v1.json`。 |
| `--json` | 输出 JSON。 |

`report daily`、`report weekly`、`report monthly` 额外支持：

| Flag | 说明 |
|---|---|
| `--by string` | 在时间桶内继续按 `channel`、`model`、`provider`、`session` 或 `project` 拆分。 |

`report slow` 额外支持：

| Flag | 说明 |
|---|---|
| `--sort string` | `output_tps`、`ttft_ms` 或 `total_duration_ms`。 |
| `--limit int` | 返回条数，默认 50。 |

`--cost recorded` 只显示 `recorded_cost_usd` 聚合值，也就是来源日志明确给出的 USD 成本。`--cost estimated` 和 `--cost both` 会按 pricing JSON 对 token bucket 做只读估算，并返回 pricing coverage / confidence；估算结果不会写入 SQLite。`report slow` 当前不支持 estimated cost。

## `status`

```bash
agent-ledger status
```

输出数据库路径、schema version、事件数、导入次数、token 汇总和 recorded cost 汇总。

## `doctor`

```bash
agent-ledger doctor
```

输出配置路径、数据库路径、数据库是否存在，以及每个启用 adapter 发现的源文件数量。该命令会读取配置并扫描 configured paths。

```bash
agent-ledger doctor codex
```

输出 Codex 本地日志诊断：raw `token_count` / `task_complete` 覆盖、当前 `duplicate_policy`、ledger 与 `ccusage_compatible` 两种口径的事件数和 token 差异，以及模型分布。

`doctor` 和 `doctor codex` 不创建配置或数据库目录；配置不存在时使用内存默认值完成诊断。

## `verify`

```bash
agent-ledger verify
```

执行：

```sql
PRAGMA integrity_check;
```

`verify` 使用基础只读 SQLite 连接，不要求数据库已经具备当前完整 v2 schema，因此可在 additive migration 前检查旧版或待升级数据库。它不会初始化、升级或替换数据库。

## `vacuum`

```bash
agent-ledger vacuum
```

执行：

```sql
VACUUM;
```

## `serve`

```bash
agent-ledger serve
agent-ledger serve --addr 127.0.0.1:54217 --static-dir web/dist
```

Flags:

| Flag | 说明 |
|---|---|
| `--addr string` | 本地监听地址，默认 `127.0.0.1:54217`（高位端口）。当前版本只允许 loopback host。 |
| `--static-dir string` | React 面板构建目录，默认 `web/dist`。不存在时使用内置 placeholder。 |

当前 `serve` 只提供只读能力，不暴露 import、merge、vacuum 或配置修改 API。

主要只读 API：

| Method | Path | 说明 |
|---|---|---|
| `GET` | `/api/v1/health` | 版本、数据库路径、数据库大小、面板资源模式。 |
| `GET` | `/api/v1/status` | 数据库统计。 |
| `GET` | `/api/v1/config` | 脱敏配置快照。 |
| `GET` | `/api/v1/analytics/summary` | 总览统计，支持统一 filters。 |
| `GET` | `/api/v1/analytics/timeseries` | 趋势数据，`bucket=daily|weekly|monthly`；可选 `by=channel|model|provider|session|project`。 |
| `GET` | `/api/v1/analytics/breakdown` | 维度排行，`by=channel|model|provider|session|project`。 |
| `GET` | `/api/v1/analytics/slow` | 慢请求列表，`sort=output_tps|ttft_ms|total_duration_ms`。 |
| `GET` | `/api/v1/filter-options` | 当前库中存在的 channel/provider/model/session/project 选项。 |
| `GET` | `/api/v1/events` | 最近 usage events，不返回 raw JSON。 |
| `GET` | `/api/v1/import-runs` | 最近 import runs。 |

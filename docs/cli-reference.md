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
| `compact-raw` | 已实现 | 显式 dry-run 或 apply，将历史 raw evidence 清为 `NULL`。 |
| `vacuum` | 已实现 | 运行 SQLite vacuum。 |
| `serve` | 已实现 | 启动本机只读 Web 面板和 `/api/v1/*` JSON API。 |
| `completion` | 已实现 | Cobra 自动生成的 shell completion 命令。 |

当前没有 `cleanup`、`restore`、`pricing` 或 `workspace` 命令。当前 `serve` 是只读面板，不提供浏览器触发 import/merge/compact-raw/vacuum 的写操作。

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
- 对需要跨文件关系的 adapter，只把通过稳定性检查的文件集交给 `PrepareFileSet`；preparation 失败时该 adapter 本轮 fail closed，不解析其任何文件，其他 adapter 继续。
- 解析 usage record，计算 fingerprint。
- upsert 写入 `usage_events`。
- 重复事件按完整度保留更完整记录。

完整度优先级：有 timing、有 recorded cost、有 model、token 总量更高。

Codex fork replay 无法证明时只 quarantine 对应 child，并记录 warning；它不会阻断同一 adapter 的其他稳定文件。这里的 quarantine 是本轮 import 内存中的整 child 跳过，不会移动、删除或持久隔离源 JSONL，也不是尚未实现的 cleanup/quarantine CLI。只要存在 warning，`import_runs.status` 会写为 `completed_with_warnings`，聚合摘要写入 `import_runs.error`，但 warning-only 的 `agent-ledger import` 仍以成功状态返回。stdout 中 `Events skipped` 只表示 fingerprint/database duplicate，不包含 replay skip、quarantine 或其他 `Source diagnostics`。Source diagnostics 使用明确单位：文件或 child 数显示 `count`，replay usage 显示 `events` / `tokens`。

Codex replay diagnostics：

| Code | 单位 | 含义 |
|---|---|---|
| `codex_fork_files` | `count` | stable file set 中识别出的 fork child 数。 |
| `codex_parent_resolved` | `count` | parent 唯一解析成功的 child 数。 |
| `codex_parent_missing` | `count` | parent 日志不可用的 child 数。 |
| `codex_parent_ambiguous` | `count` | 存在多个非等价 parent 候选的 child 数。 |
| `codex_replay_exact` | `events`, `tokens` | 由 parent prefix 连续精确匹配并过滤的 usage。 |
| `codex_replay_rewritten` | `events`, `tokens` | 由开头 rewritten burst 证据过滤的 usage。 |
| `codex_replay_events` | `events` | exact 与 rewritten replay event 合计。 |
| `codex_replay_tokens` | `tokens` | ledger accounting 下被过滤 replay 的 token impact。 |
| `codex_replay_unresolved` | `count` | 无法安全完成 replay 判定而隔离的 child 数。 |
| `codex_replay_file_changed` | `count` | preparation 后 child/parent identity 发生变化的隔离数。 |
| `codex_replay_plan_failed` | `count` | 全局 replay plan 构建失败次数；非零时本轮 Codex adapter 跳过。 |

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

当前 merge 会验证输入是普通 SQLite 文件，并要求 incoming 数据库 `meta.schema_version` 为 `2`。合并只插入本地未见过的 `usage_events`；重复 `event_id` 不覆盖本地结构化统计事实。所有未见事件的 incoming `raw_usage_json`（包括 legacy、compact 和空字符串）均省略并写为 `NULL`，且 `raw_usage_json IS NOT NULL` 的未见事件计入 `Raw evidence omitted`。

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

输出 Codex 本地日志诊断：raw `token_count` / `task_complete` 覆盖、fork parent resolution、exact/rewritten replay 过滤与 quarantine 计数、当前 `duplicate_policy`、ledger 与 `ccusage_compatible` 两种口径的事件数和 token 差异，以及模型分布。新增 replay diagnostics 只包含聚合计数，不包含 session ID、parent ID 或逐文件 source path；文件和 child 数使用 `count`，usage 使用 `events` / `tokens`。

两种 accounting policy 共享同一个 immutable replay plan 和基于 size/mtime 的 source identity snapshot。`doctor codex` 会逐文件核对 quarantine、`file_changed`、exact/rewritten replay event decisions，并在完成后再次验证整批文件身份；任一文件的 size/mtime identity 发生 drift，或两种 policy 的 quarantine/replay outcome 不一致时，命令会明确返回 `comparison inconclusive` 错误，不会把已检测到的 source drift 伪装成正常 policy delta。能够同时保持 size 和 mtime 不变的原地内容替换不在该 identity 契约的检测范围内。Replay `tokens` 表示 ledger accounting policy 下避免写入的 token impact；它不参与 policy parity 门禁，因为 ledger cumulative delta 与 `ccusage_compatible` 的 last-or-delta accounting 对同一 replay event 可以得到不同 token 数。

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

## `compact-raw`

```bash
agent-ledger compact-raw --dry-run
agent-ledger compact-raw --apply
```

必须且只能指定一个 action。`--dry-run` 通过严格只读 v2 连接统计所有非 `NULL` raw candidate、已经为 `NULL` 的行数和精确 BLOB 字节变化，零数据库写入。`--apply` 通过严格 `mode=rw` v2 连接启用 connection-local `secure_delete=ON`，按 `rowid` 每批最多 1000 行原子更新，并用 `event_id + 原 raw_usage_json` 防止并发覆盖。它把所有 channel、格式和 dedupe strategy 的历史 `raw_usage_json` 统一写为 `NULL`；命令可中断后重跑，且不会自动运行 `VACUUM`。

## `vacuum`

```bash
agent-ledger vacuum
```

执行：

```sql
VACUUM;
```

`vacuum` 使用严格的现有 v2 `mode=rw` 打开路径，不创建数据库、不初始化或升级 schema，也不改变 journal mode。运行前应停止访问同一数据库的 `serve` 和其它 writer。

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

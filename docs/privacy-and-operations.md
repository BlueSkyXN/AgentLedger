# Privacy and Operations

AgentLedger 是 local-first CLI，不主动联网。主要风险来自本机日志、SQLite 数据库和 `.aldb` 导出文件本身。

## 数据敏感性

数据库可能包含：

- agent 名称、模型名、provider、时间戳和 token 统计。
- session / request / message 标识。
- `usage_events.source_file`、`line_number`、`project_path` 等来源定位信息。
- raw usage JSON envelope。

这些内容足以反映本机 AI Coding Agent 使用痕迹。公开 issue、PR、截图和示例文档中不要粘贴真实数据库内容、真实 session 标识、私有路径或 raw JSON。

## `.aldb` 导出文件

`.aldb` 是 SQLite 数据库副本，不是脱敏报表。

当前：

- 默认 `redact_paths_on_export = true` 时，`export` 会在导出副本中清空 `project_path`、`source_file` 和 `raw_usage_json`。
- 如果关闭 `redact_paths_on_export`，`export` 会生成未脱敏 SQLite 副本。
- `privacy.mode` 是预留配置。
- 没有加密 raw archive。

对外分享前，仍应按私有数据处理；导出副本仍包含 agent、模型、时间、session 和 token 聚合等使用痕迹。

## 维护命令

### `status`

```bash
agent-ledger status
```

用途：快速确认数据库路径、事件量、设备量、导入次数、token 总数和成本字段汇总。

`status` 使用 SQLite `mode=ro` 和 `query_only` 打开现有数据库，不会创建或升级 schema。数据库尚未初始化时，先运行 `agent-ledger init` 或 `agent-ledger import`。

### `doctor`

```bash
agent-ledger doctor
```

用途：检查配置路径、数据库是否存在，以及每个启用 adapter 能发现多少源文件。该命令会扫描 configured paths，但不会导入事件。

`doctor` 和 `doctor codex` 使用现有配置或内存默认值；配置不存在时不会创建 `config.toml` 或数据库目录。

### `verify`

```bash
agent-ledger verify
```

用途：运行 SQLite `PRAGMA integrity_check`。适合在 merge、备份、迁移前后执行，也可以检查 schema v1 或缺少 additive compatibility column 的待升级数据库。

`verify` 使用基础只读 SQLite 连接，不执行 AgentLedger schema validation、schema 初始化、compatibility UPDATE 或索引创建。数据库文件损坏或不是 SQLite 时会返回错误，但不会替换原文件。

### `vacuum`

```bash
agent-ledger vacuum
```

用途：执行 SQLite `VACUUM` 回收空间。它会重写数据库文件；运行前建议先确认没有其它 AgentLedger 进程正在访问同一数据库。

### `serve`

```bash
agent-ledger serve
```

用途：启动本机只读 Web 面板和 `/api/v1/*` JSON API。当前版本默认监听 `127.0.0.1:54217`（高位端口），并且只允许 loopback host。

`serve` 与 `report *` 共用只读数据库打开路径；启动前会校验 v2 三张核心表的全部必需列，查询过程不会创建数据库、表、列或索引。升级后的数据库若只缺 additive v2 compatibility columns，应先显式运行 `agent-ledger init` 或 `agent-ledger import`；核心列损坏或缺失时应从备份恢复，或在备份后使用 `agent-ledger init --reset` 重建。

隐私边界：

- API 和面板不返回 `raw_usage_json`。
- `/api/v1/config` 会对用户主目录路径做 `~` 形式脱敏。
- 面板仍会展示聚合 token、模型、agent、project、session 和数据库状态，应按本机私有使用数据处理。
- 当前没有远程访问和 auth；不要通过代理、端口转发或非 loopback 地址对外暴露。

## Cleanup 边界

当前 CLI 没有 `cleanup` 或 `restore` 命令，也不会移动、删除或隔离原始 agent 日志。

设计稿中的 quarantine、purge、restore、hash 校验清理流程属于 roadmap。实现前不应在公开文档中写成已可用操作。

## 安全默认值

- 默认只读扫描源日志并写入 AgentLedger 自己的 SQLite 数据库。
- 导入会对 grace period 内的近期文件做稳定性检查；仍在变化的文件会跳过。
- merge 会检查输入文件存在、不是目录、并具有 SQLite header。
- monthly report 的 `--by` 使用 allowlist，避免把任意用户输入拼入 SQL label expression。

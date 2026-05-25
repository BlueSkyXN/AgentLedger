# Privacy and Operations

AgentLedger 是 local-first CLI，不主动联网。主要风险来自本机日志、SQLite 数据库和 `.aldb` 导出文件本身。

## 数据敏感性

数据库可能包含：

- agent 名称、模型名、provider、时间戳和 token 统计。
- session / request / message 标识。
- source file 推断信息。
- raw usage JSON envelope。

这些内容足以反映本机 AI Coding Agent 使用痕迹。公开 issue、PR、截图和示例文档中不要粘贴真实数据库内容、真实 session 标识、私有路径或 raw JSON。

## `.aldb` 导出文件

`.aldb` 是 SQLite 数据库副本，不是脱敏报表。

当前：

- `export` 不脱敏。
- `redact_paths_on_export` 是预留配置。
- `privacy.mode` 是预留配置。
- 没有加密 raw archive。

对外分享前，应按私有数据处理。

## 维护命令

### `status`

```bash
agent-ledger status
```

用途：快速确认数据库路径、事件量、设备量、导入次数、token 总数和成本字段汇总。

### `doctor`

```bash
agent-ledger doctor
```

用途：检查配置路径、数据库是否存在，以及每个启用 adapter 能发现多少源文件。该命令会扫描 configured paths，但不会导入事件。

### `verify`

```bash
agent-ledger verify
```

用途：运行 SQLite `PRAGMA integrity_check`。适合在 merge、备份、迁移前后执行。

### `vacuum`

```bash
agent-ledger vacuum
```

用途：执行 SQLite `VACUUM` 回收空间。它会重写数据库文件；运行前建议先确认没有其它 AgentLedger 进程正在访问同一数据库。

### `serve`

```bash
agent-ledger serve
```

用途：启动本机只读 Web 面板和 `/api/v1/*` JSON API。当前版本默认监听 `127.0.0.1:8765`，并且只允许 loopback host。

隐私边界：

- API 和面板不返回 `raw_usage_json` 或 `raw_meta_json`。
- `/api/v1/config` 会对用户主目录路径做 `~` 形式脱敏。
- 面板仍会展示聚合 token、模型、agent、session 和数据库状态，应按本机私有使用数据处理。
- 当前没有远程访问和 auth；不要通过代理、端口转发或非 loopback 地址对外暴露。

## Cleanup 边界

当前 CLI 没有 `cleanup` 或 `restore` 命令，也不会移动、删除或隔离原始 agent 日志。

设计稿中的 quarantine、purge、restore、hash 校验清理流程属于 roadmap。实现前不应在公开文档中写成已可用操作。

## 安全默认值

- 默认只读扫描源日志并写入 AgentLedger 自己的 SQLite 数据库。
- 导入会跳过仍在 grace period 内的近期文件。
- merge 会检查输入文件存在、不是目录、并具有 SQLite header。
- monthly report 的 `--by` 使用 allowlist，避免把任意用户输入拼入 SQL label expression。

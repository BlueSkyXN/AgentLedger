# Privacy and Operations

AgentLedger 是 local-first CLI，不主动联网。主要风险来自本机日志、SQLite 数据库和 `.aldb` 导出文件本身。

## 数据敏感性

数据库可能包含：

- agent 名称、模型名、provider、时间戳和 token 统计。
- session / request / message 标识。
- `usage_events.source_file`、`line_number`、`project_path` 等来源定位信息。
- `raw_usage_json` 是历史兼容列；statistics-only 新写入保持 `NULL`，旧库迁移前可能仍含 legacy 或 compact raw。

这些内容足以反映本机 AI Coding Agent 使用痕迹。公开 issue、PR、截图和示例文档中不要粘贴真实数据库内容、真实 session 标识、私有路径或 raw JSON。

## `.aldb` 导出文件

`.aldb` 是 SQLite 数据库副本，不是脱敏报表。

当前：

- 默认 `redact_paths_on_export = true` 时，`export` 会在导出副本中清空 `project_path`、`source_file` 和 `raw_usage_json`。
- 如果关闭 `redact_paths_on_export`，`export` 会生成未脱敏 SQLite 副本。
- `privacy.mode = "statistics"` 是 canonical 写入策略；旧值 `envelope` 是行为相同的 deprecated compatibility alias。`full`、`none`、空值和其它值会被写入命令拒绝。
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

用途：执行 SQLite `VACUUM` 回收空间。它会重写数据库文件；运行前应停止访问同一数据库的 `serve` 和其它 writer。命令使用严格 `mode=rw` v2 打开路径，不创建、初始化或升级数据库，也不改变 journal mode。

### `compact-raw`

```bash
agent-ledger compact-raw --dry-run
agent-ledger compact-raw --apply
```

`--dry-run` 使用严格只读 v2 连接，只输出所有非 `NULL` raw 的行数和预计逻辑字节变化。`--apply` 使用严格 `mode=rw` v2 连接，启用 connection-local `secure_delete=ON`，按 `rowid` 分批把所有 channel、格式和 dedupe strategy 的历史 `raw_usage_json` 写为 `NULL`。每批最多 1000 行并独立提交；更新只修改 `raw_usage_json`，可中断后幂等重跑，不自动执行 `VACUUM`。

迁移旧库前应先创建 SQLite 一致性备份，并在 apply 前后验证全部非 raw 列、关键聚合和 `PRAGMA integrity_check` 不变。物理空间只有在正确性验证后显式运行 `vacuum` 才会回收。

### `serve`

```bash
agent-ledger serve
```

用途：启动本机只读 Web 面板和 `/api/v1/*` JSON API。当前版本默认监听 `127.0.0.1:54217`（高位端口），并且只允许 loopback host。

`serve` 与 `report *` 共用只读数据库打开路径；启动前会校验 v2 三张核心表的全部必需列，查询过程不会创建数据库、表、列或索引。升级后的数据库若只缺 additive v2 compatibility columns，应先显式运行 `agent-ledger init` 或 `agent-ledger import`；核心列损坏或缺失时应从备份恢复，或在备份后使用 `agent-ledger init --reset` 重建。

前台、后台 `screen` 会话、全页面/API 验收和停止命令见 [Local Preview](local-preview.md)。后台会话只解决当前登录会话内的持续预览，不等于开机自启服务；在 macOS 上从 LaunchAgent 访问外置卷时，还必须单独验证系统隐私权限和重新登录后的真实读盘能力。

隐私边界：

- API 和面板不返回 `raw_usage_json`。
- `/api/v1/config` 会对用户主目录路径做 `~` 形式脱敏。
- 面板仍会展示聚合 token、模型、agent、project、session 和数据库状态，应按本机私有使用数据处理。
- 当前没有远程访问和 auth；不要通过代理、端口转发或非 loopback 地址对外暴露。

## Codex replay warning 的运维语义

Codex fork/subagent 导入会在 fingerprint 之前过滤能够证明的 parent-prefix 和 rewritten-burst replay。正常过滤只进入 `Source diagnostics`，不会计入 fingerprint duplicate，也不会把 import 标为 warning。

以下情况会 fail closed，并在本轮 import 内跳过对应 child 的全部 usage：

- parent session 缺失，且 child 开头不足以证明 rewritten burst；
- parent 候选不唯一或自指；
- fork boundary 或 comparison usage 无法证明；
- preparation 后 child 或 parent 的 size/mtime 发生变化；
- replay planner 全局失败时，本轮整个 Codex adapter 跳过。

这里的 replay `quarantine` 是内存中的本轮整文件隔离策略：不会移动、重命名、删除源 JSONL，也没有可恢复的 quarantine 目录。它和配置中尚未实现的 cleanup/quarantine 占位不是同一功能。

存在隔离时，`import_runs.status = completed_with_warnings`；warning-only import 仍以成功 exit status 返回，避免一个不确定 child 阻断其他 agent 和稳定 Codex 文件。应同时查看 stdout 的 `Source diagnostics` 和 `import_runs.error`，不能只看 exit code。

`codex_replay_file_changed` 通常表示源 JSONL 在 preparation 与 parse 之间仍在写入。等相关任务安静后重跑普通 `agent-ledger import` 即可；fingerprint/upsert 会让已写入记录保持幂等。`codex_parent_missing` 不会靠重跑自动消失，除非 parent 日志随后恢复或被显式加入 configured paths。不要为了清零 warning 关闭 replay filter 或手工猜测 parent/model。

正式库重建、二次导入幂等、SQLite backup、原子替换和回滚门槛见 [Database Rebuild](database-rebuild.md)。默认 redacted `.aldb` export 会清空路径和历史 raw 字段，适合迁移/分享，不等同于保留原库全部本机字段的精确回滚备份。

## Cleanup 边界

当前 CLI 没有 `cleanup` 或 `restore` 命令，也不会移动、删除或持久隔离原始 agent 日志。

设计稿中的 quarantine、purge、restore、hash 校验清理流程属于 roadmap。实现前不应在公开文档中写成已可用操作。

## 安全默认值

- 默认只读扫描源日志并写入 AgentLedger 自己的 SQLite 数据库。
- 所有 adapter 只在内存中读取完整 source object 以计算 usage、hash 和 fingerprint fallback；SQLite 只持久化结构化统计事实，完整重放依赖原始 agent JSON/JSONL。
- 导入会对 grace period 内的近期文件做稳定性检查；仍在变化的文件会跳过。
- merge 会检查输入文件存在、不是目录、并具有 SQLite header。
- monthly report 的 `--by` 使用 allowlist，避免把任意用户输入拼入 SQL label expression。

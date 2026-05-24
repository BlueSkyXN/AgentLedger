# Roadmap

本文档只记录 planned/reserved 能力。它们来自设计稿和当前 schema/config 预留，不代表当前 CLI 已经实现。

## Planned: cleanup / quarantine / restore

目标：

- 在确认 source file 已成功导入后，安全清理本机原始日志。
- 默认 quarantine，不直接删除。
- 通过 hash 校验避免清理已变化文件。
- 支持 restore 和 quarantine purge。

当前状态：

- 配置中已有 `[cleanup]` 预留键。
- schema 中已有 `source_files.cleanup_status` 等字段。
- Go CLI 当前没有 `cleanup` 或 `restore` 命令。
- 当前导入路径尚未写入完整 source file tracking。

## Planned: source file and raw record tracking

目标：

- 记录 source、source file、raw record、parse status 和 import status。
- 支持真正的增量导入与 cleanup eligibility 判断。
- 区分本机文件和 merge 进来的远端文件记录。

当前状态：

- `sources`、`source_files`、`raw_records` 表已在 schema 中。
- 当前 import 主路径直接写 `usage_events`，没有填充这些表。

## Planned: event observations and conflicts

目标：

- 记录同一事件被哪些设备观察到。
- 在跨设备 merge 时保留 observation。
- 对同一 fingerprint 下字段差异做 conflict 记录和 resolution。

当前状态：

- `event_observations`、`event_conflicts` 表已在 schema 中。
- 当前 merge 只对 `usage_events` 做 `INSERT OR IGNORE`。

## Planned: pricing and cost versioning

目标：

- 支持 logged cost 与 calculated cost。
- 保存 `pricing_source`、`pricing_version` 和 `cost_source`。
- 允许未来按不同价格版本重算或对比。

当前状态：

- cost/pricing 字段已存在于 `usage_events`。
- 当前没有模型价格表，也没有自动成本估算逻辑。

## Planned: timezone, currency, and richer reports

目标：

- 让报表按配置 timezone 计算日/周/月 bucket。
- 支持 currency 配置或明确的单币种成本口径。
- 增加 workspace/project 映射和趋势图 JSON 输出。

当前状态：

- `[reports].timezone` 和 `[reports].currency` 是预留配置。
- 当前报表使用 SQLite UTC date bucket。
- 当前 project/workspace 字段存在于 schema/model，但 adapter 尚未系统填充。

## Planned: privacy modes and export redaction

目标：

- 支持 normalized-only、usage envelope、encrypted raw archive 等隐私模式。
- export 时按配置脱敏路径或 raw 字段。
- 对公开分享提供安全导出模式。

当前状态：

- `[privacy].mode` 和 `redact_paths_on_export` 是预留配置。
- 当前 export 只是复制 SQLite 数据库。

## Planned: additional adapters

候选来源：

- OpenCode
- Copilot CLI
- Amp
- Droid
- Codebuff
- Hermes

新增 adapter 前需要先拿到真实日志样例或 fixture，并补解析与 fingerprint 回归测试。

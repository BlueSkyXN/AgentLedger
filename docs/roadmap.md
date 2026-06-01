# Roadmap

v2 当前主线是本地 usage analytics：导入、去重、token/timing 聚合、只读 API、CLI 报表和 Web 面板。

## Pricing mode

`recorded_cost_usd` 仍只保留日志中已有的明确 USD 成本。CLI report 已支持 `--cost estimated|both|none`，通过标准 JSON pricing profile 做只读估算；estimated cost 不写回 `usage_events`。后续如果要增强成本计算，应继续作为独立 pricing mode：

- 明确价格表来源和版本。
- 区分 recorded cost 和 calculated cost。
- 保留可审计的计算规则。
- 只有在需要冻结计算快照或审计账单时，再考虑 `cost_runs` / `event_costs` 等持久化表。

## Possible: source file tracking

v2 为了简单已删除 `sources`、`source_files`、`raw_records`。如果后续确实需要增量导入状态、cleanup/quarantine 或 parse error replay，再重新设计 source tracking schema。

## Possible: richer export controls

当前 export 已支持在导出副本中清空路径和 raw usage envelope。后续可增加：

- 时间范围过滤。
- 压缩包导出。

## Possible: timing coverage by adapter

继续补各 agent adapter 对 explicit timing 字段的解析，但仍遵守边界：不从文本长度、相邻 timestamp 或文件顺序推断耗时。

# Roadmap

v2 当前主线是本地 usage analytics：导入、去重、token/timing 聚合、只读 API、CLI 报表和 Web 面板。

## Possible: pricing mode

当前只保留日志中已有的 `recorded_cost_usd`，不做价格表计算。后续如果要加入成本计算，应作为独立 pricing mode：

- 明确价格表来源和版本。
- 区分 recorded cost 和 calculated cost。
- 保留可审计的计算规则。

## Possible: source file tracking

v2 为了简单已删除 `sources`、`source_files`、`raw_records`。如果后续确实需要增量导入状态、cleanup/quarantine 或 parse error replay，再重新设计 source tracking schema。

## Possible: richer export controls

当前 export 已支持在导出副本中清空路径和 raw usage envelope。后续可增加：

- 时间范围过滤。
- 压缩包导出。

## Possible: timing coverage by adapter

继续补各 agent adapter 对 explicit timing 字段的解析，但仍遵守边界：不从文本长度、相邻 timestamp 或文件顺序推断耗时。

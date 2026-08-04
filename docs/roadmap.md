# Roadmap

## 当前 v3 已完成的主线

- 五个本机 Agent adapter 的 Session usage 归一化。
- Schema v3 / identity v2、重复 import 幂等和跨设备 `.aldb` merge。
- 日期、channel、source、provider、model、project、Session 和 token 分项。
- CLI、只读 API v2、Web Session 分析页。
- 查询时 estimated cost 与 pricing coverage。

## 明确 non-goals

- device 资产或设备维度报表；
- file offset/tail reader、source checkpoint、parse-error replay；
- observation/conflict/merge ledger 或持久化 Session；
- 对话正文、完整 raw usage、金额持久化；
- request count、duration、TTFT、TPS、Slow；
- 客户端/OTel/proxy 三方对账；
- 远程同步、托管服务或 telemetry；
- v2 行迁移和 v2 `.aldb` merge。

## 后续候选的进入条件

只有真实规模证明“每次重新扫描”成为瓶颈时，才设计 source checkpoint；只有存在明确审计/冲突追踪用户需求时，才考虑 observation ledger；只有要开放非 loopback 访问时，才先完成认证、授权和隐私模型。任何候选都需要独立 schema/API 设计与回归计划，不能通过恢复 v1/v2 遗留字段顺手实现。

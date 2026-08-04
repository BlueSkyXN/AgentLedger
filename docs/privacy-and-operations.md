# Privacy and Operations

## 本地优先

AgentLedger 不主动联网、不上传 telemetry、不修改源 Session 日志。`serve` 默认且强制 loopback；API 只支持 GET。

## 数据分类

以下均视为私有：本机 agent 日志、SQLite/`.aldb`、event/session/request/message ID、模型、token 汇总、project/source path、import warning、面板截图和导出报表。

数据库不保存：

- 对话正文、thinking、tool 参数、完整 source object 或 raw usage envelope；
- device；
- 金额；
- request count、duration、TTFT、TPS；
- API key、token 或 credentials。

`source_file`、`line_number`、`raw_sha256` 仅用于本机诊断，不参与稳定 identity。

## Export

默认 `redact_paths_on_export=true`：export 副本清空 `project_path`、`source_file` 和 import warning，再执行 `VACUUM`。Identity 与 token facts 不变，因此 redacted export 可重复 merge。

关闭 redaction 会保留私有 locator，必须明确知道输出文件的分享边界。

## Read-only commands

`status`、`report`、`serve` 通过严格 v3 只读入口打开，不创建或迁移数据库。`verify` 只做普通 SQLite integrity；`doctor` 缺 config 时只用内存默认值。

## Writer concurrency

不要并发运行 import、merge、vacuum 或 reset。SQLite busy warning 不等于部分数据一定丢失；先查进程和最新 `import_runs`，确认上一轮 writer 已结束再重试。

## Vacuum

`vacuum` 会重写 DB 文件。运行前停止 serve 和其它 writer，创建可恢复 backup，并在完成后检查 integrity、schema、event/session/token 逻辑计数。

## 正式切换边界

代码通过、candidate 差异可解释、二次 import 幂等、merge/rollback 实验通过，不等于已授权替换正式库。正式替换、停止现有 runtime 和切换 binary 都是独立高影响动作，必须明确确认。

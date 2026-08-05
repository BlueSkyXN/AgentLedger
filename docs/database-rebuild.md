# v2 → v3 Database Rebuild Runbook

v3 不转换 v2 行，也不接受 v2 merge。唯一支持路径是：保留 v2 exact backup，用同一组真实原始 Session 日志在独立 data dir clean rebuild，然后比较差异。

## 1. 冻结 v2 只读基线

记录：

```text
Git HEAD
当前 binary 绝对路径和 SHA-256
config 快照
Agent source roots
源文件 manifest（relative path、size、mtime、hash）
v2 DB exact backup、SHA-256、PRAGMA integrity_check
event/session/import-run 数
按日期/channel/source/model/project 的 token buckets
unknown/fallback 分布
```

exact backup 使用 SQLite backup API；默认 redacted export 不能替代 rollback backup。不得停止/替换正式 runtime 或修改正式 DB。

## 2. 创建独立 candidate

```bash
export AGENT_LEDGER_DATA_DIR="$PWD/local/experiments/usage-v3"
./bin/agent-ledger init
```

复制 Agent paths、Codex duplicate policy 和 timezone，只改变 data dir / DB path。不要复制 v2 DB 行。

## 3. 两次真实导入

```bash
./bin/agent-ledger doctor
./bin/agent-ledger import
./bin/agent-ledger verify
./bin/agent-ledger status
./bin/agent-ledger report daily --json
./bin/agent-ledger report channels --json
./bin/agent-ledger report sources --json
./bin/agent-ledger report providers --json
./bin/agent-ledger report models --json
./bin/agent-ledger report projects --json
./bin/agent-ledger report sessions --json
./bin/agent-ledger import
```

源 manifest 未变化时，第二次必须 added=0、updated=0、rejected=0，skipped=第一次有效事件数。

## 4. 差异报告

比较：

```text
总事件数、distinct session 数
按日期/channel/source/provider/model/project 的 token buckets
unknown/fallback 分布
每个 adapter identity strategy 分布
invalid/rejected/warning reason code
estimated cost 与 pricing coverage
```

只允许：P0 parser 修复新增记录、非法 timestamp/token 拒绝、旧 fingerprint 碰撞拆分、旧 token-based identity 正确合并、pricing provider/channel 修复造成的纯计算差异。

每条差异必须映射到 adapter、event 和 reason code。存在无法解释的 token 差异立即停止切换。

## 5. Merge 实验

构造重叠 A/B v3 `.aldb`：各有独立事件、共享 exact 事件、B 含一个允许的 supplement；另建 token/session 冲突 fixture。

验收：

```text
merge(A,B) = canonical union
重复 merge(B) inserted=0 updated=0
日期/session/model/token 汇总符合预期
redacted export roundtrip identity/totals 不变
冲突 merge 后 destination 逻辑 hash/计数不变
```

## 6. 正式切换闸门

只有代码验证、真实差异报告、幂等和 merge 实验全部通过，并经用户确认差异后，才允许：

1. 停止 writer 和 serve。
2. 再做正式 v2 exact backup、integrity、SHA-256 和逻辑计数。
3. candidate 复制到同文件系统 staging。
4. staging 重查 integrity/schema/events/sessions/tokens。
5. 原子替换正式 DB。
6. 启动已验收 v3 binary。
7. 验证 CLI、API v2、全部 Web 页面和一次 live incremental import。
8. 保留 v2 DB、v2 binary、config snapshot，不删除。

回滚必须同时恢复 v2 binary 和 v2 DB；v2 binary 不得打开 v3 DB。

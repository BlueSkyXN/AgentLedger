# Database Rebuild, Replacement, and Rollback

本文描述需要从本机 agent 源日志重建 AgentLedger 正式 SQLite 时的安全操作链。它不是自动化迁移命令，也不授权覆盖任何真实数据库；执行正式替换前必须根据实际环境取得明确授权。

## 适用边界

适用于：

- adapter 或 fingerprint 修复后，需要从冻结源重建统计事实；
- 需要用 clean rebuild 替换含历史错误记录的正式库；
- 需要保留源日志已无法重现、但经确认必须保留的非目标历史记录，并构造 hybrid candidate。

不适用于：

- 用 `init --reset` 直接清空真实库；
- 把默认 redacted `.aldb` export 当作 exact rollback backup；
- 在源日志持续变化时把一次 live import 当作冻结基线；
- 在没有 integrity、计数和回滚证据时覆盖正式库。

## 1. 记录发布输入

先记录并冻结：

```text
Git commit SHA
实际 executable 路径与 SHA-256
config 快照
source 根目录
source 文件数、总字节数
canonical relative path + size + mtime manifest
manifest SHA-256
```

不要把真实 source path、session ID 或日志内容提交到公开仓库。私有 manifest 应放在仓库外或 ignored `local/`。

冻结时应复制同一轮需要比较的全部来源。Codex fork replay planner 和 parser 必须读取同一个不再变化的文件集；不能先对 live source 做 plan，再对稍后的 live source parse。

## 2. 使用独立 data dir 构建 candidate

为 candidate 创建独立配置和数据库，禁止把第一次试跑直接指向正式库：

```bash
export AGENT_LEDGER_DATA_DIR=/path/to/candidate-data
agent-ledger init
# 编辑 candidate config，使 agent paths 指向冻结 source。
agent-ledger import
agent-ledger verify
agent-ledger status
```

配置应保持与正式口径一致，包括 `privacy.mode`、Codex `duplicate_policy`、timezone 和启用的 agent。candidate 写入仍必须满足 `raw_usage_json IS NULL`。

## 3. 连续导入验证幂等

对同一冻结源再执行一次普通 import：

```bash
agent-ledger import
```

第二次要求：

```text
Events added:   0
Events updated: 0
```

`Events skipped` 应等于该冻结源第一次产生的可写 usage events。每次 import 都会新增一条 `import_runs`，所以不能要求数据库字节、文件哈希或 import-run 总数完全不变。

Expected replay skip 只进入 `Source diagnostics`；parent missing、ambiguous、file changed、unresolved 和 plan failed 必须单独记录。冻结源上不应出现 `file_changed`，否则该 candidate 不是可重复基线。

## 4. Candidate 验收

至少核对：

```text
PRAGMA integrity_check = ok
status 的 schema/events/tokens/import runs
各 channel/provider/model 的 events/tokens
unknown 与 policy-zero 分布
pricing coverage
raw_usage_json non-NULL = 0
dedupe-key 和 same-source duplicate = 0
Codex exact/rewritten/unresolved/quarantine diagnostics
CLI report、只读 API 和 Web 面板计数一致
```

API 最低门槛：

```text
/api/v1/health
/api/v1/status
/api/v1/analytics/summary
/api/v1/analytics/breakdown?by=model
```

Panel HTML、入口 JavaScript 和 CSS 都必须返回 HTTP 200。只验证 health 不等于面板完整可用。

## 5. Hybrid candidate

如果正式库含有冻结源无法重现、但经确认必须保留的历史记录，不要悄悄丢弃。可先用 SQLite backup 复制旧正式库，在副本中移除明确要重建的目标 channel，再 merge clean candidate。

Hybrid candidate 需要重新执行完整的 integrity、计数、幂等、API 和面板验收。必须记录哪些历史数据是“保留但不可由当前 source 重现”，不能把它们混写成 clean rebuild 结果。

## 6. 创建 exact rollback backup

正式替换前，停止同库 writer；建议同时停止 `serve`，便于核对文件身份和 companion file。使用 SQLite backup API：

```bash
db=/path/to/agent-ledger.db
backup=/path/to/backups/agent-ledger-pre-rebuild.db

sqlite3 "$db" ".backup '$backup'"
sqlite3 "file:$backup?mode=ro&immutable=1" "PRAGMA integrity_check;"
shasum -a 256 "$backup"
```

记录 backup 的 events、tokens、import runs、channel totals 和 `raw_usage_json` 数量。SQLite backup 的文件哈希不要求与源 `.db` 相同；验收依据是独立 integrity、逻辑计数和备份自身 SHA-256。

默认 `agent-ledger export` 在 `redact_paths_on_export = true` 时会清空路径和历史 raw 字段，适合可移植/脱敏副本，不是 exact rollback backup。

## 7. 同文件系统原子替换

Candidate 和正式库不在同一文件系统时，不能直接依赖跨卷 `rename`。先用 SQLite backup 把 candidate 写入正式库目录下的临时文件：

```text
<data-dir>/.agent-ledger.db.new-<timestamp>
```

对临时文件重新验证 integrity、events、tokens、import runs、channel totals 和 raw NULL。随后再次确认：

```text
没有 import/merge/vacuum writer
没有 serve listener
没有正式 DB 打开句柄
WAL 不含未 checkpoint 数据
rollback backup 仍为 integrity ok
staged candidate 的 SHA/逻辑计数未变化
```

只有全部满足时，才在正式库目录内用原子 rename 替换主 `.db`。不要删除 rollback backup。

## 8. 替换后验收与 live refresh

立即使用与 candidate 验收相同的 executable：

```bash
agent-ledger verify
agent-ledger status
agent-ledger serve --addr 127.0.0.1:54217 --static-dir web/dist
```

回读 SQLite、health、status、summary、model breakdown 和全部面板页面。然后对正式 live source 执行一次普通 import，吸收冻结后新增日志。

Live source 可能在 preparation 后继续写入。`codex_replay_file_changed` 会安全隔离对应 child，不会退化成无 replay 过滤导入；等相关任务安静后再重跑。严格幂等结论仍应来自冻结 source 的第二次 import，不能因为 live source 有真实新增就宣称失败或成功。

## 9. 回滚门槛

出现以下任一情况应停止新服务并恢复旧库：

- integrity 不是 `ok`；
- candidate/正式库 totals 与批准基线不符；
- replay plan failed、unexpected ambiguity 或冻结源出现 file drift；
- `raw_usage_json` 违反当前 privacy policy；
- SQLite、CLI report、API 或 Web 汇总不一致；
- Panel 静态资源或主要页面无法加载；
- 启动的 executable 不是已验收 commit；
- live import 出现无法解释的 planner/discovery/database 错误。

恢复时同样在正式库目录内先准备并验证 backup 的 staged 副本，再原子替换，不要在活动连接下直接覆盖文件。恢复后重新执行 integrity、status、API 和面板验收。

## 10. 交付证据

最终记录应明确区分：

```text
本地代码/tests/build
Git commit/branch
远程 PR 与 exact-head CI
冻结 source 与 manifest
candidate 与幂等结果
rollback backup 路径、integrity 和 SHA
正式库替换后 totals
live import warnings
runtime PID/listener/HTTP
持久化方式及重启边界
```

缺少其中一层时，应写成“该层未验证”，不能用另一层的成功替代。

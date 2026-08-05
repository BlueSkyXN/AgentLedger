# User Guide

## 日常流程

```bash
agent-ledger doctor
agent-ledger import
agent-ledger status
agent-ledger report daily
agent-ledger report sessions
```

`doctor` 先确认有效 config 和 source roots。不要在已有 import 仍运行时启动第二轮；import 静默并不代表卡死，可先检查进程与最新 `import_runs`。

## 如何理解 import 结果

- `added`：新的稳定 event ID。
- `updated`：同 event 的兼容补充，例如 unknown/fallback 模型升级为直接证据。
- `skipped`：content 完全相同，数据库零写入。
- `rejected`：同 identity 下出现 token/Session/time/direct-model/accounting 冲突或记录本身无效。

`completed_with_warnings` 不等于整个 import 失败；先读取 warning reason 和四类计数。拒绝记录不会覆盖 canonical row。

## Session 页面/报表

Session 行是筛选窗口内实时聚合，不是持久化表：

- first/last date 是事件日期范围，不是工作时长；
- primary model 按 total token 最大，平局按 model ID；
- model count 是窗口内 distinct model；
- estimated cost 使用当前 pricing profile，不是历史账单。

## 多设备

每台设备从本机原始日志生成 v3 `.aldb`：

```bash
agent-ledger export -o laptop.aldb
agent-ledger merge laptop.aldb
```

完全重叠事件 skip；允许补充的事实 update；任何冲突导致整次 merge rollback。AgentLedger 不保存“来自哪台设备”。

## 筛选

channel、source product、provider 是不同维度：

- channel：Agent 类别，如 `codex`。
- source product：日志形态，如 `copilot-otel` 或 `copilot-session-state`。
- provider：模型/provider 证据，如 `openai`、`anthropic`。

Session filter 接受稳定 `session_key` 或 native session ID；project filter 使用本机 path 派生的 basename label。

## 成本

默认 `--cost estimated`。未匹配模型显示 unpriced/null；unknown 的内置零值是 `policy_zero`，不是官方免费；profile unavailable 时 token 报表仍正常。

```bash
agent-ledger report models --cost none
agent-ledger report models --pricing ./my-pricing.json
```

## 重建与正式切换

v3 不迁移 v2。先在独立 `AGENT_LEDGER_DATA_DIR` 重建 candidate，做二次 import、差异报告、merge/rollback 实验。正式 DB 替换需单独确认，详见 [database-rebuild.md](database-rebuild.md)。

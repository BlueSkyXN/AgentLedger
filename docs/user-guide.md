# User Guide

AgentLedger v2 是本地 usage 统计分析器。典型使用流程是：初始化 v2 数据库，导入本机 agent 日志，按渠道 / 模型 / provider / 时间 / session / project 查看统计，并用只读 Web 面板做分析。

## 初始化

```bash
agent-ledger init
```

如果当前本地库是旧 schema，使用：

```bash
agent-ledger init --reset
```

`--reset` 会删除当前配置指向的数据库及 WAL/SHM 文件并重建空库。它不会迁移旧数据。

## 导入

```bash
agent-ledger import
```

导入会遍历配置中启用的 agent 路径，解析 usage 记录并 upsert 到 `usage_events`。重复事件按完整度保留更完整版本：有 timing、有 recorded cost、有 model、token 总量更高。

## 常用报表

```bash
agent-ledger report daily
agent-ledger report weekly --channel codex
agent-ledger report monthly --provider anthropic
agent-ledger report models --json
agent-ledger report channels
agent-ledger report projects --channel claude
agent-ledger report sessions --since 2026-05-01
agent-ledger report slow --sort ttft_ms --limit 20
```

统一过滤参数：

```bash
--since YYYY-MM-DD
--until YYYY-MM-DD
--channel string
--provider string
--model string
--session string
--project string
--json
```

## 指标语义

- `channel`: Agent 来源，例如 `claude`、`codex`、`copilot`、`gemini`。
- `provider`: 模型或日志 provider，例如 `anthropic`、`openai`、`google`。
- `model_normalized`: 归一化模型名。
- `project_path`: adapter 解析到的项目路径；报表/API 从它派生项目标签，可用于统计某个项目下的 usage，不代表客户端产品。
- `total_tokens`: source 提供时使用 source 值；否则按 input/output/reasoning/cache 分项计算。
- `ttft_ms`: `first_token_at_ms - request_started_at_ms`。
- `output_duration_ms`: `completed_at_ms - first_token_at_ms`。
- `total_duration_ms`: `completed_at_ms - request_started_at_ms`。
- `output_tps`: `output_tokens / (output_duration_ms / 1000.0)`。

AgentLedger 不从文本长度、相邻 timestamp 或文件顺序推断 token / 耗时。日志没明确提供的 timing 字段保持 `NULL`。

## Web 面板

```bash
agent-ledger serve
```

默认地址：

```text
http://127.0.0.1:54217
```

面板提供 Overview、趋势、渠道 / provider、模型、project / session、慢请求、导入 / 设置等只读页面。

## Export / merge

```bash
agent-ledger export --output usage.aldb
agent-ledger merge usage.aldb
```

导出的 `.aldb` 是 SQLite 数据库文件。merge 只接受 schema v2 数据库，只合并未见过的 `usage_events`。

## 隐私提示

本地数据库、`.aldb` 文件和面板截图可能包含 session id、项目路径、模型名、token 用量和 raw usage envelope。公开传播前应按私有使用数据处理。

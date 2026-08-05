# Configuration

默认配置位于 `AGENT_LEDGER_DATA_DIR/config.toml`；未设置环境变量时，源码仓内运行通常使用 `<repo>/local/data/config.toml`。

## 完整 v3 结构

```toml
[database]
path = "/absolute/or/~/agent-ledger.db"

[privacy]
redact_paths_on_export = true

[import]
gracing_minutes = 15

[reports]
timezone = "Asia/Shanghai"
pricing_path = ""

[agents.claude]
enabled = true
paths = ["~/.config/claude/projects", "~/.claude/projects"]

[agents.codex]
enabled = true
paths = ["~/.codex/sessions"]
duplicate_policy = "ledger"

[agents.gemini]
enabled = true
paths = ["~/.gemini"]

[agents.copilot]
enabled = true
paths = ["~/.copilot/otel", "~/.copilot/session-state"]

[agents.workbuddy]
enabled = true
paths = ["~/.workbuddy/projects"]
```

## 字段

| Key | 行为 |
|---|---|
| `database.path` | 当前 v3 SQLite；支持 `~` 展开。 |
| `privacy.redact_paths_on_export` | 默认 export 是否清空 project/source path 与 import warning。 |
| `import.gracing_minutes` | 最近修改文件的稳定性边界；并非 checkpoint。 |
| `reports.timezone` | Go 可加载的 IANA timezone、`UTC` 或 `Local`；用于逐事件日期分桶和日期 filter。 |
| `reports.pricing_path` | 空值使用内置 profile；非空值是默认 profile。无效时 usage 仍返回，pricing unavailable。 |
| `agents.*.enabled` | 是否扫描对应来源。 |
| `agents.*.paths` | Adapter discovery roots。 |
| `agents.codex.duplicate_policy` | `ledger` 或 `ccusage_compatible`；重建 candidate 时应与 v2 baseline 保持一致。 |

CLI `--pricing` 优先于 `reports.pricing_path`，且显式文件无效会直接失败。

## 已删除配置

```text
cleanup.*
import.single_thread
reports.currency
privacy.mode
privacy envelope compatibility alias
```

旧 TOML 中未知键不会恢复相应功能。保存 v3 config 时不会再次写出这些键。

# Configuration

AgentLedger 使用 TOML 配置。配置文件不存在时，`config.Load()` 会写入默认配置。

配置文件保存在数据目录下的 `config.toml`。数据目录选择顺序：

1. 如果设置了 `AGENT_LEDGER_DATA_DIR`，使用该目录。
2. 如果当前工作目录或可执行文件所在目录的上级能找到 `go.mod`，使用 `<repo-root>/local/data`。
3. 否则使用 `~/.local/share/agent-ledger`。

## 默认配置

下面是默认配置的语义示例。实际生成的 `[database].path` 会基于数据目录解析；在源码仓库内运行时通常指向 `<repo-root>/local/data/agent-ledger.db`。

```toml
[database]
path = "local/data/agent-ledger.db"

[privacy]
mode = "envelope"
redact_paths_on_export = true

[import]
gracing_minutes = 15
single_thread = false

[cleanup]
default_mode = "quarantine"
older_than_days = 30
purge_after_days = 90

[reports]
timezone = "Local"
currency = "USD"

[agents.claude]
enabled = true
paths = ["~/.config/claude/projects", "~/.claude/projects"]

[agents.codex]
enabled = true
paths = ["~/.codex/sessions"]

[agents.gemini]
enabled = true
paths = ["~/.gemini"]

[agents.qwen]
enabled = true
paths = ["~/.qwen"]
```

## 当前生效的键

| Key | 当前用途 |
|---|---|
| `[database].path` | SQLite 数据库路径；支持 `~/` 展开。默认生成在数据目录下。 |
| `[import].gracing_minutes` | `import` 跳过最近修改文件的时间窗口。 |
| `[agents.*].enabled` | 是否启用对应 adapter。 |
| `[agents.*].paths` | adapter 扫描的根路径列表；支持 `~/` 展开。 |
| `[reports].timezone` | daily / weekly / monthly 报表分桶和 `--since` / `--until` 日期过滤使用的时区。支持 `Local`、`UTC`、固定偏移如 `+08:00`，以及 Go 可加载的 IANA 时区如 `Asia/Shanghai`。 |

## 当前预留的键

| Key | 当前状态 |
|---|---|
| `[privacy].mode` | 已保存到配置，但当前 import/export 逻辑尚未按该值切换隐私模式。 |
| `[privacy].redact_paths_on_export` | 预留；当前 `export` 只是复制 SQLite 数据库。 |
| `[import].single_thread` | 预留；当前 import 是顺序遍历。 |
| `[cleanup].*` | 预留；当前 CLI 没有 `cleanup` 命令。 |
| `[reports].currency` | 预留；当前没有 currency conversion。 |

## 修改 agent 路径

如果某个 agent 的日志不在默认目录：

```toml
[agents.codex]
enabled = true
paths = ["~/custom-codex-logs"]
```

如果暂时不导入某个 agent：

```toml
[agents.gemini]
enabled = false
paths = ["~/.gemini"]
```

## 路径和隐私

配置里的路径是本机路径，不应写进公开 issue、PR 描述或截图。公开示例使用 `~` 或占位路径即可。

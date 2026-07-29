# Local Preview

AgentLedger 的完整预览由两部分组成：React 静态资源和读取当前 SQLite 的本机只读 API。只发布 `web/dist` 或 GitHub Pages 只能得到静态壳，不能展示真实统计、筛选和导入状态。

## 构建与前置检查

从仓库根目录运行：

```bash
mkdir -p bin
go build -trimpath -o bin/agent-ledger .

cd web
npm ci
npm audit --audit-level=moderate
npm run lint
npm run build
cd ..

./bin/agent-ledger verify
./bin/agent-ledger status
```

`serve` 不会初始化、升级或写入数据库。配置或数据库尚未准备好时，先显式运行 `init` 或 `import`，不要把启动面板当成迁移步骤。

## 前台预览

```bash
./bin/agent-ledger serve \
  --addr 127.0.0.1:54217 \
  --static-dir web/dist
```

打开 <http://127.0.0.1:54217>。当前版本只允许 loopback host，不提供远程认证；不要改成 LAN 地址、公开 tunnel 或反向代理。

## 当前登录会话内后台预览

macOS 通常自带 `screen`。它可以让预览脱离当前终端或 Codex task 继续运行：

```bash
screen -dmS agentledger-preview \
  ./bin/agent-ledger serve \
    --addr 127.0.0.1:54217 \
    --static-dir web/dist
```

检查会话、listener 和 health：

```bash
screen -list
lsof -nP -iTCP:54217 -sTCP:LISTEN
curl -fsS http://127.0.0.1:54217/api/v1/health
```

查看服务输出：

```bash
screen -r agentledger-preview
```

按 `Ctrl-A`、再按 `D` 可重新 detach。停止预览：

```bash
screen -S agentledger-preview -X quit
lsof -nP -iTCP:54217 -sTCP:LISTEN
```

`screen` 会话结束后必须以 listener 为准确认服务已停；不能只看 `screen -list`。如果会话 socket 已消失但上面的 `lsof` 仍返回 AgentLedger PID，先确认该 PID 确实监听 `127.0.0.1:54217`，再正常终止并复查端口：

```bash
kill -TERM <listener-pid>
lsof -nP -iTCP:54217 -sTCP:LISTEN
```

`screen` 不是开机自启服务：注销、重启、外置卷卸载或会话被显式停止后都需要重新启动。macOS LaunchAgent 若读取外置卷，还需要单独解决并验证系统隐私权限；仅看到 launchd PID 不代表数据库、listener 和 HTTP 已可用。

## 全页面验收

逐页打开并确认没有错误提示、无限 loading 或空白 chunk：

| 页面 | URL | 主要内容 |
|---|---|---|
| 总览 | `/` | KPI、价格覆盖、趋势摘要和主要排行。 |
| 趋势 | `/trends` | daily / weekly / monthly 时间序列和维度拆分。 |
| 模型 | `/models` | 模型 token、成本、request coverage 和 timing。 |
| 渠道 | `/agents` | channel / provider 分布。 |
| 会话 | `/sessions` | project / session 聚合。 |
| 慢请求 | `/slow` | output TPS、TTFT 和总耗时排序。 |
| 导入 | `/imports` | 最近 import run、added / updated / skipped 和 warning 状态。 |
| 设置 | `/settings` | 脱敏配置、数据库和 agent discovery 状态。 |

顶部时间范围和 channel、provider、model、session、project filter 应能在各分析页复用；主题切换和浏览器前进/后退也应正常。

## API 验收

下面的只读检查覆盖面板使用的主要 API：

```bash
base=http://127.0.0.1:54217

for route in \
  /api/v1/health \
  /api/v1/status \
  /api/v1/config \
  /api/v1/analytics/summary \
  '/api/v1/analytics/timeseries?bucket=daily' \
  '/api/v1/analytics/breakdown?by=model' \
  '/api/v1/analytics/slow?sort=output_tps&limit=10' \
  /api/v1/filter-options \
  /api/v1/events \
  /api/v1/import-runs
do
  curl -fsS "$base$route" >/dev/null || exit 1
  printf 'ok  %s\n' "$route"
done
```

至少再核对一次 `/api/v1/status` 和 `/api/v1/analytics/summary` 的 `total_events`、`total_tokens`、`import_runs` 与 `agent-ledger status` / SQLite 汇总一致。HTTP 200 只能证明请求成功，不能替代计数一致性。

## 隐私与故障排查

- 面板和 API 不返回 `raw_usage_json`，但 model、session、project、数据库路径和聚合用量仍是本机私有数据；不要把真实页面截图提交到公开 issue 或 PR。
- 首页显示 placeholder 时，重新运行 `npm run build`，并确认 `--static-dir` 指向实际 `web/dist`。
- 端口冲突时先用 `lsof -nP -iTCP:54217 -sTCP:LISTEN` 找到现有 listener；不要盲目重复启动多个实例。
- `serve` 是只读 reader，可以和一次 `import` 并存；仍应确保同一时刻只有一个 writer/import。
- 导入页出现 `completed_with_warnings` 时，按 [Privacy and Operations](privacy-and-operations.md#codex-replay-warning-的运维语义) 区分安全 replay quarantine、文件变化和真正的 planner/discovery 错误。

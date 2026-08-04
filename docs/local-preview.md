# Local Preview

## 构建并启动

```bash
go build -o bin/agent-ledger .
cd web
npm ci
npm run lint
npm run build
cd ..
./bin/agent-ledger serve --addr 127.0.0.1:54217
```

有效启动需要同时满足：进程存在、loopback listener 存在、HTTP health 200。

```bash
lsof -nP -iTCP:54217 -sTCP:LISTEN
curl -fsS http://127.0.0.1:54217/api/v2/health
```

## API smoke

```bash
for path in \
  /api/v2/health \
  /api/v2/status \
  /api/v2/config \
  /api/v2/analytics/summary \
  '/api/v2/analytics/timeseries?bucket=daily' \
  '/api/v2/analytics/breakdown?by=model' \
  /api/v2/filter-options \
  '/api/v2/sessions?limit=20&offset=0' \
  '/api/v2/events?limit=20&offset=0' \
  /api/v2/import-runs
do
  curl -fsS "http://127.0.0.1:54217${path}" >/dev/null
done
```

`/api/v1/health` 应返回 404。

## Web 页面

逐页检查：Overview、Trends、Models、Channels、Sources、Projects、Sessions、Imports、Settings。

重点：

- channel/source/provider 维度分离；
- Session 分页、稳定 `session_key`、first/last date、主模型、模型数、token 明细；
- estimated cost 标注当前 profile，unavailable/unpriced 不显示伪造 `$0`；
- Imports 显示 rejected；
- 不存在 Slow、timing/TPS/request-count/recorded-cost 文案。

## 一致性

HTTP 200 只证明路由可用。还要将 `/api/v2/status`、summary 的 event/session/token 与 CLI `status`、reports 和 SQLite 逻辑查询对齐。真实截图和响应含私有数据，不进入公开产物。

import { useConfig, useHealth, useStatus } from "@/hooks/queries";
import { formatCost, formatInt } from "@/utils/format";

export function SettingsPage() {
  const { data: health } = useHealth();
  const { data: config } = useConfig();
  const { data: status } = useStatus();
  return (
    <div className="page-stack">
      <section className="panel split">
        <div>
          <h2>运行状态</h2>
          <dl>
            <dt>版本</dt><dd>{health?.version ?? "-"}</dd>
            <dt>前端资源</dt><dd>{health?.asset_mode ?? "-"}</dd>
            <dt>数据库</dt><dd className="mono">{health?.database ?? "-"}</dd>
            <dt>数据库大小</dt><dd>{formatInt(health?.database_bytes)} bytes</dd>
          </dl>
        </div>
        <div>
          <h2>账本概况</h2>
          <dl>
            <dt>总事件</dt><dd>{formatInt(status?.total_events)}</dd>
            <dt>总 Tokens</dt><dd>{formatInt(status?.total_tokens)}</dd>
            <dt>总成本</dt><dd>{formatCost(status?.total_cost_usd)}</dd>
            <dt>配置路径</dt><dd className="mono">{config?.config_path ?? "-"}</dd>
          </dl>
        </div>
      </section>
      <section className="panel">
        <h2>Agent 配置</h2>
        <div className="table-wrap">
          <table>
            <thead><tr><th>Agent</th><th>启用</th><th>路径</th></tr></thead>
            <tbody>
              {Object.entries(config?.agents ?? {}).map(([name, agent]) => <tr key={name}><td>{name}</td><td>{agent.enabled ? "是" : "否"}</td><td className="mono">{agent.paths.join(", ") || "-"}</td></tr>)}
              {Object.keys(config?.agents ?? {}).length === 0 && <tr><td colSpan={3} className="empty-cell">暂无配置数据</td></tr>}
            </tbody>
          </table>
        </div>
        <p className="note">{config?.privacy_note ?? "面板只读，不暴露 raw usage JSON。"}</p>
      </section>
    </div>
  );
}

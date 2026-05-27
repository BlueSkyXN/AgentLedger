import { useState } from "react";

import { useEvents, useSessions } from "@/hooks/queries";
import { formatCost, formatDate, formatInt, formatMs, formatTPS, shortHash } from "@/utils/format";

const EVENT_LIMITS = [50, 100, 200, 500];

export function SessionsPage() {
  const [eventLimit, setEventLimit] = useState(100);
  const { data: sessions } = useSessions(50);
  const { data: events } = useEvents(eventLimit);

  return (
    <div className="page-stack">
      <section className="panel">
        <h2>Top 会话</h2>
        <div className="table-wrap">
          <table>
            <thead><tr><th>会话</th><th>事件</th><th>Tokens</th><th>输入</th><th>输出</th><th>缓存写入</th><th>缓存读取</th><th>平均 TPS</th><th>记录成本</th></tr></thead>
            <tbody>
              {(sessions ?? []).map((row) => <tr key={row.label}><td className="mono">{shortHash(row.label)}</td><td>{formatInt(row.events)}</td><td>{formatInt(row.total_tokens)}</td><td>{formatInt(row.input_tokens)}</td><td>{formatInt(row.output_tokens)}</td><td>{formatInt(row.cache_creation_tokens)}</td><td>{formatInt(row.cache_read_tokens)}</td><td>{formatTPS(row.avg_output_tps)}</td><td>{formatCost(row.recorded_cost_usd)}</td></tr>)}
              {(sessions ?? []).length === 0 && <tr><td colSpan={9} className="empty-cell">暂无会话数据</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
      <section className="panel">
        <header className="panel-heading">
          <h2>近期事件</h2>
          <label className="select-label">
            行数
            <select value={eventLimit} onChange={(event) => setEventLimit(Number(event.target.value))}>
              {EVENT_LIMITS.map((value) => <option key={value} value={value}>{value}</option>)}
            </select>
          </label>
        </header>
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>Channel</th><th>模型</th><th>会话</th><th>Tokens</th><th>缓存写入</th><th>缓存读取</th><th>TPS</th><th>TTFT</th><th>去重策略</th></tr></thead>
            <tbody>
              {(events ?? []).map((row) => <tr key={row.event_id}><td>{formatDate(row.timestamp)}</td><td>{row.channel}</td><td>{row.model_normalized ?? row.model_raw ?? "-"}</td><td className="mono">{shortHash(row.session_id)}</td><td>{formatInt(row.total_tokens)}</td><td>{formatInt(row.cache_creation_tokens)}</td><td>{formatInt(row.cache_read_tokens)}</td><td>{formatTPS(row.output_tps)}</td><td>{formatMs(row.ttft_ms)}</td><td>{row.dedupe_strategy}</td></tr>)}
              {(events ?? []).length === 0 && <tr><td colSpan={10} className="empty-cell">暂无事件数据</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

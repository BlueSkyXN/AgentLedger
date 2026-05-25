import { useState } from "react";

import type { SlowSort } from "@/api/types";
import { useSlow } from "@/hooks/queries";
import { formatDate, formatInt, formatMs, formatTPS, shortHash } from "@/utils/format";

const sortOptions: Array<{ value: SlowSort; label: string }> = [
  { value: "output_tps", label: "输出 TPS 最低" },
  { value: "ttft_ms", label: "TTFT 最高" },
  { value: "total_duration_ms", label: "总耗时最高" },
];

export function SlowPage() {
  const [sort, setSort] = useState<SlowSort>("output_tps");
  const { data: rows } = useSlow(sort, 50);

  return (
    <div className="page-stack">
      <section className="panel">
        <header className="panel-heading">
          <h2>慢请求</h2>
          <label className="select-label">
            排序
            <select value={sort} onChange={(event) => setSort(event.target.value as SlowSort)}>
              {sortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </label>
        </header>
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>Channel</th><th>模型</th><th>会话</th><th>输出</th><th>TPS</th><th>TTFT</th><th>输出耗时</th><th>总耗时</th></tr></thead>
            <tbody>
              {(rows ?? []).map((row) => (
                <tr key={row.event_id}>
                  <td>{formatDate(row.timestamp)}</td>
                  <td>{row.channel}</td>
                  <td>{row.model_normalized ?? row.model_raw ?? "-"}</td>
                  <td className="mono">{shortHash(row.session_id)}</td>
                  <td>{formatInt(row.output_tokens)}</td>
                  <td>{formatTPS(row.output_tps)}</td>
                  <td>{formatMs(row.ttft_ms)}</td>
                  <td>{formatMs(row.output_duration_ms)}</td>
                  <td>{formatMs(row.total_duration_ms)}</td>
                </tr>
              ))}
              {(rows ?? []).length === 0 && <tr><td colSpan={9} className="empty-cell">暂无明确 timing 数据</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

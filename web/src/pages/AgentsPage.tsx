import { useMemo } from "react";

import { Chart } from "@/components/Chart";
import { useBreakdown } from "@/hooks/queries";
import { formatCost, formatInt, formatMs, formatTPS } from "@/utils/format";

export function AgentsPage() {
  const { data: channels } = useBreakdown("channel");
  const { data: providers } = useBreakdown("provider");

  const channelOption = useMemo(() => {
    const rows = channels ?? [];
    return { xAxis: { type: "category", data: rows.map((row) => row.label) }, yAxis: { type: "value" }, series: [{ name: "Tokens", type: "bar", data: rows.map((row) => row.total_tokens) }] };
  }, [channels]);

  const providerOption = useMemo(() => {
    const rows = providers ?? [];
    return { tooltip: { trigger: "item" }, series: [{ name: "Provider", type: "pie", radius: "70%", data: rows.map((row) => ({ name: row.label, value: row.total_tokens })) }] };
  }, [providers]);

  return (
    <div className="page-stack">
      <section className="panel split">
        <div>
          <h2>Channel 用量</h2>
          <Chart option={channelOption} />
        </div>
        <div>
          <h2>Provider 占比</h2>
          <Chart option={providerOption} />
        </div>
      </section>
      <section className="panel">
        <h2>Channel 对比</h2>
        <div className="table-wrap">
          <table>
            <thead><tr><th>Channel</th><th>事件</th><th>Tokens</th><th>输入</th><th>输出</th><th>平均 TPS</th><th>平均耗时</th><th>记录成本</th></tr></thead>
            <tbody>
              {(channels ?? []).map((row) => <tr key={row.label}><td>{row.label}</td><td>{formatInt(row.events)}</td><td>{formatInt(row.total_tokens)}</td><td>{formatInt(row.input_tokens)}</td><td>{formatInt(row.output_tokens)}</td><td>{formatTPS(row.avg_output_tps)}</td><td>{formatMs(row.avg_total_duration_ms)}</td><td>{formatCost(row.recorded_cost_usd)}</td></tr>)}
              {(channels ?? []).length === 0 && <tr><td colSpan={8} className="empty-cell">暂无 Channel 数据</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

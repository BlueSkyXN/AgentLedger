import { useMemo } from "react";

import { Chart } from "@/components/Chart";
import { useBreakdown } from "@/hooks/queries";
import { formatCost, formatInt } from "@/utils/format";

export function AgentsPage() {
  const { data: agents } = useBreakdown("agent");
  const { data: devices } = useBreakdown("device");

  const agentOption = useMemo(() => {
    const rows = agents ?? [];
    return { xAxis: { type: "category", data: rows.map((row) => row.label) }, yAxis: { type: "value" }, series: [{ name: "Tokens", type: "bar", data: rows.map((row) => row.total_tokens) }] };
  }, [agents]);

  const deviceOption = useMemo(() => {
    const rows = devices ?? [];
    return { xAxis: { type: "category", data: rows.map((row) => row.label), axisLabel: { rotate: 20 } }, yAxis: { type: "value" }, series: [{ name: "Tokens", type: "bar", data: rows.map((row) => row.total_tokens) }] };
  }, [devices]);

  return (
    <div className="page-stack">
      <section className="panel split">
        <div>
          <h2>Agent 用量</h2>
          <Chart option={agentOption} />
        </div>
        <div>
          <h2>设备用量</h2>
          <Chart option={deviceOption} />
        </div>
      </section>
      <section className="panel">
        <h2>Agent 对比</h2>
        <div className="table-wrap">
          <table>
            <thead><tr><th>Agent</th><th>事件</th><th>Tokens</th><th>输入</th><th>输出</th><th>成本</th></tr></thead>
            <tbody>
              {(agents ?? []).map((row) => <tr key={row.label}><td>{row.label}</td><td>{formatInt(row.events)}</td><td>{formatInt(row.total_tokens)}</td><td>{formatInt(row.input_tokens)}</td><td>{formatInt(row.output_tokens)}</td><td>{formatCost(row.cost_usd)}</td></tr>)}
              {(agents ?? []).length === 0 && <tr><td colSpan={6} className="empty-cell">暂无 Agent 数据</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

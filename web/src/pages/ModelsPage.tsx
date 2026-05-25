import { useMemo } from "react";

import { Chart } from "@/components/Chart";
import { useBreakdown } from "@/hooks/queries";
import { formatCost, formatInt, formatMs, formatTPS } from "@/utils/format";

export function ModelsPage() {
  const { data: models } = useBreakdown("model");
  const { data: providers } = useBreakdown("provider");

  const modelOption = useMemo(() => {
    const rows = (models ?? []).slice(0, 12);
    return {
      tooltip: { trigger: "axis" },
      xAxis: { type: "category", data: rows.map((row) => row.label), axisLabel: { rotate: 28 } },
      yAxis: { type: "value" },
      series: [{ name: "Tokens", type: "bar", data: rows.map((row) => row.total_tokens) }],
    };
  }, [models]);

  const providerOption = useMemo(() => {
    const rows = providers ?? [];
    return { tooltip: { trigger: "item" }, series: [{ name: "服务商", type: "pie", radius: "70%", data: rows.map((row) => ({ name: row.label, value: row.total_tokens })) }] };
  }, [providers]);

  return (
    <div className="page-stack">
      <section className="panel split">
        <div>
          <h2>Top 模型</h2>
          <Chart option={modelOption} />
        </div>
        <div>
          <h2>服务商占比</h2>
          <Chart option={providerOption} />
        </div>
      </section>
      <section className="panel">
        <h2>模型明细</h2>
        <div className="table-wrap">
          <table>
            <thead><tr><th>模型</th><th>事件</th><th>Tokens</th><th>输入</th><th>输出</th><th>推理</th><th>平均 TPS</th><th>平均 TTFT</th><th>记录成本</th></tr></thead>
            <tbody>
              {(models ?? []).map((row) => <tr key={row.label}><td>{row.label}</td><td>{formatInt(row.events)}</td><td>{formatInt(row.total_tokens)}</td><td>{formatInt(row.input_tokens)}</td><td>{formatInt(row.output_tokens)}</td><td>{formatInt(row.reasoning_tokens)}</td><td>{formatTPS(row.avg_output_tps)}</td><td>{formatMs(row.avg_ttft_ms)}</td><td>{formatCost(row.recorded_cost_usd)}</td></tr>)}
              {(models ?? []).length === 0 && <tr><td colSpan={9} className="empty-cell">暂无模型数据</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

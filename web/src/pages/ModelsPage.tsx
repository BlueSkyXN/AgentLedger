import { useMemo } from "react";

import { Chart } from "@/components/Chart";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import type { MetricRow } from "@/api/types";
import { useBreakdown } from "@/hooks/queries";
import { formatCost, formatInt, formatMs, formatPercent, formatTPS } from "@/utils/format";

const modelColumns: Array<DataTableColumn<MetricRow>> = [
  { key: "model", label: "模型", render: (row) => row.label, value: (row) => row.label },
  { key: "events", label: "事件", render: (row) => formatInt(row.events), value: (row) => row.events, numeric: true },
  { key: "total_tokens", label: "Tokens", render: (row) => formatInt(row.total_tokens), value: (row) => row.total_tokens, numeric: true },
  { key: "input_tokens", label: "输入", render: (row) => formatInt(row.input_tokens), value: (row) => row.input_tokens, numeric: true },
  { key: "output_tokens", label: "输出", render: (row) => formatInt(row.output_tokens), value: (row) => row.output_tokens, numeric: true },
  { key: "cache_creation_tokens", label: "缓存写入", render: (row) => formatInt(row.cache_creation_tokens), value: (row) => row.cache_creation_tokens, numeric: true },
  { key: "cache_read_tokens", label: "缓存读取", render: (row) => formatInt(row.cache_read_tokens), value: (row) => row.cache_read_tokens, numeric: true },
  { key: "reasoning_tokens", label: "推理", render: (row) => formatInt(row.reasoning_tokens), value: (row) => row.reasoning_tokens, numeric: true },
  { key: "avg_output_tps", label: "平均 TPS", render: (row) => formatTPS(row.avg_output_tps), value: (row) => row.avg_output_tps, numeric: true },
  { key: "avg_ttft_ms", label: "平均 TTFT", render: (row) => formatMs(row.avg_ttft_ms), value: (row) => row.avg_ttft_ms, numeric: true },
  { key: "cost", label: "成本", render: (row) => formatCost(row.estimated_cost_usd), value: (row) => row.estimated_cost_usd, numeric: true },
  { key: "coverage", label: "覆盖率", render: (row) => formatPercent(row.pricing?.token_coverage_ratio ?? row.pricing?.coverage_ratio), value: (row) => row.pricing?.token_coverage_ratio ?? row.pricing?.coverage_ratio, numeric: true },
];

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
        <DataTable rows={models ?? []} columns={modelColumns} rowKey={(row) => row.label} emptyText="暂无模型数据" defaultSortKey="total_tokens" />
      </section>
    </div>
  );
}

import { useMemo } from "react";

import type { MetricRow } from "@/api/types";
import { Chart } from "@/components/Chart";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import { useBreakdown } from "@/hooks/queries";
import { formatEstimatedCost, formatInt } from "@/utils/format";

const sourceColumns: Array<DataTableColumn<MetricRow>> = [
  { key: "source_product", label: "来源", render: (row) => row.label, value: (row) => row.label },
  { key: "events", label: "事件", render: (row) => formatInt(row.events), value: (row) => row.events, numeric: true },
  { key: "total_tokens", label: "Tokens", render: (row) => formatInt(row.total_tokens), value: (row) => row.total_tokens, numeric: true },
  { key: "input_tokens", label: "输入", render: (row) => formatInt(row.input_tokens), value: (row) => row.input_tokens, numeric: true },
  { key: "output_tokens", label: "输出", render: (row) => formatInt(row.output_tokens), value: (row) => row.output_tokens, numeric: true },
  { key: "cache_creation_tokens", label: "缓存写入", render: (row) => formatInt(row.cache_creation_tokens), value: (row) => row.cache_creation_tokens, numeric: true },
  { key: "cache_read_tokens", label: "缓存读取", render: (row) => formatInt(row.cache_read_tokens), value: (row) => row.cache_read_tokens, numeric: true },
  { key: "reasoning_tokens", label: "推理", render: (row) => formatInt(row.reasoning_tokens), value: (row) => row.reasoning_tokens, numeric: true },
  { key: "estimated_cost_usd", label: "即时估算", render: (row) => formatEstimatedCost(row.estimated_cost_usd, row.pricing), value: (row) => row.estimated_cost_usd, numeric: true },
];

export function SourcesPage() {
  const { data: sources } = useBreakdown("source_product");
  const chartOption = useMemo(() => {
    const rows = sources ?? [];
    return {
      tooltip: { trigger: "item" },
      series: [{ name: "来源", type: "pie", radius: "70%", data: rows.map((row) => ({ name: row.label, value: row.total_tokens })) }],
    };
  }, [sources]);

  return (
    <div className="page-stack">
      <section className="panel split">
        <div>
          <h2>来源 Tokens 占比</h2>
          <Chart option={chartOption} />
        </div>
        <div>
          <h2>来源说明</h2>
          <p className="panel-subtitle">来源维度区分具体日志形态，例如 Claude Code、Codex CLI、Copilot OTel 或 session-state。筛选来源不会改变只读统计口径。</p>
        </div>
      </section>
      <section className="panel">
        <h2>来源明细</h2>
        <DataTable rows={sources ?? []} columns={sourceColumns} rowKey={(row) => row.label} emptyText="暂无来源数据" defaultSortKey="total_tokens" />
      </section>
    </div>
  );
}

import { useMemo } from "react";

import { Chart } from "@/components/Chart";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import type { MetricRow } from "@/api/types";
import { useBreakdown } from "@/hooks/queries";
import { formatEstimatedCost, formatInt } from "@/utils/format";

const channelColumns: Array<DataTableColumn<MetricRow>> = [
  { key: "channel", label: "Channel", render: (row) => row.label, value: (row) => row.label },
  { key: "events", label: "事件", render: (row) => formatInt(row.events), value: (row) => row.events, numeric: true },
  { key: "total_tokens", label: "Tokens", render: (row) => formatInt(row.total_tokens), value: (row) => row.total_tokens, numeric: true },
  { key: "input_tokens", label: "输入", render: (row) => formatInt(row.input_tokens), value: (row) => row.input_tokens, numeric: true },
  { key: "output_tokens", label: "输出", render: (row) => formatInt(row.output_tokens), value: (row) => row.output_tokens, numeric: true },
  { key: "cache_creation_tokens", label: "缓存写入", render: (row) => formatInt(row.cache_creation_tokens), value: (row) => row.cache_creation_tokens, numeric: true },
  { key: "cache_read_tokens", label: "缓存读取", render: (row) => formatInt(row.cache_read_tokens), value: (row) => row.cache_read_tokens, numeric: true },
  { key: "reasoning_tokens", label: "推理", render: (row) => formatInt(row.reasoning_tokens), value: (row) => row.reasoning_tokens, numeric: true },
  { key: "estimated_cost_usd", label: "即时估算", render: (row) => formatEstimatedCost(row.estimated_cost_usd, row.pricing), value: (row) => row.estimated_cost_usd, numeric: true },
];

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
        <DataTable rows={channels ?? []} columns={channelColumns} rowKey={(row) => row.label} emptyText="暂无 Channel 数据" defaultSortKey="total_tokens" />
      </section>
    </div>
  );
}

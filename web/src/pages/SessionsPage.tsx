import { useState } from "react";

import { DataTable, type DataTableColumn } from "@/components/DataTable";
import type { EventItem, MetricRow } from "@/api/types";
import { useEvents, useSessions } from "@/hooks/queries";
import { formatCost, formatDate, formatInt, formatMs, formatPercent, formatTPS, shortHash } from "@/utils/format";

const EVENT_LIMITS = [50, 100, 200, 500];

const sessionColumns: Array<DataTableColumn<MetricRow>> = [
  { key: "session", label: "会话", render: (row) => <span className="mono">{shortHash(row.label)}</span>, value: (row) => row.label },
  { key: "events", label: "事件", render: (row) => formatInt(row.events), value: (row) => row.events, numeric: true },
  { key: "total_tokens", label: "Tokens", render: (row) => formatInt(row.total_tokens), value: (row) => row.total_tokens, numeric: true },
  { key: "input_tokens", label: "输入", render: (row) => formatInt(row.input_tokens), value: (row) => row.input_tokens, numeric: true },
  { key: "output_tokens", label: "输出", render: (row) => formatInt(row.output_tokens), value: (row) => row.output_tokens, numeric: true },
  { key: "cache_creation_tokens", label: "缓存写入", render: (row) => formatInt(row.cache_creation_tokens), value: (row) => row.cache_creation_tokens, numeric: true },
  { key: "cache_read_tokens", label: "缓存读取", render: (row) => formatInt(row.cache_read_tokens), value: (row) => row.cache_read_tokens, numeric: true },
  { key: "avg_output_tps", label: "平均 TPS", render: (row) => formatTPS(row.avg_output_tps), value: (row) => row.avg_output_tps, numeric: true },
  { key: "cost", label: "成本", render: (row) => formatCost(row.estimated_cost_usd), value: (row) => row.estimated_cost_usd, numeric: true },
  { key: "coverage", label: "覆盖率", render: (row) => formatPercent(row.pricing?.token_coverage_ratio ?? row.pricing?.coverage_ratio), value: (row) => row.pricing?.token_coverage_ratio ?? row.pricing?.coverage_ratio, numeric: true },
];

const eventColumns: Array<DataTableColumn<EventItem>> = [
  { key: "timestamp", label: "时间", render: (row) => formatDate(row.timestamp), value: (row) => row.timestamp ?? "" },
  { key: "channel", label: "Channel", render: (row) => row.channel, value: (row) => row.channel },
  { key: "model", label: "模型", render: (row) => row.model_normalized ?? row.model_raw ?? "-", value: (row) => row.model_normalized ?? row.model_raw ?? "" },
  { key: "session", label: "会话", render: (row) => <span className="mono">{shortHash(row.session_id)}</span>, value: (row) => row.session_id ?? "" },
  { key: "total_tokens", label: "Tokens", render: (row) => formatInt(row.total_tokens), value: (row) => row.total_tokens, numeric: true },
  { key: "cache_creation_tokens", label: "缓存写入", render: (row) => formatInt(row.cache_creation_tokens), value: (row) => row.cache_creation_tokens, numeric: true },
  { key: "cache_read_tokens", label: "缓存读取", render: (row) => formatInt(row.cache_read_tokens), value: (row) => row.cache_read_tokens, numeric: true },
  { key: "output_tps", label: "TPS", render: (row) => formatTPS(row.output_tps), value: (row) => row.output_tps, numeric: true },
  { key: "ttft_ms", label: "TTFT", render: (row) => formatMs(row.ttft_ms), value: (row) => row.ttft_ms, numeric: true },
  { key: "dedupe_strategy", label: "去重策略", render: (row) => row.dedupe_strategy, value: (row) => row.dedupe_strategy },
];

export function SessionsPage() {
  const [eventLimit, setEventLimit] = useState(100);
  const { data: sessions } = useSessions(50);
  const { data: events } = useEvents(eventLimit);

  return (
    <div className="page-stack">
      <section className="panel">
        <h2>Top 会话</h2>
        <DataTable rows={sessions ?? []} columns={sessionColumns} rowKey={(row) => row.label} emptyText="暂无会话数据" defaultSortKey="total_tokens" />
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
        <DataTable rows={events ?? []} columns={eventColumns} rowKey={(row) => row.event_id} emptyText="暂无事件数据" defaultSortKey="timestamp" initialLimit={100} />
      </section>
    </div>
  );
}

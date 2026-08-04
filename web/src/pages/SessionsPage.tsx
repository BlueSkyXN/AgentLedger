import { useEffect, useState } from "react";

import type { EventItem, Paginated, SessionItem } from "@/api/types";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import { useFilterContext } from "@/hooks/filters";
import { useEvents, useSessions } from "@/hooks/queries";
import { formatCost, formatDate, formatInt, shortHash } from "@/utils/format";

const PAGE_LIMITS = [25, 50, 100, 200];

function estimatedCostLabel(row: SessionItem): string {
  const hasCoverage = row.priced_events > 0 || row.unpriced_events > 0 || row.policy_zero_events > 0;
  if (row.estimated_cost_usd == null || (row.event_count > 0 && !hasCoverage) || (row.priced_events === 0 && row.unpriced_events > 0)) return "不可用";
  return formatCost(row.estimated_cost_usd);
}

const sessionColumns: Array<DataTableColumn<SessionItem>> = [
  { key: "session_key", label: "会话", render: (row) => <span className="mono">{shortHash(row.session_id ?? row.session_key)}</span>, value: (row) => row.session_key },
  { key: "first_date", label: "开始", render: (row) => formatDate(row.first_date), value: (row) => row.first_date ?? "" },
  { key: "last_date", label: "结束", render: (row) => formatDate(row.last_date), value: (row) => row.last_date ?? "" },
  { key: "channel", label: "Channel", render: (row) => row.channel || "-", value: (row) => row.channel },
  { key: "source_product", label: "来源", render: (row) => row.source_product || "-", value: (row) => row.source_product ?? "" },
  { key: "primary_model", label: "主模型", render: (row) => row.primary_model || "-", value: (row) => row.primary_model ?? "" },
  { key: "model_count", label: "模型数", render: (row) => formatInt(row.model_count), value: (row) => row.model_count, numeric: true },
  { key: "event_count", label: "事件", render: (row) => formatInt(row.event_count), value: (row) => row.event_count, numeric: true },
  { key: "total_tokens", label: "Tokens", render: (row) => formatInt(row.total_tokens), value: (row) => row.total_tokens, numeric: true },
  { key: "input_tokens", label: "输入", render: (row) => formatInt(row.input_tokens), value: (row) => row.input_tokens, numeric: true },
  { key: "output_tokens", label: "输出", render: (row) => formatInt(row.output_tokens), value: (row) => row.output_tokens, numeric: true },
  { key: "cache_creation_tokens", label: "缓存写入", render: (row) => formatInt(row.cache_creation_tokens), value: (row) => row.cache_creation_tokens, numeric: true },
  { key: "cache_read_tokens", label: "缓存读取", render: (row) => formatInt(row.cache_read_tokens), value: (row) => row.cache_read_tokens, numeric: true },
  { key: "reasoning_tokens", label: "推理", render: (row) => formatInt(row.reasoning_tokens), value: (row) => row.reasoning_tokens, numeric: true },
  { key: "estimated_cost_usd", label: "估算成本", render: estimatedCostLabel, value: (row) => row.estimated_cost_usd, numeric: true },
  { key: "priced_events", label: "已计价", render: (row) => formatInt(row.priced_events), value: (row) => row.priced_events, numeric: true },
  { key: "unpriced_events", label: "缺价", render: (row) => formatInt(row.unpriced_events), value: (row) => row.unpriced_events, numeric: true },
  { key: "policy_zero_events", label: "零值政策", render: (row) => formatInt(row.policy_zero_events), value: (row) => row.policy_zero_events, numeric: true },
];

const eventColumns: Array<DataTableColumn<EventItem>> = [
  { key: "timestamp", label: "时间", render: (row) => formatDate(row.timestamp), value: (row) => row.timestamp ?? "" },
  { key: "channel", label: "Channel", render: (row) => row.channel, value: (row) => row.channel },
  { key: "source_product", label: "来源", render: (row) => row.source_product || "-", value: (row) => row.source_product ?? "" },
  { key: "model", label: "模型", render: (row) => row.model_normalized ?? row.model_raw ?? "-", value: (row) => row.model_normalized ?? row.model_raw ?? "" },
  { key: "session", label: "会话", render: (row) => <span className="mono">{shortHash(row.session_key)}</span>, value: (row) => row.session_key ?? "" },
  { key: "total_tokens", label: "Tokens", render: (row) => formatInt(row.total_tokens), value: (row) => row.total_tokens, numeric: true },
  { key: "input_tokens", label: "输入", render: (row) => formatInt(row.input_tokens), value: (row) => row.input_tokens, numeric: true },
  { key: "output_tokens", label: "输出", render: (row) => formatInt(row.output_tokens), value: (row) => row.output_tokens, numeric: true },
  { key: "cache_creation_tokens", label: "缓存写入", render: (row) => formatInt(row.cache_creation_tokens), value: (row) => row.cache_creation_tokens, numeric: true },
  { key: "cache_read_tokens", label: "缓存读取", render: (row) => formatInt(row.cache_read_tokens), value: (row) => row.cache_read_tokens, numeric: true },
  { key: "reasoning_tokens", label: "推理", render: (row) => formatInt(row.reasoning_tokens), value: (row) => row.reasoning_tokens, numeric: true },
  { key: "identity_strategy", label: "身份策略", render: (row) => row.identity_strategy, value: (row) => row.identity_strategy },
];

function PageNavigation({ page, onPageChange }: { page: Paginated<unknown>; onPageChange: (offset: number) => void }) {
  const currentPage = page.limit > 0 ? Math.floor(page.offset / page.limit) + 1 : 1;
  const totalPages = page.limit > 0 ? Math.max(1, Math.ceil(page.total / page.limit)) : 1;
  return (
    <div className="pagination" aria-label="分页">
      <span>{formatInt(page.total)} 条，共 {currentPage} / {totalPages} 页</span>
      <div>
        <button type="button" disabled={currentPage <= 1} onClick={() => onPageChange(Math.max(0, page.offset - page.limit))}>上一页</button>
        <button type="button" disabled={currentPage >= totalPages} onClick={() => onPageChange(page.offset + page.limit)}>下一页</button>
      </div>
    </div>
  );
}

export function SessionsPage() {
  const [sessionLimit, setSessionLimit] = useState(50);
  const [sessionOffset, setSessionOffset] = useState(0);
  const [eventLimit, setEventLimit] = useState(100);
  const [eventOffset, setEventOffset] = useState(0);
  const { filters } = useFilterContext();
  const { data: sessions } = useSessions(sessionLimit, sessionOffset);
  const { data: events } = useEvents(eventLimit, eventOffset);

  useEffect(() => {
    setSessionOffset(0);
    setEventOffset(0);
  }, [eventLimit, sessionLimit, filters]);

  return (
    <div className="page-stack">
      <section className="panel">
        <header className="panel-heading">
          <div>
            <h2>会话统计</h2>
            <p className="panel-subtitle">每行是一个会话聚合，估算成本按当前模型价格即时计算；缺价与零值政策单独披露。</p>
          </div>
          <label className="select-label">每页<select value={sessionLimit} onChange={(event) => { setSessionLimit(Number(event.target.value)); setSessionOffset(0); }}>{PAGE_LIMITS.map((value) => <option key={value} value={value}>{value}</option>)}</select></label>
        </header>
        <DataTable rows={sessions?.items ?? []} columns={sessionColumns} rowKey={(row) => row.session_key} emptyText="暂无会话数据" defaultSortKey="total_tokens" initialLimit={0} />
        {sessions ? <PageNavigation page={sessions} onPageChange={setSessionOffset} /> : null}
      </section>
      <section className="panel">
        <header className="panel-heading">
          <div>
            <h2>事件明细</h2>
            <p className="panel-subtitle">事件数据按服务端分页返回，当前页面只读展示结构化 token facts。</p>
          </div>
          <label className="select-label">每页<select value={eventLimit} onChange={(event) => { setEventLimit(Number(event.target.value)); setEventOffset(0); }}>{PAGE_LIMITS.map((value) => <option key={value} value={value}>{value}</option>)}</select></label>
        </header>
        <DataTable rows={events?.items ?? []} columns={eventColumns} rowKey={(row) => row.event_id} emptyText="暂无事件数据" defaultSortKey="timestamp" initialLimit={0} />
        {events ? <PageNavigation page={events} onPageChange={setEventOffset} /> : null}
      </section>
    </div>
  );
}

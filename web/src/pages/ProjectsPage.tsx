import type { MetricRow } from "@/api/types";
import { DataTable, type DataTableColumn } from "@/components/DataTable";
import { useBreakdown } from "@/hooks/queries";
import { formatInt } from "@/utils/format";

const projectColumns: Array<DataTableColumn<MetricRow>> = [
  { key: "project", label: "项目", render: (row) => row.label || "-", value: (row) => row.label },
  { key: "events", label: "事件", render: (row) => formatInt(row.events), value: (row) => row.events, numeric: true },
  { key: "total_tokens", label: "Tokens", render: (row) => formatInt(row.total_tokens), value: (row) => row.total_tokens, numeric: true },
  { key: "input_tokens", label: "输入", render: (row) => formatInt(row.input_tokens), value: (row) => row.input_tokens, numeric: true },
  { key: "output_tokens", label: "输出", render: (row) => formatInt(row.output_tokens), value: (row) => row.output_tokens, numeric: true },
  { key: "cache_creation_tokens", label: "缓存写入", render: (row) => formatInt(row.cache_creation_tokens), value: (row) => row.cache_creation_tokens, numeric: true },
  { key: "cache_read_tokens", label: "缓存读取", render: (row) => formatInt(row.cache_read_tokens), value: (row) => row.cache_read_tokens, numeric: true },
  { key: "reasoning_tokens", label: "推理", render: (row) => formatInt(row.reasoning_tokens), value: (row) => row.reasoning_tokens, numeric: true },
];

export function ProjectsPage() {
  const { data: projects } = useBreakdown("project");
  return (
    <div className="page-stack">
      <section className="panel">
        <h2>项目用量</h2>
        <p className="panel-subtitle">按本地项目标签汇总结构化 usage facts，不展示 raw usage 内容。</p>
        <DataTable rows={projects ?? []} columns={projectColumns} rowKey={(row) => row.label} emptyText="暂无项目数据" defaultSortKey="total_tokens" />
      </section>
    </div>
  );
}

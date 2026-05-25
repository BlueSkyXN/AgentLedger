import { useImportRuns, useStatus } from "@/hooks/queries";
import { formatDate, formatInt } from "@/utils/format";

export function ImportsPage() {
  const { data: runs } = useImportRuns(50);
  const { data: status } = useStatus();
  return (
    <div className="page-stack">
      <section className="kpi-grid small">
        <article className="kpi-card"><span>导入运行</span><strong>{formatInt(status?.total_import_runs)}</strong></article>
        <article className="kpi-card"><span>已跟踪源文件</span><strong>{formatInt(status?.total_source_files)}</strong><small>预留表，常见为空</small></article>
      </section>
      <section className="panel">
        <h2>最近导入记录</h2>
        <div className="table-wrap">
          <table>
            <thead><tr><th>运行 ID</th><th>状态</th><th>文件</th><th>新增</th><th>跳过</th><th>开始</th><th>结束</th><th>错误</th></tr></thead>
            <tbody>
              {(runs ?? []).map((run) => <tr key={run.id}><td className="mono">{run.id}</td><td>{run.status}</td><td>{formatInt(run.files_scanned)}</td><td>{formatInt(run.events_added)}</td><td>{formatInt(run.events_skipped)}</td><td>{formatDate(run.started_at)}</td><td>{formatDate(run.finished_at)}</td><td>{run.error ?? "-"}</td></tr>)}
              {(runs ?? []).length === 0 && <tr><td colSpan={8} className="empty-cell">暂无导入记录</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

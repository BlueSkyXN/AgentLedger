import { useMemo } from "react";

import { Chart } from "@/components/Chart";
import { KpiCard } from "@/components/KpiCard";
import { useBreakdown, useSummary, useTimeseries } from "@/hooks/queries";
import { formatCost, formatDate, formatInt, formatMs, formatTPS } from "@/utils/format";

export function OverviewPage() {
  const { data: summary } = useSummary();
  const { data: daily } = useTimeseries("daily");
  const { data: channels } = useBreakdown("channel");

  const dailyOption = useMemo(() => {
    const rows = daily ?? [];
    return {
      tooltip: { trigger: "axis" },
      xAxis: { type: "category", data: rows.map((row) => row.label) },
      yAxis: { type: "value" },
      series: [{ name: "Tokens", type: "line", smooth: true, areaStyle: {}, data: rows.map((row) => row.total_tokens) }],
    };
  }, [daily]);

  const channelOption = useMemo(() => {
    const rows = channels ?? [];
    return {
      tooltip: { trigger: "item" },
      legend: { orient: "vertical", right: 0, top: 12 },
      series: [{ name: "Channel", type: "pie", radius: ["44%", "72%"], data: rows.map((row) => ({ name: row.label, value: row.total_tokens })) }],
    };
  }, [channels]);

  return (
    <div className="page-stack">
      <section className="kpi-grid">
        <KpiCard label="事件数" value={formatInt(summary?.total_events)} hint={`${formatInt(summary?.import_runs)} 次导入`} />
        <KpiCard label="总 Tokens" value={formatInt(summary?.total_tokens)} />
        <KpiCard label="输入 Tokens" value={formatInt(summary?.input_tokens)} />
        <KpiCard label="输出 Tokens" value={formatInt(summary?.output_tokens)} />
        <KpiCard label="推理 Tokens" value={formatInt(summary?.reasoning_tokens)} />
        <KpiCard label="缓存写入" value={formatInt(summary?.cache_creation_tokens)} />
        <KpiCard label="缓存读取" value={formatInt(summary?.cache_read_tokens)} />
        <KpiCard label="平均耗时" value={formatMs(summary?.avg_total_duration_ms)} />
        <KpiCard label="平均 TTFT" value={formatMs(summary?.avg_ttft_ms)} />
        <KpiCard label="平均输出 TPS" value={formatTPS(summary?.avg_output_tps)} />
        <KpiCard label="记录成本" value={formatCost(summary?.recorded_cost_usd)} />
      </section>
      <section className="panel split">
        <div>
          <h2>每日 Tokens 趋势</h2>
          <Chart option={dailyOption} />
        </div>
        <div>
          <h2>Channel 占比</h2>
          <Chart option={channelOption} />
        </div>
      </section>
      <section className="panel meta-row">
        <span>第一条事件：{formatDate(summary?.first_event_at)}</span>
        <span>最后事件：{formatDate(summary?.last_event_at)}</span>
      </section>
    </div>
  );
}

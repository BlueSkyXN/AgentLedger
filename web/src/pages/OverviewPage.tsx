import { useMemo } from "react";

import type { MetricRow } from "@/api/types";
import { Chart } from "@/components/Chart";
import { KpiCard } from "@/components/KpiCard";
import { useBreakdown, useSummary, useTimeseries } from "@/hooks/queries";
import { formatDate, formatInt, formatPercent } from "@/utils/format";

const piePalette = ["#2563eb", "#0f9f6e", "#f59e0b", "#e11d48", "#7c3aed", "#94a3b8"];

function topFivePieData(rows: MetricRow[]) {
  const sorted = rows
    .map((row) => ({ name: row.label, value: row.total_tokens }))
    .filter((row) => row.value > 0)
    .sort((a, b) => b.value - a.value);
  const top = sorted.slice(0, 5);
  const others = sorted.slice(5).reduce((total, row) => total + row.value, 0);
  if (others > 0) top.push({ name: "其他", value: others });
  return top;
}

export function OverviewPage() {
  const { data: summary } = useSummary();
  const { data: daily } = useTimeseries("daily");
  const { data: channels } = useBreakdown("channel");
  const { data: models } = useBreakdown("model");
  const inputSideTokens = summary == null ? undefined : summary.input_tokens + summary.cache_creation_tokens + summary.cache_read_tokens;
  const cacheRate = inputSideTokens && inputSideTokens > 0 && summary != null ? summary.cache_read_tokens / inputSideTokens : undefined;
  const modelPieRows = useMemo(() => topFivePieData(models ?? []), [models]);
  const modelPieTotal = modelPieRows.reduce((total, row) => total + row.value, 0);

  const dailyOption = useMemo(() => {
    const rows = daily ?? [];
    return {
      tooltip: { trigger: "axis", valueFormatter: (value: number) => `${formatInt(Math.round(value * 1_000_000))} tokens` },
      xAxis: { type: "category", data: rows.map((row) => row.label) },
      yAxis: { type: "value", name: "M tokens", axisLabel: { formatter: "{value}M" } },
      grid: { left: 54, right: 16, top: 36, bottom: 36 },
      series: [{ name: "总 Tokens", type: "line", smooth: true, areaStyle: {}, data: rows.map((row) => Number((row.total_tokens / 1_000_000).toFixed(2))) }],
    };
  }, [daily]);

  const channelOption = useMemo(() => ({
    tooltip: { trigger: "item" },
    legend: { orient: "vertical", right: 0, top: 12 },
    series: [{ name: "Channel", type: "pie", radius: ["44%", "72%"], data: (channels ?? []).map((row) => ({ name: row.label, value: row.total_tokens })) }],
  }), [channels]);

  const modelOption = useMemo(() => ({
    tooltip: { trigger: "item" },
    color: piePalette,
    series: [{ name: "Model", type: "pie", radius: ["48%", "74%"], center: ["50%", "52%"], label: { show: false }, labelLine: { show: false }, data: modelPieRows }],
  }), [modelPieRows]);

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
        <KpiCard label="缓存率" value={formatPercent(cacheRate)} hint={`${formatInt(summary?.cache_read_tokens)} / ${formatInt(inputSideTokens)} 输入侧 tokens`} />
      </section>
      <section className="panel chart-grid">
        <div>
          <h2>每日总 Tokens 趋势</h2>
          <Chart option={dailyOption} />
        </div>
        <div>
          <h2>Channel 占比</h2>
          <Chart option={channelOption} />
        </div>
        <div>
          <h2>模型占比 Top 5</h2>
          <Chart option={modelOption} />
          <div className="pie-legend-list">
            {modelPieRows.map((row, index) => (
              <span key={row.name} className="pie-legend-item">
                <i style={{ background: piePalette[index % piePalette.length] }} />
                {row.name}
                <strong>{formatPercent(modelPieTotal > 0 ? row.value / modelPieTotal : undefined)}</strong>
              </span>
            ))}
          </div>
        </div>
      </section>
      <section className="panel meta-row">
        <span>第一条事件：{formatDate(summary?.first_date)}</span>
        <span>最后事件：{formatDate(summary?.last_date)}</span>
      </section>
    </div>
  );
}

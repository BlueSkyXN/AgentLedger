import { useMemo } from "react";

import type { MetricRow } from "@/api/types";
import { Chart } from "@/components/Chart";
import { KpiCard } from "@/components/KpiCard";
import { useBreakdown, useSummary, useTimeseries } from "@/hooks/queries";
import { formatCost, formatDate, formatInt, formatPercent } from "@/utils/format";

const piePalette = ["#2563eb", "#0f9f6e", "#f59e0b", "#e11d48", "#7c3aed", "#94a3b8"];

function estimatedCostLabel(summary: { estimated_cost_usd: number | null; pricing?: { status: string } } | undefined): string {
  if (summary == null) return "-";
  const status = summary?.pricing?.status?.toLowerCase();
  if (summary?.estimated_cost_usd == null || status !== "available") return "不可用";
  return formatCost(summary.estimated_cost_usd);
}

function ChartState({ message, error = false, onRetry }: { message: string; error?: boolean; onRetry?: () => void }) {
  return (
    <div className={`chart-state${error ? " error" : ""}`}>
      <span>{message}</span>
      {onRetry ? <button type="button" className="ghost-button" onClick={onRetry}>重试</button> : null}
    </div>
  );
}

function topFivePieData(rows: MetricRow[], valueOf: (row: MetricRow) => number | null | undefined) {
  const sorted = rows
    .map((row) => ({ name: row.label, value: valueOf(row) }))
    .filter((row): row is { name: string; value: number } => row.value != null && row.value > 0)
    .sort((a, b) => b.value - a.value);
  const top = sorted.slice(0, 5);
  const others = sorted.slice(5).reduce((total, row) => total + row.value, 0);
  if (others > 0) top.push({ name: "其他", value: others });
  return top;
}

export function OverviewPage() {
  const { data: summary, isPending: summaryPending, isError: summaryIsError, refetch: refetchSummary } = useSummary();
  const { data: daily, isPending: dailyPending, isError: dailyIsError, refetch: refetchDaily } = useTimeseries("daily");
  const { data: channels, isPending: channelsPending, isError: channelsIsError, refetch: refetchChannels } = useBreakdown("channel");
  const { data: models, isPending: modelsPending, isError: modelsIsError, refetch: refetchModels } = useBreakdown("model");
  const inputSideTokens = summary == null ? undefined : summary.input_tokens + summary.cache_creation_tokens + summary.cache_read_tokens;
  const cacheRate = inputSideTokens && inputSideTokens > 0 && summary != null ? summary.cache_read_tokens / inputSideTokens : undefined;
  const modelPieRows = useMemo(() => topFivePieData(models ?? [], (row) => row.total_tokens), [models]);
  const modelCostPieRows = useMemo(() => topFivePieData(models ?? [], (row) => row.estimated_cost_usd), [models]);
  const modelPieTotal = modelPieRows.reduce((total, row) => total + row.value, 0);
  const modelCostPieTotal = modelCostPieRows.reduce((total, row) => total + row.value, 0);

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

  const modelCostOption = useMemo(() => ({
    tooltip: { trigger: "item" },
    color: piePalette,
    series: [{ name: "模型成本", type: "pie", radius: ["48%", "74%"], center: ["50%", "52%"], label: { show: false }, labelLine: { show: false }, data: modelCostPieRows }],
  }), [modelCostPieRows]);

  const pricing = summary?.pricing;
  const dailyContent = dailyPending
    ? <ChartState message="每日趋势加载中…" />
    : dailyIsError
      ? <ChartState message="每日趋势加载失败，请检查服务状态后重试。" error onRetry={() => void refetchDaily()} />
      : daily == null || daily.length === 0
        ? <ChartState message="当前筛选范围暂无每日趋势数据。" />
        : <Chart option={dailyOption} />;
  const channelContent = channelsPending
    ? <ChartState message="Channel 占比加载中…" />
    : channelsIsError
      ? <ChartState message="Channel 占比加载失败。" error onRetry={() => void refetchChannels()} />
      : channels == null || channels.length === 0
        ? <ChartState message="当前筛选范围暂无 Channel 数据。" />
        : <Chart option={channelOption} />;
  const modelContent = modelsPending
    ? <ChartState message="模型占比加载中…" />
    : modelsIsError
      ? <ChartState message="模型占比加载失败。" error onRetry={() => void refetchModels()} />
      : modelPieRows.length === 0
        ? <ChartState message="当前筛选范围暂无模型数据。" />
        : <Chart option={modelOption} />;
  const modelCostContent = modelsPending
    ? <ChartState message="模型成本占比加载中…" />
    : modelsIsError
      ? <ChartState message="模型成本占比加载失败。" error onRetry={() => void refetchModels()} />
      : modelCostPieRows.length === 0
        ? <ChartState message="当前筛选范围暂无可计价的模型成本数据。" />
        : <Chart option={modelCostOption} />;

  return (
    <div className="page-stack">
      <section className="kpi-grid">
        <KpiCard label="事件数" value={formatInt(summary?.total_events)} hint={`${formatInt(summary?.import_runs)} 次导入`} />
        <KpiCard label="会话数" value={formatInt(summary?.total_sessions)} />
        <KpiCard label="总 Tokens" value={formatInt(summary?.total_tokens)} />
        <KpiCard label="输入 Tokens" value={formatInt(summary?.input_tokens)} />
        <KpiCard label="输出 Tokens" value={formatInt(summary?.output_tokens)} />
        <KpiCard label="推理 Tokens" value={formatInt(summary?.reasoning_tokens)} />
        <KpiCard label="缓存写入" value={formatInt(summary?.cache_creation_tokens)} />
        <KpiCard label="缓存读取" value={formatInt(summary?.cache_read_tokens)} />
        <KpiCard label="估算成本" value={estimatedCostLabel(summary)} hint={summary == null ? "加载中" : summary.pricing?.status === "available" ? "当前 profile" : summary.pricing?.error_code ?? "计价不可用"} />
        <KpiCard label="缓存率" value={formatPercent(cacheRate)} hint={`${formatInt(summary?.cache_read_tokens)} / ${formatInt(inputSideTokens)} 输入侧 tokens`} />
      </section>
      <section className="panel chart-grid">
        <div>
          <h2>每日总 Tokens 趋势</h2>
          {dailyContent}
        </div>
        <div>
          <h2>Channel 占比</h2>
          {channelContent}
        </div>
        <div>
          <h2>模型占比 Top 5</h2>
          {modelContent}
          {modelPieRows.length > 0 ? <div className="pie-legend-list">
            {modelPieRows.map((row, index) => (
              <span key={row.name} className="pie-legend-item">
                <i style={{ background: piePalette[index % piePalette.length] }} />
                {row.name}
                <strong>{formatPercent(modelPieTotal > 0 ? row.value / modelPieTotal : undefined)}</strong>
              </span>
            ))}
          </div> : null}
        </div>
        <div>
          <h2>模型成本占比 Top 5</h2>
          {modelCostContent}
          {modelCostPieRows.length > 0 ? <div className="pie-legend-list">
            {modelCostPieRows.map((row, index) => (
              <span key={row.name} className="pie-legend-item">
                <i style={{ background: piePalette[index % piePalette.length] }} />
                {row.name}
                <strong>{formatPercent(modelCostPieTotal > 0 ? row.value / modelCostPieTotal : undefined)}</strong>
              </span>
            ))}
          </div> : null}
          <p className="chart-note">仅统计当前 pricing profile 可估算的金额；未计价部分不进入占比。</p>
        </div>
      </section>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>计价覆盖</h2>
            <p className="panel-subtitle">按当前筛选范围统计成本估算覆盖情况；政策零值事件不计入成本。</p>
          </div>
          <span className={`status-pill ${summaryPending ? "" : pricing?.status === "available" ? "success" : "danger"}`}>
            {summaryPending ? "加载中" : pricing?.status === "available" ? "可用" : pricing?.error_code ?? "不可用"}
          </span>
        </div>
        {summaryIsError ? (
          <div className="panel-state error">
            <span>计价覆盖加载失败。</span>
            <button type="button" className="ghost-button" onClick={() => void refetchSummary()}>重试</button>
          </div>
        ) : summaryPending ? (
          <p className="panel-subtitle">计价覆盖加载中…</p>
        ) : pricing?.status === "available" ? (
          <>
            <div className="pricing-summary-grid">
              <div><span>已计价事件</span><strong>{formatInt(pricing.priced_events)}</strong></div>
              <div><span>未计价事件</span><strong>{formatInt(pricing.unpriced_events)}</strong></div>
              <div><span>政策零值事件</span><strong>{formatInt(pricing.policy_zero_events)}</strong></div>
              <div><span>已计价 Tokens</span><strong>{formatInt(pricing.priced_tokens)}</strong></div>
              <div><span>未计价 Tokens</span><strong>{formatInt(pricing.unpriced_tokens)}</strong></div>
              <div><span>政策零值 Tokens</span><strong>{formatInt(pricing.policy_zero_tokens)}</strong></div>
            </div>
            <div className="meta-row coverage-meta">
              <span>事件覆盖：{formatPercent(pricing.event_coverage_ratio)}</span>
              <span>Token 覆盖：{formatPercent(pricing.token_coverage_ratio)}</span>
              {pricing.profile_id && <span>Profile：{pricing.profile_id}</span>}
            </div>
          </>
        ) : (
          <p className="panel-subtitle">当前 pricing profile 不可用，无法提供覆盖统计。</p>
        )}
      </section>
      <section className="panel meta-row">
        <span>第一条事件：{formatDate(summary?.first_date)}</span>
        <span>最后事件：{formatDate(summary?.last_date)}</span>
      </section>
    </div>
  );
}

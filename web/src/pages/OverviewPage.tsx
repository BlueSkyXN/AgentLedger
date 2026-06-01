import { useMemo } from "react";

import type { MetricRow } from "@/api/types";
import { Chart } from "@/components/Chart";
import { KpiCard } from "@/components/KpiCard";
import { useBreakdown, useSummary, useTimeseries } from "@/hooks/queries";
import { formatCost, formatDate, formatInt, formatMs, formatPercent, formatTPS } from "@/utils/format";

const piePalette = ["#2563eb", "#0f9f6e", "#f59e0b", "#e11d48", "#7c3aed", "#94a3b8"];

function topFivePieData(rows: MetricRow[], valueOf: (row: MetricRow) => number | null | undefined) {
  const sorted = rows
    .map((row) => ({ name: row.label, value: valueOf(row) ?? 0 }))
    .filter((row) => row.value > 0)
    .sort((a, b) => b.value - a.value);
  const top = sorted.slice(0, 5);
  const others = sorted.slice(5).reduce((total, row) => total + row.value, 0);
  if (others > 0) {
    top.push({ name: "Others", value: others });
  }
  return top;
}

export function OverviewPage() {
  const { data: summary } = useSummary();
  const { data: daily } = useTimeseries("daily");
  const { data: channels } = useBreakdown("channel");
  const { data: models } = useBreakdown("model");
  const missingPricingModels = useMemo(() => {
    const rows = summary?.pricing?.missing_models ?? [];
    return [...rows].sort((a, b) => b.tokens - a.tokens);
  }, [summary?.pricing?.missing_models]);
  const missingPricingPreview = missingPricingModels.slice(0, 10);
  const missingPricingTokens = missingPricingModels.reduce((total, row) => total + row.tokens, 0);
  const missingPricingEvents = missingPricingModels.reduce((total, row) => total + row.events, 0);
  const hasMissingPricing = missingPricingModels.length > 0;
  const inputSideTokens = summary == null ? undefined : summary.input_tokens + summary.cache_creation_tokens + summary.cache_read_tokens;
  const cacheReadTokens = summary?.cache_read_tokens;
  const cacheRate = inputSideTokens && inputSideTokens > 0 && cacheReadTokens != null ? cacheReadTokens / inputSideTokens : undefined;
  const modelPieRows = useMemo(() => topFivePieData(models ?? [], (row) => row.total_tokens), [models]);
  const modelCostPieRows = useMemo(() => topFivePieData(models ?? [], (row) => row.estimated_cost_usd), [models]);
  const modelPieTotal = modelPieRows.reduce((total, row) => total + row.value, 0);
  const modelCostPieTotal = modelCostPieRows.reduce((total, row) => total + row.value, 0);

  const dailyOption = useMemo(() => {
    const rows = daily ?? [];
    return {
      tooltip: {
        trigger: "axis",
        valueFormatter: (value: number) => `${formatInt(Math.round(value * 1_000_000))} tokens`,
      },
      xAxis: { type: "category", data: rows.map((row) => row.label) },
      yAxis: {
        type: "value",
        name: "M tokens",
        axisLabel: { formatter: "{value}M" },
      },
      grid: { left: 54, right: 16, top: 36, bottom: 36 },
      series: [{ name: "总 Tokens", type: "line", smooth: true, areaStyle: {}, data: rows.map((row) => Number((row.total_tokens / 1_000_000).toFixed(2))) }],
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

  const modelOption = useMemo(() => {
    return {
      tooltip: { trigger: "item" },
      color: piePalette,
      series: [{
        name: "Model",
        type: "pie",
        radius: ["48%", "74%"],
        center: ["50%", "52%"],
        label: { show: false },
        labelLine: { show: false },
        data: modelPieRows,
      }],
    };
  }, [modelPieRows]);

  const modelCostOption = useMemo(() => {
    return {
      tooltip: { trigger: "item" },
      color: piePalette,
      series: [{
        name: "Model Cost",
        type: "pie",
        radius: ["48%", "74%"],
        center: ["50%", "52%"],
        label: { show: false },
        labelLine: { show: false },
        data: modelCostPieRows,
      }],
    };
  }, [modelCostPieRows]);

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
        <KpiCard label="成本" value={formatCost(summary?.estimated_cost_usd)} hint={summary?.pricing?.profile_id ?? "pricing.v1"} />
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
        <div>
          <h2>模型成本占比 Top 5</h2>
          <Chart option={modelCostOption} />
          <div className="pie-legend-list">
            {modelCostPieRows.map((row, index) => (
              <span key={row.name} className="pie-legend-item">
                <i style={{ background: piePalette[index % piePalette.length] }} />
                {row.name}
                <strong>{formatPercent(modelCostPieTotal > 0 ? row.value / modelCostPieTotal : undefined)}</strong>
              </span>
            ))}
          </div>
        </div>
      </section>
      <section className="panel pricing-panel">
        <div className="panel-heading">
          <div>
            <h2>计价缺口</h2>
            <p className="panel-subtitle">
              成本按模型官网正价对 token buckets 聚合计算，不写入数据库；普通模型缓存创建按输入价，Claude 缓存创建按 5 分钟写入价，推理 token 不单独重复计费。
            </p>
          </div>
          <span className={`status-pill ${hasMissingPricing ? "danger" : "success"}`}>
            {hasMissingPricing ? "存在缺价模型" : "计价完整"}
          </span>
        </div>
        <div className="pricing-summary-grid">
          <div>
            <span>Pricing profile</span>
            <strong>{summary?.pricing?.profile_id ?? "pricing.v1"}</strong>
          </div>
          <div>
            <span>缺价模型</span>
            <strong>{formatInt(missingPricingModels.length)}</strong>
          </div>
          <div>
            <span>缺价事件</span>
            <strong>{formatInt(missingPricingEvents)}</strong>
          </div>
          <div>
            <span>缺价 Tokens</span>
            <strong>{formatInt(missingPricingTokens)}</strong>
          </div>
        </div>
        <div className="table-wrap">
          <table className="compact-table">
            <thead>
              <tr>
                <th>Provider</th>
                <th>Channel</th>
                <th>Model</th>
                <th>Events</th>
                <th>Tokens</th>
                <th>Reason</th>
              </tr>
            </thead>
            <tbody>
              {missingPricingPreview.map((row) => (
                <tr key={`${row.provider}:${row.channel}:${row.model}`}>
                  <td>{row.provider || "-"}</td>
                  <td>{row.channel || "-"}</td>
                  <td className="mono">{row.model || "-"}</td>
                  <td>{formatInt(row.events)}</td>
                  <td>{formatInt(row.tokens)}</td>
                  <td>{row.reason}</td>
                </tr>
              ))}
              {!hasMissingPricing ? (
                <tr>
                  <td className="empty-cell" colSpan={6}>当前范围没有缺价模型。</td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
        {hasMissingPricing ? (
          <p className="note">
            这里显示 token 影响最大的前 {formatInt(missingPricingPreview.length)} 项，共 {formatInt(missingPricingModels.length)} 个缺价模型。缺价部分不会被塞进成本，避免假成本。
          </p>
        ) : (
          <p className="note">当前范围的成本已覆盖全部 token。</p>
        )}
      </section>
      <section className="panel meta-row">
        <span>第一条事件：{formatDate(summary?.first_event_at)}</span>
        <span>最后事件：{formatDate(summary?.last_event_at)}</span>
        <span>成本为模型官网正价估算口径，不代表实际账单。</span>
      </section>
    </div>
  );
}

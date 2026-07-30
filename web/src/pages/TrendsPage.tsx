import { useMemo } from "react";

import type { MetricRow } from "@/api/types";
import { Chart } from "@/components/Chart";
import { useTimeseries } from "@/hooks/queries";
import { formatInt, formatTPS } from "@/utils/format";

type TrendTooltipParam = {
  dataIndex: number;
  marker: string;
  seriesName: string;
  value: number | null;
};

function trendOption(rows: MetricRow[]) {
  return {
    tooltip: {
      trigger: "axis",
      formatter: (params: TrendTooltipParam[]) => {
        const index = params[0]?.dataIndex;
        const row = index == null ? undefined : rows[index];
        const values = params.map((param) => `${param.marker}${param.seriesName}：${param.seriesName === "输出 TPS" ? formatTPS(param.value) : formatInt(param.value)}`);
        return [`${row?.label ?? ""}`, ...values].join("<br/>");
      },
    },
    legend: { data: ["事件数", "Tokens", "输出 TPS"] },
    xAxis: { type: "category", data: rows.map((row) => row.label) },
    yAxis: [{ type: "value", name: "事件" }, { type: "value", name: "Tokens" }, { type: "value", name: "TPS" }],
    series: [
      { name: "事件数", type: "bar", data: rows.map((row) => row.events) },
      { name: "Tokens", type: "line", yAxisIndex: 1, smooth: true, data: rows.map((row) => row.total_tokens) },
      { name: "输出 TPS", type: "line", yAxisIndex: 2, smooth: true, data: rows.map((row) => row.avg_output_tps ?? null) },
    ],
  };
}

export function TrendsPage() {
  const { data: daily } = useTimeseries("daily");
  const { data: weekly } = useTimeseries("weekly");
  const { data: monthly } = useTimeseries("monthly");

  const dailyOption = useMemo(() => {
    return trendOption(daily ?? []);
  }, [daily]);

  const weeklyOption = useMemo(() => {
    return trendOption(weekly ?? []);
  }, [weekly]);

  const monthlyOption = useMemo(() => {
    return trendOption(monthly ?? []);
  }, [monthly]);

  return (
    <div className="page-stack">
      <section className="panel">
        <h2>每日事件与 Tokens</h2>
        <Chart option={dailyOption} />
      </section>
      <section className="panel split">
        <div>
          <h2>每周事件与 Tokens</h2>
          <Chart option={weeklyOption} />
        </div>
        <div>
          <h2>每月事件与 Tokens</h2>
          <Chart option={monthlyOption} />
        </div>
      </section>
    </div>
  );
}

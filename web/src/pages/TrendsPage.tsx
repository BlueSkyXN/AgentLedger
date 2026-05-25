import { useMemo } from "react";

import { Chart } from "@/components/Chart";
import { useTimeseries } from "@/hooks/queries";

export function TrendsPage() {
  const { data: daily } = useTimeseries("daily");
  const { data: weekly } = useTimeseries("weekly");
  const { data: monthly } = useTimeseries("monthly");

  const dailyOption = useMemo(() => {
    const rows = daily ?? [];
    return {
      tooltip: { trigger: "axis" },
      legend: { data: ["事件数", "Tokens", "输出 TPS"] },
      xAxis: { type: "category", data: rows.map((row) => row.label) },
      yAxis: [{ type: "value", name: "事件" }, { type: "value", name: "Tokens/TPS" }],
      series: [
        { name: "事件数", type: "bar", data: rows.map((row) => row.events) },
        { name: "Tokens", type: "line", yAxisIndex: 1, smooth: true, data: rows.map((row) => row.total_tokens) },
        { name: "输出 TPS", type: "line", yAxisIndex: 1, smooth: true, data: rows.map((row) => row.avg_output_tps ?? null) },
      ],
    };
  }, [daily]);

  const weeklyOption = useMemo(() => {
    const rows = weekly ?? [];
    return { xAxis: { type: "category", data: rows.map((row) => row.label) }, yAxis: { type: "value" }, series: [{ name: "Tokens", type: "bar", data: rows.map((row) => row.total_tokens) }] };
  }, [weekly]);

  const monthlyOption = useMemo(() => {
    const rows = monthly ?? [];
    return { xAxis: { type: "category", data: rows.map((row) => row.label) }, yAxis: { type: "value" }, series: [{ name: "Tokens", type: "bar", data: rows.map((row) => row.total_tokens) }] };
  }, [monthly]);

  return (
    <div className="page-stack">
      <section className="panel">
        <h2>每日事件与 Tokens</h2>
        <Chart option={dailyOption} />
      </section>
      <section className="panel split">
        <div>
          <h2>每周 Tokens</h2>
          <Chart option={weeklyOption} />
        </div>
        <div>
          <h2>每月 Tokens</h2>
          <Chart option={monthlyOption} />
        </div>
      </section>
    </div>
  );
}

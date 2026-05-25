import { BarChart, LineChart, PieChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { init, use, type EChartsCoreOption, type EChartsType } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useMemo, useRef } from "react";

import { useThemeContext, type ThemeMode } from "@/hooks/theme";

use([BarChart, LineChart, PieChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

type ChartOption = Record<string, unknown>;

type Props = {
  option: ChartOption;
  height?: number;
};

const palettes = {
  light: {
    text: "#172033",
    dim: "#667085",
    panel: "#ffffff",
    line: "#d7dfeb",
    series: ["#2563eb", "#0f9f6e", "#f59e0b", "#e11d48", "#7c3aed", "#0891b2"],
  },
  dark: {
    text: "#edf2f7",
    dim: "#a8b3c7",
    panel: "#151b26",
    line: "#334155",
    series: ["#60a5fa", "#34d399", "#fbbf24", "#fb7185", "#c084fc", "#22d3ee"],
  },
};

function styleAxis(axis: unknown, theme: ThemeMode): unknown {
  const palette = palettes[theme];
  const defaults = {
    axisLabel: { color: palette.dim },
    axisLine: { lineStyle: { color: palette.line } },
    splitLine: { lineStyle: { color: palette.line, opacity: theme === "light" ? 0.65 : 0.28 } },
  };
  if (Array.isArray(axis)) return axis.map((item) => item && typeof item === "object" ? { ...defaults, ...item } : item);
  if (axis && typeof axis === "object") return { ...defaults, ...axis };
  return axis;
}

function buildOption(option: ChartOption, theme: ThemeMode): EChartsCoreOption {
  const palette = palettes[theme];
  const hasAxis = Boolean(option.xAxis || option.yAxis);
  const legend = { textStyle: { color: palette.dim }, top: 4 };
  const result: ChartOption = {
    backgroundColor: "transparent",
    color: palette.series,
    textStyle: { color: palette.dim, fontFamily: "inherit" },
    tooltip: {
      trigger: "axis",
      backgroundColor: palette.panel,
      borderColor: palette.line,
      textStyle: { color: palette.text },
    },
    ...option,
  };
  if (hasAxis) result.grid = { left: 44, right: 18, top: 44, bottom: 38, ...(option.grid as object | undefined) };
  if (option.legend !== undefined) result.legend = typeof option.legend === "object" && !Array.isArray(option.legend) ? { ...legend, ...option.legend } : option.legend;
  if (option.xAxis) result.xAxis = styleAxis(option.xAxis, theme);
  if (option.yAxis) result.yAxis = styleAxis(option.yAxis, theme);
  return result as EChartsCoreOption;
}

export function Chart({ option, height = 320 }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<EChartsType | null>(null);
  const { theme } = useThemeContext();
  const themedOption = useMemo(() => buildOption(option, theme), [option, theme]);

  useEffect(() => {
    if (!containerRef.current) return undefined;
    const chart = init(containerRef.current, undefined, { renderer: "canvas" });
    const observer = new ResizeObserver(() => chart.resize());
    chartRef.current = chart;
    observer.observe(containerRef.current);
    return () => {
      observer.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    chartRef.current?.setOption(themedOption, { notMerge: true, lazyUpdate: true });
  }, [themedOption]);

  return <div ref={containerRef} className="chart" style={{ height }} role="img" aria-label="数据图表" />;
}

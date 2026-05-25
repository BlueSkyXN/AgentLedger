import { useFilterContext, type TimeRange } from "@/hooks/filters";

const ranges: Array<{ value: TimeRange; label: string }> = [
  { value: "all", label: "全部" },
  { value: "7d", label: "近 7 天" },
  { value: "30d", label: "近 30 天" },
  { value: "month", label: "本月" },
  { value: "custom", label: "自定义" },
];

export function FilterBar() {
  const { range, customSince, customUntil, activeSince, activeUntil, setRange, setCustomSince, setCustomUntil, clearFilters } = useFilterContext();
  const rangeText = activeSince || activeUntil ? `${activeSince || "最早"} 至 ${activeUntil || "现在"}` : "全部时间";

  return (
    <div className="filter-bar" aria-label="时间筛选">
      <div className="range-tabs" role="group" aria-label="快捷时间范围">
        {ranges.map((item) => (
          <button key={item.value} type="button" className={range === item.value ? "active" : undefined} onClick={() => setRange(item.value)}>
            {item.label}
          </button>
        ))}
      </div>
      <label>
        <span>开始日期</span>
        <input type="date" value={customSince} onChange={(event) => setCustomSince(event.target.value)} />
      </label>
      <label>
        <span>结束日期</span>
        <input type="date" value={customUntil} onChange={(event) => setCustomUntil(event.target.value)} />
      </label>
      <button type="button" onClick={clearFilters}>重置</button>
      <small>{rangeText}</small>
    </div>
  );
}

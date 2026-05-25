import { useFilterContext, type TimeRange } from "@/hooks/filters";
import { useFilterOptions } from "@/hooks/queries";

const ranges: Array<{ value: TimeRange; label: string }> = [
  { value: "all", label: "全部" },
  { value: "7d", label: "近 7 天" },
  { value: "30d", label: "近 30 天" },
  { value: "month", label: "本月" },
  { value: "custom", label: "自定义" },
];

export function FilterBar() {
  const { data: options } = useFilterOptions();
  const {
    range,
    customSince,
    customUntil,
    activeSince,
    activeUntil,
    channel,
    provider,
    model,
    session,
    setRange,
    setCustomSince,
    setCustomUntil,
    setChannel,
    setProvider,
    setModel,
    setSession,
    clearFilters,
  } = useFilterContext();
  const rangeText = activeSince || activeUntil ? `${activeSince || "最早"} 至 ${activeUntil || "现在"}` : "全部时间";

  return (
    <div className="filter-bar" aria-label="用量筛选">
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
      <label>
        <span>Channel</span>
        <select value={channel} onChange={(event) => setChannel(event.target.value)}>
          <option value="">全部</option>
          {(options?.channels ?? []).map((value) => <option key={value} value={value}>{value}</option>)}
        </select>
      </label>
      <label>
        <span>Provider</span>
        <select value={provider} onChange={(event) => setProvider(event.target.value)}>
          <option value="">全部</option>
          {(options?.providers ?? []).map((value) => <option key={value} value={value}>{value}</option>)}
        </select>
      </label>
      <label>
        <span>Model</span>
        <input list="model-options" value={model} onChange={(event) => setModel(event.target.value)} placeholder="全部模型" />
        <datalist id="model-options">
          {(options?.models ?? []).map((value) => <option key={value} value={value} />)}
        </datalist>
      </label>
      <label>
        <span>Session</span>
        <input list="session-options" value={session} onChange={(event) => setSession(event.target.value)} placeholder="全部会话" />
        <datalist id="session-options">
          {(options?.sessions ?? []).map((value) => <option key={value} value={value} />)}
        </datalist>
      </label>
      <button type="button" onClick={clearFilters}>重置</button>
      <small>{rangeText}</small>
    </div>
  );
}

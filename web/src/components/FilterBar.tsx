import { useMemo, useState } from "react";

import { useFilterContext, type TimeRange } from "@/hooks/filters";
import { useFilterOptions, useSummary } from "@/hooks/queries";
import { formatDate } from "@/utils/format";

const ranges: Array<{ value: TimeRange; label: string }> = [
  { value: "all", label: "全部" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "近 7 天" },
  { value: "30d", label: "近 30 天" },
  { value: "month", label: "本月" },
  { value: "last_month", label: "上月" },
  { value: "custom", label: "自定义" },
];

export function FilterBar() {
  const { data: options } = useFilterOptions();
  const { data: summary } = useSummary();
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
    project,
    setRange,
    setCustomSince,
    setCustomUntil,
    setChannel,
    setProvider,
    setModel,
    setSession,
    setProject,
    clearFilters,
  } = useFilterContext();
  const rangeText = activeSince || activeUntil
    ? `${formatDate(activeSince) || "最早"} 至 ${formatDate(activeUntil) || "现在"}`
    : summary?.first_event_at || summary?.last_event_at
      ? `${formatDate(summary?.first_event_at)} 至 ${formatDate(summary?.last_event_at)}`
      : "全部时间";
  const detailedCount = [channel, provider, model, session, project].filter(Boolean).length;
  const [advancedOpen, setAdvancedOpen] = useState(detailedCount > 0 || range === "custom");
  const chips = useMemo(() => {
    const values: string[] = [];
    if (range !== "all") values.push(rangeText);
    if (channel) values.push(`Channel: ${channel}`);
    if (provider) values.push(`Provider: ${provider}`);
    if (model) values.push(`Model: ${model}`);
    if (session) values.push(`Session: ${session}`);
    if (project) values.push(`Project: ${project}`);
    return values;
  }, [channel, model, project, provider, range, rangeText, session]);

  function chooseRange(value: TimeRange) {
    setRange(value);
    if (value === "custom") setAdvancedOpen(true);
  }

  return (
    <div className="filter-bar" aria-label="用量筛选">
      <div className="filter-main">
        <div className="range-tabs" role="group" aria-label="快捷时间范围">
          {ranges.map((item) => (
            <button key={item.value} type="button" className={range === item.value ? "active" : undefined} onClick={() => chooseRange(item.value)}>
              {item.label}
            </button>
          ))}
        </div>
        <div className="filter-state">
          <strong>{rangeText}</strong>
          <span>{chips.length > 0 ? chips.join(" · ") : "未限定 channel / provider / model / session / project"}</span>
        </div>
        <button type="button" className="ghost-button" onClick={clearFilters}>重置</button>
        <button type="button" className="filter-toggle" onClick={() => setAdvancedOpen((open) => !open)}>
          {advancedOpen ? "收起筛选" : `筛选字段${detailedCount > 0 ? `(${detailedCount})` : ""}`}
        </button>
      </div>
      {advancedOpen ? (
        <div className="filter-grid">
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
          <label>
            <span>Project</span>
            <input list="project-options" value={project} onChange={(event) => setProject(event.target.value)} placeholder="全部项目" />
            <datalist id="project-options">
              {(options?.projects ?? []).map((value) => <option key={value} value={value} />)}
            </datalist>
          </label>
        </div>
      ) : null}
    </div>
  );
}

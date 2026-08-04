import { createContext, ReactNode, useContext, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { Filters } from "@/api/types";

export type TimeRange = "all" | "24h" | "7d" | "30d" | "month" | "last_month" | "custom";

type StoredFilterState = {
  range: TimeRange;
  customSince: string;
  customUntil: string;
  channel: string;
  sourceProduct: string;
  provider: string;
  model: string;
  session: string;
  project: string;
};

type FilterContextValue = {
  filters: Filters;
  range: TimeRange;
  customSince: string;
  customUntil: string;
  activeSince: string;
  activeUntil: string;
  channel: string;
  sourceProduct: string;
  provider: string;
  model: string;
  session: string;
  project: string;
  setRange: (value: TimeRange) => void;
  setCustomSince: (value: string) => void;
  setCustomUntil: (value: string) => void;
  setChannel: (value: string) => void;
  setSourceProduct: (value: string) => void;
  setProvider: (value: string) => void;
  setModel: (value: string) => void;
  setSession: (value: string) => void;
  setProject: (value: string) => void;
  clearFilters: () => void;
};

const STORAGE_KEY = "agent-ledger-filters";
const FilterContext = createContext<FilterContextValue | null>(null);

type CalendarDate = { year: number; month: number; day: number };

function formatCalendarDate(value: CalendarDate): string {
  return `${value.year}-${String(value.month).padStart(2, "0")}-${String(value.day).padStart(2, "0")}`;
}

function calendarDateAt(value: Date, timezone: string): CalendarDate {
  try {
    const options: Intl.DateTimeFormatOptions = { year: "numeric", month: "2-digit", day: "2-digit" };
    if (timezone && timezone !== "Local") options.timeZone = timezone;
    const parts = new Intl.DateTimeFormat("en-US-u-ca-iso8601", options).formatToParts(value);
    const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
    return { year: Number(values.year), month: Number(values.month), day: Number(values.day) };
  } catch (_) {
    return { year: value.getFullYear(), month: value.getMonth() + 1, day: value.getDate() };
  }
}

function shiftCalendarDays(value: CalendarDate, days: number): CalendarDate {
  const shifted = new Date(Date.UTC(value.year, value.month - 1, value.day + days));
  return { year: shifted.getUTCFullYear(), month: shifted.getUTCMonth() + 1, day: shifted.getUTCDate() };
}

function shiftHoursISO(value: Date, hours: number): string {
  return new Date(value.getTime() + hours * 60 * 60 * 1000).toISOString();
}

function lastMonthRange(today: CalendarDate): { since: string; until: string } {
  const start = new Date(Date.UTC(today.year, today.month - 2, 1));
  const end = new Date(Date.UTC(today.year, today.month - 1, 0));
  return {
    since: formatCalendarDate({ year: start.getUTCFullYear(), month: start.getUTCMonth() + 1, day: start.getUTCDate() }),
    until: formatCalendarDate({ year: end.getUTCFullYear(), month: end.getUTCMonth() + 1, day: end.getUTCDate() }),
  };
}

function readInitialState(): StoredFilterState {
  const empty = { range: "all" as TimeRange, customSince: "", customUntil: "", channel: "", sourceProduct: "", provider: "", model: "", session: "", project: "" };
  if (typeof window === "undefined") return empty;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return empty;
    const parsed = JSON.parse(raw) as Partial<StoredFilterState>;
    const range = parsed.range && ["all", "24h", "7d", "30d", "month", "last_month", "custom"].includes(parsed.range) ? parsed.range : "all";
    return {
      range,
      customSince: parsed.customSince ?? "",
      customUntil: parsed.customUntil ?? "",
      channel: parsed.channel ?? "",
      sourceProduct: parsed.sourceProduct ?? "",
      provider: parsed.provider ?? "",
      model: parsed.model ?? "",
      session: parsed.session ?? "",
      project: parsed.project ?? "",
    };
  } catch (_) {
    return empty;
  }
}

function buildDateRange(range: TimeRange, customSince: string, customUntil: string, timezone: string): { since: string; until: string } {
  const now = new Date();
  const today = calendarDateAt(now, timezone);
  switch (range) {
    case "24h":
      return { since: shiftHoursISO(now, -24), until: shiftHoursISO(now, 0) };
    case "7d":
      return { since: formatCalendarDate(shiftCalendarDays(today, -6)), until: formatCalendarDate(today) };
    case "30d":
      return { since: formatCalendarDate(shiftCalendarDays(today, -29)), until: formatCalendarDate(today) };
    case "month":
      return { since: formatCalendarDate({ ...today, day: 1 }), until: formatCalendarDate(today) };
    case "last_month":
      return lastMonthRange(today);
    case "custom":
      return { since: customSince, until: customUntil };
    case "all":
    default:
      return { since: "", until: "" };
  }
}

export function FilterProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<StoredFilterState>(readInitialState);
  const { data: config } = useQuery({ queryKey: ["config"], queryFn: api.config });
  const reportTimezone = config?.reports.timezone || "Local";
  const { since: activeSince, until: activeUntil } = useMemo(
    () => buildDateRange(state.range, state.customSince, state.customUntil, reportTimezone),
    [reportTimezone, state.range, state.customSince, state.customUntil]
  );
  const filters = useMemo(() => ({
    since: activeSince,
    until: activeUntil,
    channel: state.channel,
    source_product: state.sourceProduct,
    provider: state.provider,
    model: state.model,
    session: state.session,
    project: state.project,
  }), [activeSince, activeUntil, state.channel, state.model, state.project, state.provider, state.session, state.sourceProduct]);

  useEffect(() => {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  }, [state]);

  const value = useMemo<FilterContextValue>(() => ({
    filters,
    range: state.range,
    customSince: state.customSince,
    customUntil: state.customUntil,
    activeSince,
    activeUntil,
    channel: state.channel,
    sourceProduct: state.sourceProduct,
    provider: state.provider,
    model: state.model,
    session: state.session,
    project: state.project,
    setRange: (range) => setState((current) => ({ ...current, range })),
    setCustomSince: (customSince) => setState((current) => ({ ...current, range: "custom", customSince })),
    setCustomUntil: (customUntil) => setState((current) => ({ ...current, range: "custom", customUntil })),
    setChannel: (channel) => setState((current) => ({ ...current, channel })),
    setSourceProduct: (sourceProduct) => setState((current) => ({ ...current, sourceProduct })),
    setProvider: (provider) => setState((current) => ({ ...current, provider })),
    setModel: (model) => setState((current) => ({ ...current, model })),
    setSession: (session) => setState((current) => ({ ...current, session })),
    setProject: (project) => setState((current) => ({ ...current, project })),
    clearFilters: () => setState({ range: "all", customSince: "", customUntil: "", channel: "", sourceProduct: "", provider: "", model: "", session: "", project: "" }),
  }), [activeSince, activeUntil, filters, state.channel, state.customSince, state.customUntil, state.model, state.project, state.provider, state.range, state.session, state.sourceProduct]);

  return <FilterContext.Provider value={value}>{children}</FilterContext.Provider>;
}

export function useFilterContext() {
  const value = useContext(FilterContext);
  if (!value) throw new Error("useFilterContext must be used within FilterProvider");
  return value;
}

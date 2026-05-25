import { createContext, ReactNode, useContext, useEffect, useMemo, useState } from "react";

import type { Filters } from "@/api/types";

export type TimeRange = "all" | "7d" | "30d" | "month" | "custom";

type StoredFilterState = {
  range: TimeRange;
  customSince: string;
  customUntil: string;
  channel: string;
  provider: string;
  model: string;
  session: string;
};

type FilterContextValue = {
  filters: Filters;
  range: TimeRange;
  customSince: string;
  customUntil: string;
  activeSince: string;
  activeUntil: string;
  channel: string;
  provider: string;
  model: string;
  session: string;
  setRange: (value: TimeRange) => void;
  setCustomSince: (value: string) => void;
  setCustomUntil: (value: string) => void;
  setChannel: (value: string) => void;
  setProvider: (value: string) => void;
  setModel: (value: string) => void;
  setSession: (value: string) => void;
  clearFilters: () => void;
};

const STORAGE_KEY = "agent-ledger-filters";
const FilterContext = createContext<FilterContextValue | null>(null);

function dateInput(value: Date): string {
  const year = value.getFullYear();
  const month = String(value.getMonth() + 1).padStart(2, "0");
  const day = String(value.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function shiftDays(days: number): string {
  const date = new Date();
  date.setDate(date.getDate() + days);
  return dateInput(date);
}

function monthStart(): string {
  const date = new Date();
  date.setDate(1);
  return dateInput(date);
}

function readInitialState(): StoredFilterState {
  const empty = { range: "all" as TimeRange, customSince: "", customUntil: "", channel: "", provider: "", model: "", session: "" };
  if (typeof window === "undefined") return empty;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return empty;
    const parsed = JSON.parse(raw) as Partial<StoredFilterState>;
    const range = parsed.range && ["all", "7d", "30d", "month", "custom"].includes(parsed.range) ? parsed.range : "all";
    return {
      range,
      customSince: parsed.customSince ?? "",
      customUntil: parsed.customUntil ?? "",
      channel: parsed.channel ?? "",
      provider: parsed.provider ?? "",
      model: parsed.model ?? "",
      session: parsed.session ?? "",
    };
  } catch (_) {
    return empty;
  }
}

function buildDateRange(range: TimeRange, customSince: string, customUntil: string): { since: string; until: string } {
  switch (range) {
    case "7d":
      return { since: shiftDays(-6), until: shiftDays(0) };
    case "30d":
      return { since: shiftDays(-29), until: shiftDays(0) };
    case "month":
      return { since: monthStart(), until: shiftDays(0) };
    case "custom":
      return { since: customSince, until: customUntil };
    case "all":
    default:
      return { since: "", until: "" };
  }
}

export function FilterProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<StoredFilterState>(readInitialState);
  const { since: activeSince, until: activeUntil } = useMemo(
    () => buildDateRange(state.range, state.customSince, state.customUntil),
    [state.range, state.customSince, state.customUntil]
  );
  const filters = useMemo(() => ({
    since: activeSince,
    until: activeUntil,
    channel: state.channel,
    provider: state.provider,
    model: state.model,
    session: state.session,
  }), [activeSince, activeUntil, state.channel, state.model, state.provider, state.session]);

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
    provider: state.provider,
    model: state.model,
    session: state.session,
    setRange: (range) => setState((current) => ({ ...current, range })),
    setCustomSince: (customSince) => setState((current) => ({ ...current, range: "custom", customSince })),
    setCustomUntil: (customUntil) => setState((current) => ({ ...current, range: "custom", customUntil })),
    setChannel: (channel) => setState((current) => ({ ...current, channel })),
    setProvider: (provider) => setState((current) => ({ ...current, provider })),
    setModel: (model) => setState((current) => ({ ...current, model })),
    setSession: (session) => setState((current) => ({ ...current, session })),
    clearFilters: () => setState({ range: "all", customSince: "", customUntil: "", channel: "", provider: "", model: "", session: "" }),
  }), [activeSince, activeUntil, filters, state.channel, state.customSince, state.customUntil, state.model, state.provider, state.range, state.session]);

  return <FilterContext.Provider value={value}>{children}</FilterContext.Provider>;
}

export function useFilterContext() {
  const value = useContext(FilterContext);
  if (!value) throw new Error("useFilterContext must be used within FilterProvider");
  return value;
}

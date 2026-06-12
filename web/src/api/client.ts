import type { ConfigSnapshot, EventItem, FilterOptions, Filters, Health, ImportRun, MetricRow, SlowSort, Status, Summary } from "./types";

const API_BASE = "/api/v1";

type QueryValue = string | number | boolean | undefined | null;

function query(params: Record<string, QueryValue>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    search.set(key, String(value));
  }
  const text = search.toString();
  return text ? `?${text}` : "";
}

async function request<T>(path: string): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`);
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const payload = (await response.json()) as { error?: string };
      message = payload.error ?? message;
    } catch (_) {
      // ignore non-JSON errors
    }
    throw new Error(message);
  }
  return (await response.json()) as T;
}

export const api = {
  health: () => request<Health>("/health"),
  status: () => request<Status>("/status"),
  config: () => request<ConfigSnapshot>("/config"),
  summary: (filters: Filters) => request<Summary>(`/analytics/summary${query(filters)}`),
  timeseries: (bucket: "daily" | "weekly" | "monthly", filters: Filters) =>
    request<MetricRow[]>(`/analytics/timeseries${query({ ...filters, bucket })}`),
  breakdown: (by: "channel" | "model" | "provider" | "session" | "project", filters: Filters) =>
    request<MetricRow[]>(`/analytics/breakdown${query({ ...filters, by })}`),
  slow: (sort: SlowSort, filters: Filters, limit = 50) =>
    request<EventItem[]>(`/analytics/slow${query({ ...filters, sort, limit })}`),
  filterOptions: () => request<FilterOptions>("/filter-options"),
  sessions: (filters: Filters, limit = 50) =>
    request<MetricRow[]>(`/sessions${query({ ...filters, limit })}`),
  importRuns: (limit = 20) => request<ImportRun[]>(`/import-runs${query({ limit })}`),
  events: (filters: Filters, limit = 200) => request<EventItem[]>(`/events${query({ ...filters, limit })}`),
};

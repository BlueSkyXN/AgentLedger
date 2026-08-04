import type { ConfigSnapshot, EventItem, FilterOptions, Filters, Health, ImportRun, MetricRow, Paginated, SessionItem, Status, Summary } from "./types";

const API_BASE = "/api/v2";

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
      // Ignore non-JSON errors so the user still gets the HTTP status.
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
  breakdown: (by: "channel" | "source_product" | "model" | "provider" | "session" | "project", filters: Filters) =>
    request<MetricRow[]>(`/analytics/breakdown${query({ ...filters, by })}`),
  filterOptions: () => request<FilterOptions>("/filter-options"),
  sessions: (filters: Filters, limit = 50, offset = 0) =>
    request<Paginated<SessionItem>>(`/sessions${query({ ...filters, limit, offset })}`),
  importRuns: (limit = 20) => request<ImportRun[]>(`/import-runs${query({ limit })}`),
  events: (filters: Filters, limit = 200, offset = 0) =>
    request<Paginated<EventItem>>(`/events${query({ ...filters, limit, offset })}`),
};

import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import { useFilterContext } from "@/hooks/filters";

export function useHealth() {
  return useQuery({ queryKey: ["health"], queryFn: api.health });
}

export function useStatus() {
  return useQuery({ queryKey: ["status"], queryFn: api.status });
}

export function useConfig() {
  return useQuery({ queryKey: ["config"], queryFn: api.config });
}

export function useFilterOptions() {
  return useQuery({ queryKey: ["filter-options"], queryFn: api.filterOptions });
}

export function useSummary() {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["summary", filters], queryFn: () => api.summary(filters) });
}

export function useTimeseries(bucket: "daily" | "weekly" | "monthly") {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["timeseries", bucket, filters], queryFn: () => api.timeseries(bucket, filters) });
}

export function useBreakdown(by: "channel" | "model" | "provider" | "session") {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["breakdown", by, filters], queryFn: () => api.breakdown(by, filters) });
}

export function useSlow(sort: "output_tps" | "ttft_ms" | "total_duration_ms", limit = 50) {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["slow", sort, filters, limit], queryFn: () => api.slow(sort, filters, limit) });
}

export function useSessions(limit = 50) {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["sessions", filters, limit], queryFn: () => api.sessions(filters, limit) });
}

export function useImportRuns(limit = 20) {
  return useQuery({ queryKey: ["import-runs", limit], queryFn: () => api.importRuns(limit) });
}

export function useEvents(limit = 200) {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["events", filters, limit], queryFn: () => api.events(filters, limit) });
}

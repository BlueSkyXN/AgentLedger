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

export function useBreakdown(by: "channel" | "source_product" | "model" | "provider" | "session" | "project") {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["breakdown", by, filters], queryFn: () => api.breakdown(by, filters) });
}

export function useSessions(limit = 50, offset = 0) {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["sessions", filters, limit, offset], queryFn: () => api.sessions(filters, limit, offset) });
}

export function useImportRuns(limit = 20) {
  return useQuery({ queryKey: ["import-runs", limit], queryFn: () => api.importRuns(limit) });
}

export function useEvents(limit = 200, offset = 0) {
  const { filters } = useFilterContext();
  return useQuery({ queryKey: ["events", filters, limit, offset], queryFn: () => api.events(filters, limit, offset) });
}

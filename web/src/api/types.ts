export type Filters = {
  since?: string;
  until?: string;
  channel?: string;
  provider?: string;
  model?: string;
  session?: string;
};

export type Summary = {
  total_events: number;
  import_runs: number;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  recorded_cost_usd: number;
  avg_total_duration_ms: number | null;
  avg_ttft_ms: number | null;
  avg_output_tps: number | null;
  first_event_at: string | null;
  last_event_at: string | null;
};

export type MetricRow = {
  label: string;
  events: number;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  recorded_cost_usd: number;
  avg_total_duration_ms: number | null;
  avg_ttft_ms: number | null;
  avg_output_tps: number | null;
};

export type Health = {
  status: string;
  version: string;
  database: string;
  database_bytes: number;
  asset_mode: string;
};

export type Status = {
  database: string;
  schema_version: string;
  total_events: number;
  total_import_runs: number;
  total_tokens: number;
  total_recorded_cost_usd: number;
};

export type ConfigSnapshot = {
  config_path: string;
  database: { path: string };
  import: { gracing_minutes: number };
  agents: Record<string, { enabled: boolean; paths: string[] }>;
  privacy_note: string;
};

export type ImportRun = {
  id: string;
  started_at: string | null;
  finished_at: string | null;
  status: string;
  files_scanned: number;
  events_added: number;
  events_updated: number;
  events_skipped: number;
  error: string | null;
};

export type EventItem = {
  event_id: string;
  dedupe_strategy: string;
  channel: string;
  provider: string | null;
  model_raw: string | null;
  model_normalized: string | null;
  timestamp: string | null;
  session_id: string | null;
  project_path: string | null;
  message_id: string | null;
  request_id: string | null;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  total_duration_ms: number | null;
  ttft_ms: number | null;
  output_duration_ms: number | null;
  output_tps: number | null;
  recorded_cost_usd: number | null;
};

export type FilterOptions = {
  channels: string[];
  providers: string[];
  models: string[];
  sessions: string[];
};

export type SlowSort = "output_tps" | "ttft_ms" | "total_duration_ms";

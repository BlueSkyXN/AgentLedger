export type Filters = {
  since?: string;
  until?: string;
};

export type Summary = {
  total_events: number;
  total_devices: number;
  import_runs: number;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  total_cost_usd: number;
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
  cost_usd: number;
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
  total_events: number;
  total_devices: number;
  total_import_runs: number;
  total_source_files: number;
  total_tokens: number;
  total_cost_usd: number;
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
  device_id: string;
  started_at: string | null;
  finished_at: string | null;
  status: string;
  files_scanned: number;
  events_added: number;
  events_skipped: number;
  error: string | null;
};

export type EventItem = {
  event_fingerprint: string;
  fingerprint_strategy: string;
  agent: string;
  provider: string | null;
  model_raw: string | null;
  model_normalized: string | null;
  timestamp: string | null;
  session_id: string | null;
  message_id: string | null;
  request_id: string | null;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  cost_usd: number;
  origin_device_id: string;
};

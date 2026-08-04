export type Filters = {
  since?: string;
  until?: string;
  channel?: string;
  source_product?: string;
  provider?: string;
  model?: string;
  session?: string;
  project?: string;
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
  first_date: string | null;
  last_date: string | null;
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
  source_product: string | null;
  provider: string | null;
  model_raw: string | null;
  model_normalized: string | null;
  model_resolution: "direct_event" | "thread_settings" | "turn_context" | "unknown" | "legacy_unclassified" | null;
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
};

export type SessionItem = {
  session_id: string;
  first_date: string | null;
  last_date: string | null;
  channel: string;
  source_product: string | null;
  primary_model: string | null;
  model_count: number;
  event_count: number;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number | null;
  priced_events: number;
  unpriced_events: number;
  policy_zero_events: number;
};

export type Paginated<T> = {
  items: T[];
  limit: number;
  offset: number;
  total: number;
};

export type FilterOptions = {
  channels: string[];
  source_products: string[];
  providers: string[];
  models: string[];
  sessions: string[];
  projects: string[];
};

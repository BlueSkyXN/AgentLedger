package db

const schemaSQLite = `
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT OR IGNORE INTO meta (key, value) VALUES ('schema_version', '1');
INSERT OR IGNORE INTO meta (key, value) VALUES ('created_at', datetime('now'));

CREATE TABLE IF NOT EXISTS devices (
    device_id       TEXT PRIMARY KEY,
    device_name     TEXT,
    hostname        TEXT,
    os              TEXT,
    arch            TEXT,
    app_version     TEXT,
    created_at_ms   INTEGER NOT NULL,
    last_seen_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS import_runs (
    id             TEXT PRIMARY KEY,
    device_id      TEXT NOT NULL,
    started_at_ms  INTEGER NOT NULL,
    finished_at_ms INTEGER,
    status         TEXT NOT NULL DEFAULT 'running',
    files_scanned  INTEGER DEFAULT 0,
    events_added   INTEGER DEFAULT 0,
    events_skipped INTEGER DEFAULT 0,
    error          TEXT,
    FOREIGN KEY (device_id) REFERENCES devices(device_id)
);

CREATE TABLE IF NOT EXISTS merge_runs (
    id             TEXT PRIMARY KEY,
    device_id      TEXT NOT NULL,
    source_path    TEXT NOT NULL,
    started_at_ms  INTEGER NOT NULL,
    finished_at_ms INTEGER,
    status         TEXT NOT NULL DEFAULT 'running',
    events_merged  INTEGER DEFAULT 0,
    events_skipped INTEGER DEFAULT 0,
    error          TEXT,
    FOREIGN KEY (device_id) REFERENCES devices(device_id)
);

CREATE TABLE IF NOT EXISTS sources (
    id        TEXT PRIMARY KEY,
    agent     TEXT NOT NULL,
    channel   TEXT NOT NULL,
    base_path TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS source_files (
    rowid            INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id        TEXT NOT NULL,
    file_path        TEXT NOT NULL UNIQUE,
    file_size        INTEGER,
    file_mtime_ms    INTEGER,
    content_sha256   TEXT,
    import_status    TEXT NOT NULL DEFAULT 'pending',
    cleanup_status   TEXT NOT NULL DEFAULT 'none',
    quarantined_path TEXT,
    last_import_ms   INTEGER,
    FOREIGN KEY (source_id) REFERENCES sources(id)
);

CREATE TABLE IF NOT EXISTS raw_records (
    rowid          INTEGER PRIMARY KEY AUTOINCREMENT,
    source_file_id INTEGER NOT NULL,
    line_number    INTEGER,
    raw_json       TEXT NOT NULL,
    raw_sha256     TEXT NOT NULL,
    parsed_ok      INTEGER NOT NULL DEFAULT 0,
    parse_error    TEXT,
    FOREIGN KEY (source_file_id) REFERENCES source_files(rowid)
);

CREATE TABLE IF NOT EXISTS usage_events (
    event_fingerprint TEXT PRIMARY KEY,
    dedupe_key TEXT,
    fingerprint_strategy TEXT NOT NULL,

    origin_device_id TEXT NOT NULL,
    first_seen_device_id TEXT NOT NULL,
    last_seen_device_id TEXT NOT NULL,

    agent TEXT NOT NULL,
    provider TEXT,
    client_name TEXT,
    source_channel TEXT,
    billing_channel TEXT,
    source_kind TEXT,

    model_raw TEXT,
    model_normalized TEXT,
    model_provider TEXT,
    model_family TEXT,
    is_fallback_model INTEGER DEFAULT 0,

    speed_label TEXT,
    service_tier TEXT,
    speed_multiplier REAL DEFAULT 1.0,
    is_fast_mode INTEGER DEFAULT 0,

    timestamp_ms INTEGER NOT NULL,
    timestamp_text TEXT,
    source_timezone TEXT,
    timestamp_offset_minutes INTEGER,

    session_id TEXT,
    conversation_id TEXT,
    project TEXT,
    project_path_raw TEXT,
    project_path_normalized TEXT,
    workspace_key TEXT,

    message_id TEXT,
    request_id TEXT,

    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    cache_creation_tokens INTEGER DEFAULT 0,
    cache_read_tokens INTEGER DEFAULT 0,
    reasoning_tokens INTEGER DEFAULT 0,
    tool_tokens INTEGER DEFAULT 0,
    extra_total_tokens INTEGER DEFAULT 0,
    source_total_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,

    cost_usd REAL DEFAULT 0.0,
    cost_source TEXT,
    pricing_source TEXT,
    pricing_version TEXT,

    credits REAL DEFAULT 0.0,
    message_count INTEGER DEFAULT 0,

    raw_usage_json TEXT,
    raw_meta_json TEXT,
    raw_sha256 TEXT,

    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_agent ON usage_events(agent);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON usage_events(timestamp_ms);
CREATE INDEX IF NOT EXISTS idx_events_model ON usage_events(model_normalized);
CREATE INDEX IF NOT EXISTS idx_events_session ON usage_events(session_id);
CREATE INDEX IF NOT EXISTS idx_events_device ON usage_events(origin_device_id);

CREATE TABLE IF NOT EXISTS event_observations (
    rowid             INTEGER PRIMARY KEY AUTOINCREMENT,
    event_fingerprint TEXT NOT NULL,
    device_id         TEXT NOT NULL,
    import_run_id     TEXT,
    observed_at_ms    INTEGER NOT NULL,
    source_file_path  TEXT,
    FOREIGN KEY (event_fingerprint) REFERENCES usage_events(event_fingerprint),
    FOREIGN KEY (device_id) REFERENCES devices(device_id)
);

CREATE TABLE IF NOT EXISTS event_conflicts (
    rowid             INTEGER PRIMARY KEY AUTOINCREMENT,
    event_fingerprint TEXT NOT NULL,
    field_name        TEXT NOT NULL,
    old_value         TEXT,
    new_value         TEXT,
    resolution        TEXT,
    resolved_at_ms    INTEGER,
    FOREIGN KEY (event_fingerprint) REFERENCES usage_events(event_fingerprint)
);
`

func (d *Database) initSchema() error {
	_, err := d.conn.Exec(schemaSQLite)
	return err
}

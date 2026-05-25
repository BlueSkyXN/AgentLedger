package db

import (
	"database/sql"
	"errors"
	"fmt"
)

const SchemaVersion = "2"

var ErrIncompatibleSchema = errors.New("incompatible database schema")

const schemaSQLite = `
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT OR REPLACE INTO meta (key, value) VALUES ('schema_version', '2');
INSERT OR IGNORE INTO meta (key, value) VALUES ('created_at', datetime('now'));

CREATE TABLE IF NOT EXISTS import_runs (
    id             TEXT PRIMARY KEY,
    started_at_ms  INTEGER NOT NULL,
    finished_at_ms INTEGER,
    status         TEXT NOT NULL DEFAULT 'running',
    files_scanned  INTEGER NOT NULL DEFAULT 0,
    events_added   INTEGER NOT NULL DEFAULT 0,
    events_updated INTEGER NOT NULL DEFAULT 0,
    events_skipped INTEGER NOT NULL DEFAULT 0,
    error          TEXT
);

CREATE TABLE IF NOT EXISTS usage_events (
    event_id TEXT PRIMARY KEY,
    dedupe_key TEXT NOT NULL,
    dedupe_strategy TEXT NOT NULL,

    channel TEXT NOT NULL,
    provider TEXT,
    model_raw TEXT,
    model_normalized TEXT,

    timestamp_ms INTEGER NOT NULL,
    session_id TEXT,
    project_path TEXT,
    message_id TEXT,
    request_id TEXT,
    source_file TEXT,
    line_number INTEGER,
    raw_sha256 TEXT,

    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,

    request_started_at_ms INTEGER,
    first_token_at_ms INTEGER,
    completed_at_ms INTEGER,
    total_duration_ms INTEGER,
    ttft_ms INTEGER,
    output_duration_ms INTEGER,
    output_tps REAL,

    recorded_cost_usd REAL,
    raw_usage_json TEXT,

    imported_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_usage_timestamp ON usage_events(timestamp_ms);
CREATE INDEX IF NOT EXISTS idx_usage_channel ON usage_events(channel);
CREATE INDEX IF NOT EXISTS idx_usage_provider ON usage_events(provider);
CREATE INDEX IF NOT EXISTS idx_usage_model ON usage_events(model_normalized);
CREATE INDEX IF NOT EXISTS idx_usage_session ON usage_events(session_id);
CREATE INDEX IF NOT EXISTS idx_usage_output_tps ON usage_events(output_tps);
CREATE INDEX IF NOT EXISTS idx_usage_total_duration ON usage_events(total_duration_ms);
CREATE INDEX IF NOT EXISTS idx_usage_channel_time ON usage_events(channel, timestamp_ms);
CREATE INDEX IF NOT EXISTS idx_usage_model_time ON usage_events(model_normalized, timestamp_ms);
`

func (d *Database) initSchema() error {
	version, exists, err := d.schemaVersion()
	if err != nil {
		return err
	}
	if exists && version != SchemaVersion {
		return fmt.Errorf("%w: database schema version %s is not compatible with AgentLedger v2; run `agent-ledger init --reset` to rebuild the local database", ErrIncompatibleSchema, version)
	}
	_, err = d.conn.Exec(schemaSQLite)
	return err
}

func (d *Database) schemaVersion() (string, bool, error) {
	var tableName string
	err := d.conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	var version string
	err = d.conn.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", true, fmt.Errorf("%w: missing schema_version in meta table; run `agent-ledger init --reset` to rebuild the local database", ErrIncompatibleSchema)
	}
	if err != nil {
		return "", true, err
	}
	return version, true, nil
}

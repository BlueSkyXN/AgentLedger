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

    source_agent TEXT,
    source_product TEXT,
    observability_level TEXT,
    model_is_fallback INTEGER NOT NULL DEFAULT 0,
    source_total_tokens INTEGER,
    raw_input_tokens INTEGER,
    token_accounting_method TEXT,
    accounting_profile TEXT,

    timestamp_ms INTEGER NOT NULL,
    session_id TEXT,
    session_path_id TEXT,
    turn_id TEXT,
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
	if _, err = d.conn.Exec(schemaSQLite); err != nil {
		return err
	}
	return d.ensureV2CompatibilityColumns()
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

func (d *Database) ensureV2CompatibilityColumns() error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	columns := []string{
		"source_agent TEXT",
		"source_product TEXT",
		"observability_level TEXT",
		"model_is_fallback INTEGER NOT NULL DEFAULT 0",
		"source_total_tokens INTEGER",
		"raw_input_tokens INTEGER",
		"token_accounting_method TEXT",
		"accounting_profile TEXT",
		"session_path_id TEXT",
		"turn_id TEXT",
	}
	for _, columnDef := range columns {
		name := columnName(columnDef)
		exists, err := columnExists(tx, "usage_events", name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err = tx.Exec(fmt.Sprintf("ALTER TABLE usage_events ADD COLUMN %s", columnDef)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`
        UPDATE usage_events
        SET
            source_agent = COALESCE(NULLIF(source_agent, ''), channel),
            observability_level = COALESCE(NULLIF(observability_level, ''), 'unknown')
    `); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_usage_source_agent_time ON usage_events(source_agent, timestamp_ms)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_usage_session_path ON usage_events(session_path_id)`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func columnExists(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func columnName(columnDef string) string {
	for i, r := range columnDef {
		if r == ' ' || r == '\t' || r == '\n' {
			return columnDef[:i]
		}
	}
	return columnDef
}

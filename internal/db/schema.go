package db

import (
	"database/sql"
	"errors"
	"fmt"
)

const SchemaVersion = "2"

var ErrIncompatibleSchema = errors.New("incompatible database schema")

var v2CompatibilityColumns = []string{
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

type requiredTableSchema struct {
	name    string
	columns []string
}

var v2RequiredTableSchemas = []requiredTableSchema{
	{
		name:    "meta",
		columns: []string{"key", "value"},
	},
	{
		name: "import_runs",
		columns: []string{
			"id",
			"started_at_ms",
			"finished_at_ms",
			"status",
			"files_scanned",
			"events_added",
			"events_updated",
			"events_skipped",
			"error",
		},
	},
	{
		name: "usage_events",
		columns: append([]string{
			"event_id",
			"dedupe_key",
			"dedupe_strategy",
			"channel",
			"provider",
			"model_raw",
			"model_normalized",
			"timestamp_ms",
			"session_id",
			"project_path",
			"message_id",
			"request_id",
			"source_file",
			"line_number",
			"raw_sha256",
			"input_tokens",
			"output_tokens",
			"reasoning_tokens",
			"cache_creation_tokens",
			"cache_read_tokens",
			"total_tokens",
			"request_started_at_ms",
			"first_token_at_ms",
			"completed_at_ms",
			"total_duration_ms",
			"ttft_ms",
			"output_duration_ms",
			"output_tps",
			"recorded_cost_usd",
			"raw_usage_json",
			"imported_at_ms",
			"updated_at_ms",
		}, v2CompatibilityColumnNames()...),
	},
}

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
CREATE INDEX IF NOT EXISTS idx_usage_source_identity ON usage_events(source_file, line_number, raw_sha256, channel, imported_at_ms, event_id);
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

func (d *Database) validateReadOnlySchema() error {
	metaExists, err := d.tableExists("meta")
	if err != nil {
		return err
	}
	if !metaExists {
		return fmt.Errorf("%w: database is not initialized; run `agent-ledger init` or `agent-ledger import` first", ErrIncompatibleSchema)
	}
	if err := d.validateRequiredColumns(v2RequiredTableSchemas[0]); err != nil {
		return err
	}

	version, exists, err := d.schemaVersion()
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: database is not initialized; run `agent-ledger init` or `agent-ledger import` first", ErrIncompatibleSchema)
	}
	if version != SchemaVersion {
		return fmt.Errorf("%w: database schema version %s is not compatible with AgentLedger v2; run `agent-ledger init --reset` to rebuild the local database", ErrIncompatibleSchema, version)
	}

	for _, table := range v2RequiredTableSchemas[1:] {
		exists, err := d.tableExists(table.name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: database is missing required table %s; run `agent-ledger init` or `agent-ledger import` to repair the v2 schema", ErrIncompatibleSchema, table.name)
		}
		if err := d.validateRequiredColumns(table); err != nil {
			return err
		}
	}
	return nil
}

func (d *Database) tableExists(table string) (bool, error) {
	var count int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

func (d *Database) validateRequiredColumns(table requiredTableSchema) error {
	columns, err := tableColumns(d.conn, table.name)
	if err != nil {
		return err
	}
	for _, column := range table.columns {
		if _, exists := columns[column]; exists {
			continue
		}

		if table.name == "usage_events" && isV2CompatibilityColumn(column) {
			return fmt.Errorf("%w: database is missing additive v2 column %s.%s; run `agent-ledger init` or `agent-ledger import` to apply compatibility updates", ErrIncompatibleSchema, table.name, column)
		}
		return fmt.Errorf("%w: database is missing required column %s.%s; restore a valid v2 database or back it up and run `agent-ledger init --reset`", ErrIncompatibleSchema, table.name, column)
	}
	return nil
}

func v2CompatibilityColumnNames() []string {
	names := make([]string, 0, len(v2CompatibilityColumns))
	for _, columnDef := range v2CompatibilityColumns {
		names = append(names, columnName(columnDef))
	}
	return names
}

func isV2CompatibilityColumn(name string) bool {
	for _, columnDef := range v2CompatibilityColumns {
		if columnName(columnDef) == name {
			return true
		}
	}
	return false
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

	for _, columnDef := range v2CompatibilityColumns {
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

type sqlQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func columnExists(queryer sqlQueryer, table, column string) (bool, error) {
	columns, err := tableColumns(queryer, table)
	if err != nil {
		return false, err
	}
	_, exists := columns[column]
	return exists, nil
}

func tableColumns(queryer sqlQueryer, table string) (map[string]struct{}, error) {
	rows, err := queryer.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func columnName(columnDef string) string {
	for i, r := range columnDef {
		if r == ' ' || r == '\t' || r == '\n' {
			return columnDef[:i]
		}
	}
	return columnDef
}

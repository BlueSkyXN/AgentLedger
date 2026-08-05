package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

const (
	SchemaVersion   = "3"
	IdentityVersion = "2"
)

var ErrIncompatibleSchema = errors.New("incompatible database schema")

type requiredTableSchema struct {
	name    string
	columns []string
}

var v3RequiredTableSchemas = []requiredTableSchema{
	{name: "meta", columns: []string{"key", "value"}},
	{name: "import_runs", columns: []string{
		"id", "started_at_ms", "finished_at_ms", "status", "files_scanned",
		"events_added", "events_updated", "events_skipped", "events_rejected", "error",
	}},
	{name: "usage_events", columns: []string{
		"event_id", "identity_version", "identity_strategy", "identity_scope", "content_sha256",
		"parser_version", "event_granularity", "channel", "source_product", "provider",
		"model_raw", "model_normalized", "model_resolution", "model_is_fallback",
		"timestamp_ms", "session_key", "session_id", "session_path_id", "turn_id", "project_path",
		"message_id", "request_id", "source_file", "line_number", "raw_sha256",
		"input_tokens", "output_tokens", "reasoning_tokens", "cache_creation_tokens", "cache_read_tokens",
		"total_tokens", "source_total_tokens", "raw_input_tokens", "token_accounting_method",
		"accounting_profile", "observability_level", "imported_at_ms", "updated_at_ms",
	}},
}

const schemaSQLite = `
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT OR REPLACE INTO meta (key, value) VALUES ('schema_version', '3');
INSERT OR REPLACE INTO meta (key, value) VALUES ('identity_version', '2');
INSERT OR IGNORE INTO meta (key, value) VALUES ('created_at', datetime('now'));

CREATE TABLE IF NOT EXISTS import_runs (
    id              TEXT PRIMARY KEY,
    started_at_ms   INTEGER NOT NULL,
    finished_at_ms  INTEGER,
    status          TEXT NOT NULL DEFAULT 'running',
    files_scanned   INTEGER NOT NULL DEFAULT 0 CHECK (files_scanned >= 0),
    events_added    INTEGER NOT NULL DEFAULT 0 CHECK (events_added >= 0),
    events_updated  INTEGER NOT NULL DEFAULT 0 CHECK (events_updated >= 0),
    events_skipped  INTEGER NOT NULL DEFAULT 0 CHECK (events_skipped >= 0),
    events_rejected INTEGER NOT NULL DEFAULT 0 CHECK (events_rejected >= 0),
    error           TEXT
);

CREATE TABLE IF NOT EXISTS usage_events (
    event_id          TEXT PRIMARY KEY CHECK (length(trim(event_id)) > 0),
    identity_version  INTEGER NOT NULL CHECK (identity_version = 2),
    identity_strategy TEXT NOT NULL CHECK (length(trim(identity_strategy)) > 0),
    identity_scope    TEXT NOT NULL CHECK (length(trim(identity_scope)) > 0),
    content_sha256    TEXT NOT NULL CHECK (length(trim(content_sha256)) > 0),
    parser_version    TEXT,
    event_granularity TEXT NOT NULL CHECK (length(trim(event_granularity)) > 0),

    channel           TEXT NOT NULL CHECK (length(trim(channel)) > 0),
    source_product    TEXT NOT NULL CHECK (length(trim(source_product)) > 0),
    provider          TEXT,
    model_raw         TEXT,
    model_normalized  TEXT NOT NULL DEFAULT 'unknown' CHECK (length(trim(model_normalized)) > 0),
    model_resolution  TEXT,
    model_is_fallback INTEGER NOT NULL DEFAULT 0 CHECK (model_is_fallback IN (0, 1)),

    timestamp_ms   INTEGER NOT NULL CHECK (timestamp_ms > 0),
    session_key    TEXT NOT NULL CHECK (length(trim(session_key)) > 0),
    session_id     TEXT,
    session_path_id TEXT,
    turn_id        TEXT,
    project_path   TEXT,
    message_id     TEXT,
    request_id     TEXT,

    source_file TEXT,
    line_number INTEGER CHECK (line_number IS NULL OR line_number >= 0),
    raw_sha256  TEXT,

    input_tokens          INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens         INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    reasoning_tokens      INTEGER NOT NULL DEFAULT 0 CHECK (reasoning_tokens >= 0),
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_creation_tokens >= 0),
    cache_read_tokens     INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    total_tokens          INTEGER NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
    source_total_tokens   INTEGER CHECK (source_total_tokens IS NULL OR source_total_tokens >= 0),
    raw_input_tokens      INTEGER CHECK (raw_input_tokens IS NULL OR raw_input_tokens >= 0),
    token_accounting_method TEXT,
    accounting_profile     TEXT,
    observability_level    TEXT,

    imported_at_ms INTEGER NOT NULL,
    updated_at_ms  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_usage_timestamp ON usage_events(timestamp_ms);
CREATE INDEX IF NOT EXISTS idx_usage_session ON usage_events(session_key);
CREATE INDEX IF NOT EXISTS idx_usage_channel_time ON usage_events(channel, timestamp_ms);
CREATE INDEX IF NOT EXISTS idx_usage_source_time ON usage_events(source_product, timestamp_ms);
CREATE INDEX IF NOT EXISTS idx_usage_model_time ON usage_events(model_normalized, timestamp_ms);
`

func (d *Database) initSchema() error {
	version, exists, err := d.metaValue("schema_version")
	if err != nil {
		return err
	}
	if exists && version != SchemaVersion {
		return incompatibleVersionError(version)
	}
	if exists {
		identity, _, identityErr := d.identityVersion()
		if identityErr != nil {
			return identityErr
		}
		if identity != IdentityVersion {
			return fmt.Errorf("%w: identity version %s is not compatible with AgentLedger v3 identity version %s; rebuild from source logs", ErrIncompatibleSchema, identity, IdentityVersion)
		}
	}
	if _, err := d.conn.Exec(schemaSQLite); err != nil {
		return err
	}
	return d.validateReadOnlySchema()
}

func (d *Database) schemaVersion() (string, bool, error) {
	return d.metaValue("schema_version")
}

func (d *Database) identityVersion() (string, bool, error) {
	return d.metaValue("identity_version")
}

func (d *Database) metaValue(key string) (string, bool, error) {
	var tableName string
	err := d.conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	var value string
	err = d.conn.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", true, fmt.Errorf("%w: missing %s in meta table", ErrIncompatibleSchema, key)
	}
	if err != nil {
		return "", true, err
	}
	return value, true, nil
}

func (d *Database) validateReadOnlySchema() error {
	version, exists, err := d.schemaVersion()
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: database is not initialized; run `agent-ledger init` or `agent-ledger import` first", ErrIncompatibleSchema)
	}
	if version != SchemaVersion {
		return incompatibleVersionError(version)
	}
	identity, _, err := d.identityVersion()
	if err != nil {
		return err
	}
	if identity != IdentityVersion {
		return fmt.Errorf("%w: identity version %s is not compatible with AgentLedger v3 identity version %s; rebuild from source logs", ErrIncompatibleSchema, identity, IdentityVersion)
	}

	for _, table := range v3RequiredTableSchemas {
		exists, err := d.tableExists(table.name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: database is missing required table %s; rebuild the v3 database from source logs", ErrIncompatibleSchema, table.name)
		}
		if err := d.validateRequiredColumns(table); err != nil {
			return err
		}
	}
	return d.validateApplicationObjects()
}

func (d *Database) validateApplicationObjects() error {
	allowedTables := make(map[string]struct{}, len(v3RequiredTableSchemas))
	for _, table := range v3RequiredTableSchemas {
		allowedTables[table.name] = struct{}{}
	}
	rows, err := d.conn.Query(`SELECT type, name FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND type IN ('table', 'view', 'trigger')
		ORDER BY type, name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var objectType, name string
		if err := rows.Scan(&objectType, &name); err != nil {
			return err
		}
		if objectType == "table" {
			if _, ok := allowedTables[name]; ok {
				continue
			}
		}
		return fmt.Errorf("%w: database has unexpected %s %s; rebuild a valid v3 database", ErrIncompatibleSchema, objectType, name)
	}
	return rows.Err()
}

func incompatibleVersionError(version string) error {
	if version == "2" {
		return fmt.Errorf("%w: database schema version 2 is not accepted by AgentLedger v3; keep an exact backup and rebuild from source logs", ErrIncompatibleSchema)
	}
	return fmt.Errorf("%w: database schema version %s is not compatible with AgentLedger v%s", ErrIncompatibleSchema, strconv.Quote(version), SchemaVersion)
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
	expected := make(map[string]struct{}, len(table.columns))
	for _, column := range table.columns {
		expected[column] = struct{}{}
		if _, ok := columns[column]; !ok {
			return fmt.Errorf("%w: database is missing required column %s.%s; rebuild a valid v3 database", ErrIncompatibleSchema, table.name, column)
		}
	}
	var unexpected []string
	for column := range columns {
		if _, ok := expected[column]; !ok {
			unexpected = append(unexpected, column)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		return fmt.Errorf("%w: database has unexpected column %s.%s; rebuild a valid v3 database", ErrIncompatibleSchema, table.name, unexpected[0])
	}
	return nil
}

type tableInfoQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func tableColumns(queryer tableInfoQueryer, table string) (map[string]struct{}, error) {
	rows, err := queryer.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	return columns, rows.Err()
}

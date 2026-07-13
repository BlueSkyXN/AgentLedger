package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestOpenInitializesSchemaV2(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	var version string
	if err := database.Conn().QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("version = %s, want %s", version, SchemaVersion)
	}
}

func TestOpenRegistersProjectLabelFunction(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	cases := []struct {
		input any
		want  string
	}{
		{input: nil, want: "no-project"},
		{input: "", want: "no-project"},
		{input: "/Users/alice/Github/project-a", want: "project-a"},
		{input: `C:\Users\alice\repo\project-b`, want: "project-b"},
		{input: "-Users-alice-Github-project-c", want: "-Users-alice-Github-project-c"},
	}
	for _, tc := range cases {
		var got string
		if err := database.Conn().QueryRow(`SELECT agentledger_project_label(?)`, tc.input).Scan(&got); err != nil {
			t.Fatalf("project label %v: %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("project label %v = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestOpenReadOnlySeesWALWritesAndRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()

	insertEvent := func(eventID string, timestamp int64) {
		t.Helper()
		_, err := writer.Conn().Exec(`INSERT INTO usage_events (
			event_id, dedupe_key, dedupe_strategy, channel, timestamp_ms,
			input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
			imported_at_ms, updated_at_ms
		) VALUES (?, ?, 'message_id', 'codex', ?, 1, 1, 0, 0, 0, 2, ?, ?)`, eventID, eventID, timestamp, timestamp, timestamp)
		if err != nil {
			t.Fatalf("insert %s: %v", eventID, err)
		}
	}
	insertEvent("event-1", 1)

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer reader.Close()

	var queryOnly int
	if err := reader.Conn().QueryRow(`PRAGMA query_only`).Scan(&queryOnly); err != nil {
		t.Fatalf("query_only: %v", err)
	}
	if queryOnly != 1 {
		t.Fatalf("query_only = %d, want 1", queryOnly)
	}

	var count int
	if err := reader.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil {
		t.Fatalf("initial count: %v", err)
	}
	if count != 1 {
		t.Fatalf("initial count = %d, want 1", count)
	}

	insertEvent("event-2", 2)
	if err := reader.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil {
		t.Fatalf("updated count: %v", err)
	}
	if count != 2 {
		t.Fatalf("updated count = %d, want 2", count)
	}

	if _, err := reader.Conn().Exec(`UPDATE meta SET value = 'changed' WHERE key = 'schema_version'`); err == nil {
		t.Fatal("read-only connection accepted a write")
	}

	var project string
	if err := reader.Conn().QueryRow(`SELECT agentledger_project_label(?)`, "/tmp/project-a").Scan(&project); err != nil {
		t.Fatalf("project label: %v", err)
	}
	if project != "project-a" {
		t.Fatalf("project label = %q", project)
	}
}

func TestOpenReadOnlyDoesNotRunSchemaMaintenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := database.Conn().Exec(`INSERT INTO usage_events (
		event_id, dedupe_key, dedupe_strategy, channel, timestamp_ms,
		input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
		imported_at_ms, updated_at_ms
	) VALUES ('event-1', 'event-1', 'message_id', 'codex', 1, 1, 1, 0, 0, 0, 2, 1, 1)`); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := conn.Exec(`DROP INDEX idx_usage_source_identity`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	for name, opener := range map[string]func(string) (*Database, error){
		"sqlite": OpenReadOnly,
		"v2":     OpenReadOnlyV2,
	} {
		reader, err := opener(path)
		if err != nil {
			t.Fatalf("open %s read-only: %v", name, err)
		}
		if err := reader.Close(); err != nil {
			t.Fatalf("close %s read-only: %v", name, err)
		}
	}

	conn, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer conn.Close()
	var indexCount int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_usage_source_identity'`).Scan(&indexCount); err != nil {
		t.Fatalf("index count: %v", err)
	}
	if indexCount != 0 {
		t.Fatalf("read-only open recreated source identity index")
	}
	var sourceAgent, observability sql.NullString
	if err := conn.QueryRow(`SELECT source_agent, observability_level FROM usage_events WHERE event_id = 'event-1'`).Scan(&sourceAgent, &observability); err != nil {
		t.Fatalf("source metadata: %v", err)
	}
	if sourceAgent.Valid || observability.Valid {
		t.Fatalf("read-only open backfilled source metadata: source_agent=%v observability=%v", sourceAgent, observability)
	}
}

func TestOpenReadOnlyRejectsMissingDatabaseWithoutCreatingPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	path := filepath.Join(dir, "agent-ledger.db")

	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("expected missing database error")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read-only open created database directory: %v", err)
	}
}

func TestOpenReadOnlyV2RejectsIncompleteSchemaWithoutRepairingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := conn.Exec(`ALTER TABLE usage_events DROP COLUMN turn_id`); err != nil {
		t.Fatalf("drop compatibility column: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open physical read-only: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close physical read-only: %v", err)
	}

	reader, err = OpenReadOnlyV2(path)
	if err == nil {
		_ = reader.Close()
		t.Fatal("expected incomplete v2 schema error")
	}
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("expected ErrIncompatibleSchema, got %v", err)
	}

	conn, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer conn.Close()
	var turnIDColumns int
	rows, err := conn.Query(`PRAGMA table_info(usage_events)`)
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if name == "turn_id" {
			turnIDColumns++
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close rows: %v", err)
	}
	if turnIDColumns != 0 {
		t.Fatal("v2 read-only open repaired the missing compatibility column")
	}
}

func TestOpenReadOnlyV2RejectsMissingRequiredTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := conn.Exec(`DROP TABLE import_runs`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open physical read-only: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close physical read-only: %v", err)
	}

	reader, err = OpenReadOnlyV2(path)
	if err == nil {
		_ = reader.Close()
		t.Fatal("expected missing table error")
	}
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("expected ErrIncompatibleSchema, got %v", err)
	}
}

func TestSourceIdentityLookupUsesCompositeIndex(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	rows, err := database.Conn().Query(`
        EXPLAIN QUERY PLAN
        SELECT event_id, total_tokens
        FROM usage_events
        WHERE source_file = ? AND line_number = ? AND raw_sha256 = ? AND channel = ?
        ORDER BY imported_at_ms ASC, event_id ASC
    `, "/synthetic/session.jsonl", 7, "raw-hash", "codex")
	if err != nil {
		t.Fatalf("explain source identity lookup: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}

	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "idx_usage_source_identity") || strings.Contains(plan, "SCAN usage_events") || strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("source identity lookup is not using the composite index:\n%s", plan)
	}
}

func TestRedactedSourceIdentityLookupUsesCompositeIndex(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	rows, err := database.Conn().Query(`
		EXPLAIN QUERY PLAN
		SELECT event_id, total_tokens
		FROM usage_events
		WHERE source_file IS NULL
			AND line_number = ? AND raw_sha256 = ? AND channel = ?
			AND session_id = ? AND timestamp_ms = ?
		ORDER BY imported_at_ms ASC, event_id ASC
	`, 7, "raw-hash", "codex", "session-a", 1)
	if err != nil {
		t.Fatalf("explain redacted source identity lookup: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}

	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "idx_usage_source_identity") || strings.Contains(plan, "SCAN usage_events") || strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("redacted source identity lookup is not using the composite index:\n%s", plan)
	}
}

func TestCodexExactLookupUsesIndexesForEventAndRedactedExistence(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	rows, err := database.Conn().Query(
		`EXPLAIN QUERY PLAN `+codexEventComparisonQuery,
		`{}`, 1, 7, "raw-hash", "codex", "session-a", 1, "event-a",
	)
	if err != nil {
		t.Fatalf("explain Codex exact lookup: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}

	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "SEARCH usage_events") ||
		!strings.Contains(plan, "event_id=?") ||
		!strings.Contains(plan, "SEARCH redacted USING INDEX idx_usage_source_identity (source_file=? AND line_number=? AND raw_sha256=? AND channel=?)") ||
		strings.Contains(plan, "SCAN ") {
		t.Fatalf("Codex exact lookup is not using both indexes:\n%s", plan)
	}
}

func TestOpenRejectsSchemaV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("sql open: %v", err)
	}
	_, err = conn.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO meta (key, value) VALUES ('schema_version', '1')`)
	if err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	_ = conn.Close()

	database, err := Open(path)
	if err == nil {
		_ = database.Close()
		t.Fatal("expected incompatible schema error")
	}
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("expected ErrIncompatibleSchema, got %v", err)
	}

	database, err = OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open physical read-only v1: %v", err)
	}
	var integrity string
	if err := database.Conn().QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("v1 integrity check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("v1 integrity = %q", integrity)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close physical read-only v1: %v", err)
	}

	database, err = OpenReadOnlyV2(path)
	if err == nil {
		_ = database.Close()
		t.Fatal("expected v2 read-only incompatible schema error")
	}
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("expected v2 read-only ErrIncompatibleSchema, got %v", err)
	}
}

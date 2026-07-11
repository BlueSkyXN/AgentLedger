package db

import (
	"database/sql"
	"errors"
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
}

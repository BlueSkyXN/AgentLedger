package cmd

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func TestExportDatabaseRedactsPrivateFields(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.db")
	database, err := db.Open(source)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	_, err = database.Conn().Exec(`INSERT INTO usage_events (
		event_id, dedupe_key, dedupe_strategy, channel, timestamp_ms, session_id, project_path, source_file, raw_usage_json,
		input_tokens, output_tokens, total_tokens, imported_at_ms, updated_at_ms
	) VALUES (
		'event-1', 'event-1', 'message_id', 'codex', 1, 'session-1', '/Users/alice/project', '/Users/alice/.codex/log.jsonl', '{"path":"/Users/alice/project"}',
		10, 5, 15, 1, 1
	)`)
	if err != nil {
		t.Fatalf("insert source event: %v", err)
	}
	_, err = database.Conn().Exec(`INSERT INTO import_runs (
		id, started_at_ms, status, error
	) VALUES (
		'run-1', 1, 'completed_with_warnings', 'failed to parse /Users/alice/.codex/log.jsonl'
	)`)
	if err != nil {
		t.Fatalf("insert import run: %v", err)
	}
	_ = database.Close()

	output := filepath.Join(t.TempDir(), "export.aldb")
	if _, err := exportDatabase(source, output, true); err != nil {
		t.Fatalf("export: %v", err)
	}

	conn, err := sql.Open("sqlite3", output)
	if err != nil {
		t.Fatalf("open export: %v", err)
	}
	defer conn.Close()

	var session string
	var total int64
	var projectPath, sourceFile, rawUsage sql.NullString
	if err := conn.QueryRow(`SELECT session_id, total_tokens, project_path, source_file, raw_usage_json FROM usage_events WHERE event_id='event-1'`).Scan(&session, &total, &projectPath, &sourceFile, &rawUsage); err != nil {
		t.Fatalf("select export: %v", err)
	}
	if session != "session-1" || total != 15 {
		t.Fatalf("redacted export changed analytics fields session=%q total=%d", session, total)
	}
	if projectPath.Valid || sourceFile.Valid || rawUsage.Valid {
		t.Fatalf("expected private fields to be redacted, project=%v source=%v raw=%v", projectPath, sourceFile, rawUsage)
	}
	var importError sql.NullString
	if err := conn.QueryRow(`SELECT error FROM import_runs WHERE id='run-1'`).Scan(&importError); err != nil {
		t.Fatalf("select import run error: %v", err)
	}
	if importError.Valid {
		t.Fatalf("expected import run warning to be redacted, got %q", importError.String)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	for _, privateValue := range [][]byte{[]byte("/Users/alice/project"), []byte("/Users/alice/.codex/log.jsonl")} {
		if bytes.Contains(data, privateValue) {
			t.Fatalf("redacted export still contains private value %q", privateValue)
		}
	}
}

func TestExportDatabaseCanKeepPrivateFieldsWhenConfigured(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.db")
	database, err := db.Open(source)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	_, err = database.Conn().Exec(`INSERT INTO usage_events (
		event_id, dedupe_key, dedupe_strategy, channel, timestamp_ms, source_file, raw_usage_json,
		input_tokens, output_tokens, total_tokens, imported_at_ms, updated_at_ms
	) VALUES (
		'event-1', 'event-1', 'message_id', 'codex', 1, '/Users/alice/.codex/log.jsonl', '{"secret":"kept"}',
		10, 5, 15, 1, 1
	)`)
	if err != nil {
		t.Fatalf("insert source event: %v", err)
	}
	_ = database.Close()

	output := filepath.Join(t.TempDir(), "export.aldb")
	if _, err := exportDatabase(source, output, false); err != nil {
		t.Fatalf("export: %v", err)
	}

	conn, err := sql.Open("sqlite3", output)
	if err != nil {
		t.Fatalf("open export: %v", err)
	}
	defer conn.Close()

	var sourceFile, rawUsage string
	if err := conn.QueryRow(`SELECT source_file, raw_usage_json FROM usage_events WHERE event_id='event-1'`).Scan(&sourceFile, &rawUsage); err != nil {
		t.Fatalf("select export: %v", err)
	}
	if sourceFile == "" || rawUsage == "" {
		t.Fatalf("expected private fields to be preserved when redaction is disabled")
	}
}

func TestExportDatabaseRequiresExistingSource(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "existing.aldb")
	if err := os.WriteFile(output, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	if _, err := exportDatabase(filepath.Join(dir, "missing.db"), output, true); err == nil {
		t.Fatal("expected missing source database to fail")
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read existing output: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("missing source should not replace existing output, got %q", data)
	}
}

func TestExportDatabaseInvalidSourcePreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	output := filepath.Join(dir, "existing.aldb")
	if err := os.WriteFile(source, []byte("not sqlite"), 0o644); err != nil {
		t.Fatalf("write invalid source: %v", err)
	}
	if err := os.WriteFile(output, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	if _, err := exportDatabase(source, output, true); err == nil {
		t.Fatal("expected invalid source database to fail")
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read existing output: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("invalid source should not replace existing output, got %q", data)
	}
}

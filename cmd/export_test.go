package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestExportRedactionPreservesIdentityAndTotals(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := db.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	event := &model.UsageEvent{
		EventID: "event-id", IdentityVersion: model.IdentityVersion, IdentityStrategy: "native_event", IdentityScope: "session",
		ContentSHA256: "content-hash", ParserVersion: "test-v1", EventGranularity: "request",
		Channel: "codex", SourceProduct: "codex-cli", ModelNormalized: "unknown", ModelResolution: model.ModelResolutionUnknown, ModelIsFallback: true,
		TimestampMs: 1_700_000_000_000, SessionKey: "session-key", ProjectPath: "/private/project", SourceFile: "/private/session.jsonl",
		InputTokens: 5, TotalTokens: 5, ImportedAtMs: 1, UpdatedAtMs: 1,
	}
	if _, err := source.UpsertEvent(event); err != nil {
		t.Fatal(err)
	}
	if err := source.StartImportRun("run"); err != nil {
		t.Fatal(err)
	}
	if err := source.FinishImportRunWithStatus("run", 1, 1, 0, 0, 1, "completed_with_warnings", "private warning"); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()

	exportPath := filepath.Join(t.TempDir(), "export.aldb")
	if _, err := exportDatabase(sourcePath, exportPath, true); err != nil {
		t.Fatal(err)
	}
	exported, err := db.OpenReadOnlyV3(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	defer exported.Close()
	var eventID, sessionKey, contentHash string
	var project, sourceFile, warning sql.NullString
	var total int64
	if err := exported.Conn().QueryRow(`SELECT event_id, session_key, content_sha256, project_path, source_file, total_tokens FROM usage_events`).Scan(&eventID, &sessionKey, &contentHash, &project, &sourceFile, &total); err != nil {
		t.Fatal(err)
	}
	if eventID != event.EventID || sessionKey != event.SessionKey || contentHash != event.ContentSHA256 || total != event.TotalTokens {
		t.Fatalf("redaction changed identity/totals: %q %q %q %d", eventID, sessionKey, contentHash, total)
	}
	if project.Valid || sourceFile.Valid {
		t.Fatalf("redacted export retained paths: project=%v source=%v", project, sourceFile)
	}
	if err := exported.Conn().QueryRow(`SELECT error FROM import_runs WHERE id='run'`).Scan(&warning); err != nil {
		t.Fatal(err)
	}
	if warning.Valid {
		t.Fatalf("redacted export retained import warning: %q", warning.String)
	}
}

func TestExportCanKeepPrivateFieldsWhenConfigured(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := db.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	event := &model.UsageEvent{
		EventID: "event", IdentityVersion: model.IdentityVersion, IdentityStrategy: "native_event", IdentityScope: "session",
		ContentSHA256: "hash", EventGranularity: "request", Channel: "codex", SourceProduct: "codex-cli",
		ModelNormalized: "unknown", TimestampMs: 1, SessionKey: "session", ProjectPath: "/private/project", SourceFile: "/private/source",
		TotalTokens: 0, ImportedAtMs: 1, UpdatedAtMs: 1,
	}
	if _, err := source.UpsertEvent(event); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	exportPath := filepath.Join(t.TempDir(), "unredacted.aldb")
	if _, err := exportDatabase(sourcePath, exportPath, false); err != nil {
		t.Fatal(err)
	}
	exported, err := db.OpenReadOnlyV3(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	defer exported.Close()
	var project, sourceFile string
	if err := exported.Conn().QueryRow(`SELECT project_path, source_file FROM usage_events`).Scan(&project, &sourceFile); err != nil {
		t.Fatal(err)
	}
	if project != event.ProjectPath || sourceFile != event.SourceFile {
		t.Fatalf("unredacted export lost fields: %q %q", project, sourceFile)
	}
}

func TestExportRequiresExistingV3SourceAndPreservesOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "existing.aldb")
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exportDatabase(filepath.Join(t.TempDir(), "missing.db"), output, true); err == nil {
		t.Fatal("expected missing source error")
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "keep" {
		t.Fatalf("failed export changed existing output: %q err=%v", data, err)
	}
}

func TestExportRejectsMalformedV3SourceAndPreservesOutput(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "malformed.db")
	conn, err := sql.Open("sqlite3", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
        CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
        INSERT INTO meta VALUES ('schema_version', '3');
        INSERT INTO meta VALUES ('identity_version', '2');
    `); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	output := filepath.Join(t.TempDir(), "existing.aldb")
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exportDatabase(sourcePath, output, false); err == nil {
		t.Fatal("expected malformed v3 source error")
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "keep" {
		t.Fatalf("failed export changed existing output: %q err=%v", data, err)
	}
}

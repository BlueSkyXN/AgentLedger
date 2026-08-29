package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func TestCursorImportIsIdempotentAndKeepsProviderUnknown(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "logs", "run-a", "window1", "exthost", "anysphere.cursor-agent-exec")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Cursor Agent Exec.log")
	line := `2026-08-20 09:50:34.089 [info] Setting token details for client token ring {"action":"userMessageAction","cacheReadTokens":30,"cacheWriteTokens":20,"inputTokens":100,"maxTokens":1000000,"modelName":"gpt-5.6-sol","outputTokens":40,"usedTokens":140}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter := adapters.NewCursorAdapter()
	records, err := adapter.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records = adapter.PostProcessRecords(records)
	if len(records) != 1 {
		t.Fatalf("expected one Cursor record, got %d", len(records))
	}

	database, err := db.Open(filepath.Join(t.TempDir(), "cursor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	added, updated, skipped, rejected, warnings := importParsedRecords(database, adapter.Name(), records)
	if added != 1 || updated != 0 || skipped != 0 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("first import counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}
	added, updated, skipped, rejected, warnings = importParsedRecords(database, adapter.Name(), records)
	if added != 0 || updated != 0 || skipped != 1 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("second import counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}

	var channel, sourceProduct, provider, accounting string
	var input, output, cacheRead, cacheWrite, total int64
	if err := database.Conn().QueryRow(`
		SELECT channel, source_product, provider, token_accounting_method,
		       input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens
		FROM usage_events
	`).Scan(&channel, &sourceProduct, &provider, &accounting, &input, &output, &cacheRead, &cacheWrite, &total); err != nil {
		t.Fatal(err)
	}
	if channel != "cursor" || sourceProduct != "cursor-agent-exec" || provider != "unknown" || accounting != "cursor_agent_exec_usage" {
		t.Fatalf("unexpected Cursor source facts: channel=%q source=%q provider=%q accounting=%q", channel, sourceProduct, provider, accounting)
	}
	if input != 50 || output != 40 || cacheRead != 30 || cacheWrite != 20 || total != 140 {
		t.Fatalf("unexpected Cursor token facts: input=%d output=%d cache_read=%d cache_write=%d total=%d", input, output, cacheRead, cacheWrite, total)
	}
}

func TestCursorImportRemainsIdempotentAfterLogRelocation(t *testing.T) {
	root := t.TempDir()
	line := `2026-08-20 09:50:34.089 [info] Setting token details for client token ring {"action":"resumeAction","cacheReadTokens":80,"cacheWriteTokens":0,"inputTokens":100,"maxTokens":1000000,"modelName":"grok-4.6","outputTokens":10,"usedTokens":110}`
	write := func(run, name string) string {
		dir := filepath.Join(root, "logs", run, "window1", "exthost", "anysphere.cursor-agent-exec")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	paths := []string{
		write("run-a", "Cursor Agent Exec.log"),
		write("run-b", "Cursor Agent Exec.1.log"),
	}

	database, err := db.Open(filepath.Join(t.TempDir(), "cursor-relocation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for index, path := range paths {
		adapter := adapters.NewCursorAdapter()
		records, err := adapter.ParseFile(path)
		if err != nil || len(records) != 1 {
			t.Fatalf("parse relocated file %d: records=%d err=%v", index, len(records), err)
		}
		added, updated, skipped, rejected, warnings := importParsedRecords(database, adapter.Name(), records)
		if index == 0 && (added != 1 || updated != 0 || skipped != 0 || rejected != 0 || len(warnings) != 0) {
			t.Fatalf("first relocated import counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
		}
		if index == 1 && (added != 0 || updated != 0 || skipped != 1 || rejected != 0 || len(warnings) != 0) {
			t.Fatalf("second relocated import counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
		}
	}
}

func TestCursorLiveCorpusImportsIdempotentlyIntoTemporaryDatabase(t *testing.T) {
	if os.Getenv("CURSOR_LIVE_TEST") != "1" {
		t.Skip("set CURSOR_LIVE_TEST=1 to validate live Cursor import against a temporary database")
	}
	adapter := adapters.NewCursorAdapter()
	files, err := adapter.Discover(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no Cursor Agent Exec logs discovered")
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "cursor-live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cutoff := time.Now().Add(time.Hour)
	first := importAdapterFiles(database, adapter, files, cutoff)
	if first.added == 0 || first.updated != 0 || first.skipped != 0 || first.rejected != 0 || len(first.warnings) != 0 {
		t.Fatalf("unexpected first live import: %+v", first)
	}
	var events, tokens int64
	if err := database.Conn().QueryRow(`SELECT COUNT(*), COALESCE(SUM(total_tokens), 0) FROM usage_events WHERE channel='cursor'`).Scan(&events, &tokens); err != nil {
		t.Fatal(err)
	}
	if events != int64(first.added) || tokens <= 0 {
		t.Fatalf("unexpected live database totals: events=%d added=%d tokens=%d", events, first.added, tokens)
	}

	second := importAdapterFiles(database, adapter, files, cutoff)
	if second.added != 0 || second.updated != 0 || second.skipped != int(events) || second.rejected != 0 || len(second.warnings) != 0 {
		t.Fatalf("unexpected second live import: %+v", second)
	}
	t.Logf("Cursor live import: files=%d events=%d tokens=%d second_import_skipped=%d", len(files), events, tokens, second.skipped)
}

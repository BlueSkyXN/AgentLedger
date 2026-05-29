package report

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func reportTestDB(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.Conn().Exec(`INSERT INTO usage_events (
		event_id, dedupe_key, dedupe_strategy,
		channel, provider, model_raw, model_normalized, timestamp_ms, session_id, message_id,
		input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, reasoning_tokens, total_tokens,
		imported_at_ms, updated_at_ms
	) VALUES
		('claude-a', 'claude-a', 'message_id', 'claude', 'anthropic', 'claude-sonnet', 'claude-sonnet', 1, 's1', 'm1', 10, 5, 3, 7, 0, 25, 1, 1),
		('claude-b', 'claude-b', 'message_id', 'claude', 'anthropic', 'claude-sonnet', 'claude-sonnet', 2, 's1', 'm2', 20, 8, 4, 9, 0, 41, 1, 1)`)
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}
	return database
}

func TestGenerateGroupedJSONIncludesCacheTokens(t *testing.T) {
	database := reportTestDB(t)
	output := captureReportOutput(t, func() error {
		return Generate(database.Conn(), "models", Filters{Channel: "claude"}, true)
	})
	var rows []ReportRow
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, output)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.TotalTokens != 66 || row.InputTokens != 30 || row.OutputTokens != 13 || row.CacheCreationTokens != 7 || row.CacheReadTokens != 16 {
		t.Fatalf("unexpected token breakdown: %+v", row)
	}
}

func TestGenerateGroupedTableShowsCacheColumns(t *testing.T) {
	database := reportTestDB(t)
	output := captureReportOutput(t, func() error {
		return Generate(database.Conn(), "models", Filters{Channel: "claude"}, false)
	})
	for _, want := range []string{"Cache Create", "Cache Read", "Reasoning", "claude-sonnet"} {
		if !strings.Contains(output, want) {
			t.Fatalf("report output missing %q:\n%s", want, output)
		}
	}
}

func TestGenerateTimeBreakdownJSON(t *testing.T) {
	database := reportTestDB(t)
	output := captureReportOutput(t, func() error {
		return Generate(database.Conn(), "daily", Filters{By: "model"}, true)
	})
	var rows []TimeBreakdownRow
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, output)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.Bucket != "1970-01-01" || row.Label != "claude-sonnet" || row.TotalTokens != 66 || row.CacheReadTokens != 16 {
		t.Fatalf("unexpected time breakdown: %+v", row)
	}
}

func TestGenerateTimeBreakdownRejectsRawDimension(t *testing.T) {
	database := reportTestDB(t)
	if err := Generate(database.Conn(), "daily", Filters{By: "raw"}, true); err == nil {
		t.Fatal("expected invalid time breakdown")
	}
}

func TestGenerateRejectsInvalidDateFilters(t *testing.T) {
	database := reportTestDB(t)
	if err := Generate(database.Conn(), "daily", Filters{Since: "2026/05/01"}, true); err == nil || !strings.Contains(err.Error(), "since must use YYYY-MM-DD") {
		t.Fatalf("expected invalid since error, got %v", err)
	}
	if err := Generate(database.Conn(), "daily", Filters{Until: "tomorrow"}, true); err == nil || !strings.Contains(err.Error(), "until must use YYYY-MM-DD") {
		t.Fatalf("expected invalid until error, got %v", err)
	}
}

func captureReportOutput(t *testing.T, run func() error) string {
	t.Helper()
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writer
	runErr := run()
	_ = writer.Close()
	os.Stdout = oldStdout
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return buf.String()
}

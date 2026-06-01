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
	for _, want := range []string{"Cache Create", "Cache Read", "Reasoning", "Recorded Cost(USD)", "claude-sonnet"} {
		if !strings.Contains(output, want) {
			t.Fatalf("report output missing %q:\n%s", want, output)
		}
	}
}

func TestGenerateGroupedEstimatedCostJSON(t *testing.T) {
	database := reportTestDB(t)
	pricingPath := writeReportPricingProfile(t)
	output := captureReportOutput(t, func() error {
		return Generate(database.Conn(), "models", Filters{Channel: "claude", CostMode: "estimated", PricingPath: pricingPath}, true)
	})
	var rows []ReportRow
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, output)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.EstimatedCostMicroUSD == nil || *row.EstimatedCostMicroUSD != 212 {
		t.Fatalf("expected estimated cost 212 micro USD, got %+v", row)
	}
	if row.Pricing == nil || row.Pricing.PricedEvents != 2 || row.Pricing.TotalEvents != 2 || row.Pricing.CoverageRatio != 1 {
		t.Fatalf("unexpected pricing coverage: %+v", row.Pricing)
	}
}

func TestGenerateGroupedEstimatedCostTable(t *testing.T) {
	database := reportTestDB(t)
	pricingPath := writeReportPricingProfile(t)
	output := captureReportOutput(t, func() error {
		return Generate(database.Conn(), "models", Filters{Channel: "claude", CostMode: "both", PricingPath: pricingPath}, false)
	})
	for _, want := range []string{"Recorded Cost(USD)", "Estimated Cost(USD)", "Pricing Coverage", "100.0%"} {
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

func TestGenerateRejectsInvalidCostMode(t *testing.T) {
	database := reportTestDB(t)
	if err := Generate(database.Conn(), "models", Filters{CostMode: "blended"}, true); err == nil || !strings.Contains(err.Error(), "invalid cost mode") {
		t.Fatalf("expected invalid cost mode error, got %v", err)
	}
}

func writeReportPricingProfile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pricing.json")
	data := `{
	  "schema_version": 1,
	  "id": "report-test-pricing",
	  "currency": "USD",
	  "unit": "usd_per_1m_tokens",
	  "defaults": {"reasoning_policy": "included_in_output", "cache_write_assumption": "treat_as_input", "confidence": "exact"},
	  "rules": [
	    {
	      "id": "anthropic:claude-sonnet",
	      "provider": "anthropic",
	      "channel": "*",
	      "model_patterns": ["claude-sonnet"],
	      "priority": 10,
	      "basis": "api_equivalent",
	      "rates": {"input": 2, "cached_input": 0.5, "output": 10},
	      "confidence": "exact"
	    }
	  ]
	}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write pricing profile: %v", err)
	}
	return path
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

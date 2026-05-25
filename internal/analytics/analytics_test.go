package analytics

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func testDB(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.Conn().Exec(`INSERT INTO devices (device_id, device_name, hostname, os, arch, app_version, created_at_ms, last_seen_at_ms)
		VALUES ('dev1', 'Laptop', 'host', 'darwin', 'arm64', '0.1.0', 1, 1)`)
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	_, err = database.Conn().Exec(`INSERT INTO usage_events (
		event_fingerprint, dedupe_key, fingerprint_strategy, origin_device_id, first_seen_device_id, last_seen_device_id,
		agent, provider, source_channel, source_kind, model_raw, model_normalized, model_provider, timestamp_ms,
		session_id, message_id, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, reasoning_tokens,
		total_tokens, cost_usd, raw_usage_json, created_at_ms, updated_at_ms
	) VALUES
		('fp1', 'fp1', 'message_id', 'dev1', 'dev1', 'dev1', 'codex', 'openai', 'local', 'log', 'gpt-5', 'gpt-5', 'openai', ?, 's1', 'm1', 100, 50, 10, 5, 20, 185, 0.1, '{"secret":"hidden"}', 1, 1),
		('fp2', 'fp2', 'message_id', 'dev1', 'dev1', 'dev1', 'claude', 'anthropic', 'local', 'log', 'claude-sonnet', 'claude-sonnet', 'anthropic', ?, 's2', 'm2', 200, 80, 0, 0, 0, 280, 0.2, '{"secret":"hidden"}', 2, 2)`, base, base+86400000)
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}
	_, err = database.Conn().Exec(`INSERT INTO import_runs (id, device_id, started_at_ms, finished_at_ms, status, files_scanned, events_added, events_skipped)
		VALUES ('run1', 'dev1', ?, ?, 'completed', 2, 2, 0)`, base, base+1000)
	if err != nil {
		t.Fatalf("insert import run: %v", err)
	}
	return database
}

func TestBuildSummary(t *testing.T) {
	database := testDB(t)
	summary, err := BuildSummary(database.Conn(), Filters{})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.TotalEvents != 2 || summary.TotalTokens != 465 || summary.TotalDevices != 1 || summary.ImportRuns != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestBuildTimeseriesAndBreakdown(t *testing.T) {
	database := testDB(t)
	series, err := BuildTimeseries(database.Conn(), "daily", Filters{})
	if err != nil {
		t.Fatalf("timeseries: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("expected 2 daily rows, got %d", len(series))
	}
	models, err := BuildBreakdown(database.Conn(), "model", Filters{})
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	if len(models) != 2 || models[0].TotalTokens != 280 {
		t.Fatalf("unexpected model breakdown: %+v", models)
	}
}

func TestSessionsImportRunsAndEvents(t *testing.T) {
	database := testDB(t)
	sessions, err := BuildSessions(database.Conn(), Filters{}, 10)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].Label != "s2" {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
	runs, err := ListImportRuns(database.Conn(), 10)
	if err != nil {
		t.Fatalf("import runs: %v", err)
	}
	if len(runs) != 1 || runs[0].EventsAdded != 2 {
		t.Fatalf("unexpected runs: %+v", runs)
	}
	events, err := ListEvents(database.Conn(), Filters{}, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 2 || events[0].EventFingerprint != "fp2" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestInvalidAnalyticsOptions(t *testing.T) {
	database := testDB(t)
	if _, err := BuildTimeseries(database.Conn(), "hourly", Filters{}); err == nil {
		t.Fatal("expected invalid bucket error")
	}
	if _, err := BuildBreakdown(database.Conn(), "raw", Filters{}); err == nil {
		t.Fatal("expected invalid breakdown error")
	}
}

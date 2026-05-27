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
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	_, err = database.Conn().Exec(`INSERT INTO usage_events (
		event_id, dedupe_key, dedupe_strategy,
		channel, provider, model_raw, model_normalized, timestamp_ms, session_id, message_id,
		input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, reasoning_tokens, total_tokens,
		request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
		recorded_cost_usd, raw_usage_json, imported_at_ms, updated_at_ms
	) VALUES
		('fp1', 'fp1', 'message_id', 'codex', 'openai', 'gpt-5', 'gpt-5', ?, 's1', 'm1', 100, 50, 10, 5, 20, 185, ?, ?, ?, 3000, 500, 2500, 20.0, 0.1, '{"secret":"hidden"}', 1, 1),
		('fp2', 'fp2', 'message_id', 'claude', 'anthropic', 'claude-sonnet', 'claude-sonnet', ?, 's2', 'm2', 200, 80, 0, 0, 0, 280, NULL, NULL, NULL, NULL, NULL, NULL, NULL, 0.2, '{"secret":"hidden"}', 2, 2)`,
		base, base, base+500, base+3000, base+86400000)
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}
	_, err = database.Conn().Exec(`INSERT INTO import_runs (id, started_at_ms, finished_at_ms, status, files_scanned, events_added, events_updated, events_skipped)
		VALUES ('run1', ?, ?, 'completed', 2, 2, 1, 0)`, base, base+1000)
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
	if summary.TotalEvents != 2 || summary.TotalTokens != 465 || summary.ImportRuns != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.AvgOutputTPS == nil || *summary.AvgOutputTPS != 20 {
		t.Fatalf("expected avg tps from timed rows, got %+v", summary.AvgOutputTPS)
	}
}

func TestBuildTimeseriesBreakdownAndFilters(t *testing.T) {
	database := testDB(t)
	series, err := BuildTimeseries(database.Conn(), "daily", Filters{})
	if err != nil {
		t.Fatalf("timeseries: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("expected 2 daily rows, got %d", len(series))
	}
	models, err := BuildBreakdown(database.Conn(), "model", Filters{Channel: "claude"})
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	if len(models) != 1 || models[0].Label != "claude-sonnet" || models[0].TotalTokens != 280 {
		t.Fatalf("unexpected model breakdown: %+v", models)
	}
}

func TestBuildTimeseriesUsesReportTimezone(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	lateUTC := time.Date(2026, 5, 1, 23, 30, 0, 0, time.UTC).UnixMilli()
	earlyUTC := time.Date(2026, 5, 2, 0, 30, 0, 0, time.UTC).UnixMilli()
	_, err = database.Conn().Exec(`INSERT INTO usage_events (
		event_id, dedupe_key, dedupe_strategy, channel, provider, model_raw, model_normalized, timestamp_ms,
		input_tokens, output_tokens, total_tokens, imported_at_ms, updated_at_ms
	) VALUES
		('tz-a', 'tz-a', 'message_id', 'claude', 'anthropic', 'claude-sonnet', 'claude-sonnet', ?, 10, 5, 15, 1, 1),
		('tz-b', 'tz-b', 'message_id', 'claude', 'anthropic', 'claude-sonnet', 'claude-sonnet', ?, 20, 8, 28, 1, 1)`,
		lateUTC, earlyUTC)
	if err != nil {
		t.Fatalf("insert timezone events: %v", err)
	}

	series, err := BuildTimeseries(database.Conn(), "daily", Filters{Channel: "claude", Timezone: "+08:00"})
	if err != nil {
		t.Fatalf("timeseries: %v", err)
	}
	if len(series) != 1 || series[0].Label != "2026-05-02" || series[0].TotalTokens != 43 {
		t.Fatalf("unexpected timezone series: %+v", series)
	}
}

func TestSessionsImportRunsEventsSlowAndOptions(t *testing.T) {
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
	if len(runs) != 1 || runs[0].EventsAdded != 2 || runs[0].EventsUpdated != 1 {
		t.Fatalf("unexpected runs: %+v", runs)
	}
	events, err := ListEvents(database.Conn(), Filters{}, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 2 || events[0].EventID != "fp2" {
		t.Fatalf("unexpected events: %+v", events)
	}
	slow, err := BuildSlow(database.Conn(), "output_tps", Filters{}, 10)
	if err != nil {
		t.Fatalf("slow: %v", err)
	}
	if len(slow) != 1 || slow[0].OutputTPS == nil {
		t.Fatalf("unexpected slow rows: %+v", slow)
	}
	options, err := BuildFilterOptions(database.Conn())
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if len(options.Channels) != 2 || len(options.Models) != 2 {
		t.Fatalf("unexpected options: %+v", options)
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
	if _, err := BuildSlow(database.Conn(), "raw", Filters{}, 10); err == nil {
		t.Fatal("expected invalid slow sort error")
	}
}

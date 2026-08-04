package analytics

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestSummaryFiltersAndUnavailablePricing(t *testing.T) {
	database := analyticsTestDatabase(t)
	defer database.Close()
	insertAnalyticsEvent(t, database, "e1", "s1", "codex", "codex-cli", "openai", "model-a", 10, atUTC(2026, 3, 7, 23, 30), "/private/project-a")
	insertAnalyticsEvent(t, database, "e2", "s2", "claude", "claude-code", "anthropic", "model-b", 20, atUTC(2026, 3, 8, 8, 30), "/private/project-b")

	summary, err := BuildSummary(database.Conn(), Filters{
		Channel: "codex", Timezone: "America/New_York",
		PricingPath: filepath.Join(t.TempDir(), "missing.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalEvents != 1 || summary.TotalSessions != 1 || summary.TotalTokens != 10 {
		t.Fatalf("unexpected filtered summary: %+v", summary)
	}
	if summary.FirstDate == nil || *summary.FirstDate != "2026-03-07" {
		t.Fatalf("unexpected first date: %+v", summary.FirstDate)
	}
	if summary.EstimatedCostUSD != nil || summary.Pricing == nil || summary.Pricing.Status != "unavailable" || summary.Pricing.ErrorCode != "pricing_profile_invalid" {
		t.Fatalf("invalid configured pricing should not fail usage query: %+v", summary)
	}
}

func TestTimeseriesUsesHistoricalIANAOffset(t *testing.T) {
	database := analyticsTestDatabase(t)
	defer database.Close()
	// 04:30 UTC is still the previous calendar date in New York before DST.
	insertAnalyticsEvent(t, database, "dst-a", "s1", "codex", "codex-cli", "openai", "unknown", 1, atUTC(2026, 3, 8, 4, 30), "")
	// 04:30 UTC in July uses UTC-4 and therefore belongs to the same date.
	insertAnalyticsEvent(t, database, "dst-b", "s2", "codex", "codex-cli", "openai", "unknown", 1, atUTC(2026, 7, 8, 4, 30), "")
	rows, err := BuildTimeseries(database.Conn(), "daily", Filters{Timezone: "America/New_York", CostMode: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Label != "2026-03-07" || rows[1].Label != "2026-07-08" {
		t.Fatalf("historical timezone buckets are wrong: %+v", rows)
	}
}

func TestSessionsPrimaryModelPaginationAndFilters(t *testing.T) {
	database := analyticsTestDatabase(t)
	defer database.Close()
	timestamp := atUTC(2026, 5, 1, 12, 0)
	insertAnalyticsEvent(t, database, "s1-a", "session-one", "codex", "codex-cli", "openai", "z-model", 10, timestamp, "/work/project")
	insertAnalyticsEvent(t, database, "s1-b", "session-one", "codex", "codex-cli", "openai", "a-model", 10, timestamp+1000, "/work/project")
	insertAnalyticsEvent(t, database, "s2", "session-two", "claude", "claude-code", "anthropic", "claude-model", 5, timestamp+2000, "/work/other")

	page, err := BuildSessions(database.Conn(), Filters{Timezone: "UTC", CostMode: "none"}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 1 || page.Limit != 1 || page.Offset != 0 {
		t.Fatalf("unexpected page: %+v", page)
	}
	filtered, err := BuildSessions(database.Conn(), Filters{Timezone: "UTC", SourceProduct: "codex-cli", CostMode: "none"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 {
		t.Fatalf("source filter failed: %+v", filtered)
	}
	session := filtered.Items[0]
	if session.SessionKey != "session-one" || session.PrimaryModel != "a-model" || session.ModelCount != 2 || session.EventCount != 2 || session.TotalTokens != 20 {
		t.Fatalf("session aggregation or primary-model tie break failed: %+v", session)
	}
	if session.FirstDate != "2026-05-01" || session.LastDate != "2026-05-01" {
		t.Fatalf("unexpected filtered dates: %+v", session)
	}
}

func TestEventsPaginationAndFilterOptions(t *testing.T) {
	database := analyticsTestDatabase(t)
	defer database.Close()
	insertAnalyticsEvent(t, database, "e1", "s1", "workbuddy", "workbuddy", "workbuddy", "m1", 1, atUTC(2026, 1, 1, 0, 0), "/a/project")
	insertAnalyticsEvent(t, database, "e2", "s2", "codex", "codex-cli", "openai", "m2", 2, atUTC(2026, 1, 2, 0, 0), "/b/other")
	page, err := ListEvents(database.Conn(), Filters{Timezone: "UTC", Provider: "openai", CostMode: "none"}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].EventID != "e2" || page.Items[0].SessionKey != "s2" {
		t.Fatalf("event pagination/filter failed: %+v", page)
	}
	options, err := BuildFilterOptions(database.Conn())
	if err != nil {
		t.Fatal(err)
	}
	if len(options.SourceProducts) != 2 || len(options.Sessions) != 2 || len(options.Projects) != 2 {
		t.Fatalf("unexpected filter options: %+v", options)
	}
}

func analyticsTestDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "analytics.db"))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func insertAnalyticsEvent(t *testing.T, database *db.Database, id, session, channel, source, provider, modelID string, total, timestamp int64, project string) {
	t.Helper()
	event := &model.UsageEvent{
		EventID: id, IdentityVersion: model.IdentityVersion, IdentityStrategy: "native_event", IdentityScope: "session",
		ContentSHA256: "content-" + id, ParserVersion: "test-v1", EventGranularity: "request",
		Channel: channel, SourceProduct: source, Provider: provider,
		ModelRaw: modelID, ModelNormalized: modelID, ModelResolution: model.ModelResolutionDirectEvent,
		TimestampMs: timestamp, SessionKey: session, SessionID: session, ProjectPath: project,
		InputTokens: total, TotalTokens: total, ImportedAtMs: timestamp, UpdatedAtMs: timestamp,
	}
	if _, err := database.UpsertEvent(event); err != nil {
		t.Fatal(err)
	}
}

func atUTC(year int, month time.Month, day, hour, minute int) int64 {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC).UnixMilli()
}

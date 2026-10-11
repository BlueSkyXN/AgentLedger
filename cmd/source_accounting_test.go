package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
)

func writeAccountingFixture(t *testing.T, root, relative, content string) string {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func accountingDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func assertAccountingTotals(t *testing.T, database *db.Database, count int, total int64) {
	t.Helper()
	var gotCount int
	var gotTotal int64
	if err := database.Conn().QueryRow(`SELECT COUNT(*), COALESCE(SUM(total_tokens), 0) FROM usage_events`).Scan(&gotCount, &gotTotal); err != nil {
		t.Fatal(err)
	}
	if gotCount != count || gotTotal != total {
		t.Fatalf("events/tokens=%d/%d, want %d/%d", gotCount, gotTotal, count, total)
	}
}

func importAccountingFixtures(t *testing.T, database *db.Database, adapter adapters.Adapter, paths ...string) importAdapterResult {
	t.Helper()
	result := importAdapterFiles(database, adapter, paths, time.Now().Add(time.Hour))
	if result.rejected != 0 || len(result.warnings) != 0 {
		t.Fatalf("rejected=%d warnings=%v", result.rejected, result.warnings)
	}
	return result
}

func claudeAccountingLine(session, timestamp string, tokens int) string {
	return fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"id":"reused-id","model":"claude-sonnet-4","usage":{"input_tokens":%d,"output_tokens":0}}}`, session, timestamp, tokens)
}

func TestClaudeRequestlessCallsSurviveBatchAndStagedImports(t *testing.T) {
	for _, sameSession := range []bool{false, true} {
		for _, staged := range []bool{false, true} {
			t.Run(fmt.Sprintf("same-session=%v/staged=%v", sameSession, staged), func(t *testing.T) {
				root := t.TempDir()
				session := "session-b"
				if sameSession {
					session = "session-a"
				}
				first := writeAccountingFixture(t, root, "projects/p/a.jsonl", claudeAccountingLine("session-a", "2026-01-01T00:00:00Z", 10))
				second := writeAccountingFixture(t, root, "projects/p/b.jsonl", claudeAccountingLine(session, "2026-01-01T00:01:00Z", 20))
				database := accountingDatabase(t)
				if staged {
					importAccountingFixtures(t, database, adapters.NewClaudeAdapter(), first)
				}
				importAccountingFixtures(t, database, adapters.NewClaudeAdapter(), first, second)
				assertAccountingTotals(t, database, 2, 30)
				result := importAccountingFixtures(t, database, adapters.NewClaudeAdapter(), first, second)
				if result.added != 0 || result.updated != 0 {
					t.Fatalf("repeat writes: %+v", result)
				}
			})
		}
	}
}

func TestClaudeRequestlessUpgradeKeepsOldUsageWithoutDuplicate(t *testing.T) {
	root := t.TempDir()
	first := writeAccountingFixture(t, root, "projects/p/a.jsonl", claudeAccountingLine("session-a", "2026-01-01T00:00:00Z", 10))
	second := writeAccountingFixture(t, root, "projects/p/b.jsonl", claudeAccountingLine("session-a", "2026-01-01T00:01:00Z", 20))
	database := accountingDatabase(t)
	records, err := adapters.NewClaudeAdapter().ParseFile(first)
	if err != nil {
		t.Fatal(err)
	}
	records[0].ParserVersion = "claude-v1"
	records[0].IdentitySubkey = records[0].MessageID
	if added, _, _, rejected, _ := importParsedRecords(database, "claude", records); added != 1 || rejected != 0 {
		t.Fatal("legacy insert failed")
	}
	_, legacyID, _, _, err := fingerprint.ComputeIdentity(records[0])
	if err != nil {
		t.Fatal(err)
	}
	var storedID string
	if err := database.Conn().QueryRow(`SELECT event_id FROM usage_events`).Scan(&storedID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`UPDATE usage_events SET event_id=?, parser_version='claude-v1' WHERE event_id=?`, legacyID, storedID); err != nil {
		t.Fatal(err)
	}
	importAccountingFixtures(t, database, adapters.NewClaudeAdapter(), second)
	assertAccountingTotals(t, database, 2, 30)
	importAccountingFixtures(t, database, adapters.NewClaudeAdapter(), first, second)
	assertAccountingTotals(t, database, 2, 30)
}

func copilotShutdownLine(id, session, timestamp string, tokens int) string {
	return fmt.Sprintf(`{"type":"session.shutdown","id":%q,"timestamp":%q,"data":{"sessionId":%q,"modelMetrics":{"gpt-4.1":{"usage":{"inputTokens":%d,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0}}}}}`, id, timestamp, session, tokens)
}

func copilotOtelLine(id, session, timestamp string, tokens int) string {
	return fmt.Sprintf(`{"traceId":%q,"spanId":"span","timestamp":%q,"attributes":{"gen_ai.conversation.id":%q,"gen_ai.response.model":"gpt-4.1","gen_ai.usage.input_tokens":%d,"gen_ai.usage.output_tokens":0,"gen_ai.usage.total_tokens":%d}}`, id, timestamp, session, tokens, tokens)
}

func TestCopilotSourceCoverageAndStagedSummary(t *testing.T) {
	t.Setenv("COPILOT_OTEL_FILE_EXPORTER_PATH", "")
	root := filepath.Join(t.TempDir(), ".copilot")
	old := writeAccountingFixture(t, root, "session-state/old/events.jsonl", copilotShutdownLine("old-end", "old", "2026-01-01T00:01:00Z", 100))
	otel := writeAccountingFixture(t, root, "otel/usage.jsonl", "")
	adapter := adapters.NewCopilotAdapter()
	files, err := adapter.Discover([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	database := accountingDatabase(t)
	importAccountingFixtures(t, database, adapter, files...)
	assertAccountingTotals(t, database, 1, 100)
	writeAccountingFixture(t, root, "otel/usage.jsonl", copilotOtelLine("new-call", "new", "2026-01-02T00:00:10Z", 50))
	importAccountingFixtures(t, database, adapter, old, otel)
	assertAccountingTotals(t, database, 2, 150)
	newSummary := writeAccountingFixture(t, root, "session-state/new/events.jsonl", `{"type":"session.start","timestamp":"2026-01-02T00:00:00Z","data":{"sessionId":"new"}}`+"\n"+copilotShutdownLine("new-end", "new", "2026-01-02T00:01:00Z", 50))
	importAccountingFixtures(t, database, adapter, old, otel, newSummary)
	assertAccountingTotals(t, database, 2, 150)
	writeAccountingFixture(t, root, "otel/usage.jsonl", copilotOtelLine("new-call", "new", "2026-01-02T00:00:10Z", 50)+"\n"+copilotOtelLine("resumed-call", "new", "2026-01-02T00:02:00Z", 20))
	importAccountingFixtures(t, database, adapter, old, otel, newSummary)
	assertAccountingTotals(t, database, 3, 170)
	result := importAccountingFixtures(t, database, adapter, old, otel, newSummary)
	if result.added != 0 || result.updated != 0 {
		t.Fatalf("repeat writes: %+v", result)
	}
}

func TestCopilotRejectedSummaryDoesNotDeleteStoredOtel(t *testing.T) {
	root := t.TempDir()
	otel := writeAccountingFixture(t, root, "otel/usage.jsonl", copilotOtelLine("call", "s", "2026-01-01T00:00:10Z", 50))
	summary := writeAccountingFixture(t, root, "session-state/s/events.jsonl", copilotShutdownLine("end", "s", "2026-01-01T00:01:00Z", 50))
	database := accountingDatabase(t)
	importAccountingFixtures(t, database, adapters.NewCopilotAdapter(), otel)
	records, err := adapters.NewCopilotAdapter().ParseFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	records[0].SupersededRecords, err = adapters.NewCopilotAdapter().ParseFile(otel)
	if err != nil {
		t.Fatal(err)
	}
	records[0].TotalTokens++
	_, _, _, rejected, _ := importParsedRecords(database, "copilot", records)
	if rejected != 1 {
		t.Fatalf("rejected=%d", rejected)
	}
	assertAccountingTotals(t, database, 1, 50)
}

func TestCopilotInclusiveBucketsAndSegmentedShutdown(t *testing.T) {
	line := `{"type":"session.shutdown","id":"first","timestamp":"2026-01-01T00:00:00Z","data":{"sessionId":"s","modelMetrics":{"gpt-4.1":{"usage":{"inputTokens":100,"outputTokens":20,"cacheReadTokens":30,"cacheWriteTokens":10,"reasoningTokens":5}}}}}`
	path := writeAccountingFixture(t, t.TempDir(), "session-state/s/events.jsonl", line+"\n"+copilotShutdownLine("second", "s", "2026-01-02T00:00:00Z", 50))
	database := accountingDatabase(t)
	importAccountingFixtures(t, database, adapters.NewCopilotAdapter(), path)
	assertAccountingTotals(t, database, 2, 170)
	importAccountingFixtures(t, database, adapters.NewCopilotAdapter(), path)
	assertAccountingTotals(t, database, 2, 170)
}

func TestClaudeAdvisorUsageAndRepeatedImport(t *testing.T) {
	line := `{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-01-01T00:00:00Z","message":{"id":"m","model":"claude-sonnet-4","usage":{"input_tokens":1,"output_tokens":2,"iterations":[{"type":"message","input_tokens":1,"output_tokens":2},{"type":"advisor_message","model":"claude-opus-4","input_tokens":10,"output_tokens":2},{"type":"advisor_message","model":"claude-opus-4","input_tokens":20,"output_tokens":3}]}}}`
	path := writeAccountingFixture(t, t.TempDir(), "projects/p/a.jsonl", line)
	database := accountingDatabase(t)
	importAccountingFixtures(t, database, adapters.NewClaudeAdapter(), path)
	assertAccountingTotals(t, database, 3, 38)
	importAccountingFixtures(t, database, adapters.NewClaudeAdapter(), path)
	assertAccountingTotals(t, database, 3, 38)
}

package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestImportPersistsStatisticsWithoutRawUsage(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	records := []*fingerprint.ParsedRecord{{
		Agent:           "claude",
		Provider:        "anthropic",
		Model:           "claude-test",
		DedupeID:        "message-1",
		TimestampMs:     1,
		InputTokens:     3,
		OutputTokens:    2,
		TotalTokens:     5,
		FingerprintJSON: `{"usage":{"input_tokens":3},"private":"must-not-persist"}`,
		RawSHA256:       "source-hash",
	}}
	added, updated, skipped, warnings := importParsedRecords(database, "claude", records)
	if added != 1 || updated != 0 || skipped != 0 || len(warnings) != 0 {
		t.Fatalf("unexpected import result added=%d updated=%d skipped=%d warnings=%v", added, updated, skipped, warnings)
	}

	var raw sql.NullString
	var input, output, total int64
	var rawSHA string
	if err := database.Conn().QueryRow(`
		SELECT raw_usage_json, input_tokens, output_tokens, total_tokens, raw_sha256
		FROM usage_events
	`).Scan(&raw, &input, &output, &total, &rawSHA); err != nil {
		t.Fatalf("read imported event: %v", err)
	}
	if raw.Valid || input != 3 || output != 2 || total != 5 || rawSHA != "source-hash" {
		t.Fatalf("unexpected stored event raw=%+v input=%d output=%d total=%d raw_sha=%q", raw, input, output, total, rawSHA)
	}
}

func TestApplyTimingFieldsDerivesOutputDurationAndTPS(t *testing.T) {
	event := &model.UsageEvent{OutputTokens: 42}
	rec := &fingerprint.ParsedRecord{
		TotalDurationMs: 12000,
		TTFTMs:          1500,
	}

	applyTimingFields(event, rec)

	if event.TotalDurationMs == nil || *event.TotalDurationMs != 12000 {
		t.Fatalf("expected total duration 12000, got %v", event.TotalDurationMs)
	}
	if event.OutputDurationMs == nil || *event.OutputDurationMs != 10500 {
		t.Fatalf("expected output duration 10500, got %v", event.OutputDurationMs)
	}
	if event.OutputTPS == nil || *event.OutputTPS != 4 {
		t.Fatalf("expected output TPS 4, got %v", event.OutputTPS)
	}
}

func TestConfigureImportAdapterAppliesCodexDuplicatePolicy(t *testing.T) {
	adapter := configureImportAdapter(adapters.NewCodexAdapter(), &config.AgentConfig{DuplicatePolicy: adapters.CodexDuplicatePolicyCCUsageCompatible})
	codexAdapter, ok := adapter.(*adapters.CodexAdapter)
	if !ok {
		t.Fatalf("expected Codex adapter, got %T", adapter)
	}

	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},"last_token_usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:03Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},"last_token_usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	records, err := codexAdapter.ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected ccusage policy to keep both records, got %d", len(records))
	}
}

func TestSummarizeImportWarnings(t *testing.T) {
	summary := summarizeImportWarnings([]string{"first", "second", "third", "fourth", "fifth", "sixth"})
	for _, want := range []string{"6 warning(s)", "first", "fifth", "1 more"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
	if strings.Contains(summary, "sixth") {
		t.Fatalf("summary should truncate detailed warnings: %s", summary)
	}
}

func TestSourceProductForClaudeRemainsClaudeCode(t *testing.T) {
	if got := sourceProductForAgent("claude"); got != "claude-code" {
		t.Fatalf("expected Claude default source product claude-code, got %q", got)
	}
}

func TestSourceProductForWorkBuddy(t *testing.T) {
	if got := sourceProductForAgent("workbuddy"); got != "workbuddy" {
		t.Fatalf("expected WorkBuddy source product, got %q", got)
	}
}

func TestImportUsesParsedNormalizedModelOverride(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	records := []*fingerprint.ParsedRecord{{
		Agent:           "workbuddy",
		Provider:        "custom",
		Model:           "deepseek-v4-pro-202606",
		ModelNormalized: "deepseek-v4-pro",
		ModelResolution: model.ModelResolutionThreadSettings,
		TimestampMs:     1,
		DedupeID:        "source-event",
		TotalTokens:     10,
	}}
	added, updated, skipped, warnings := importParsedRecords(database, "workbuddy", records)
	if added != 1 || updated != 0 || skipped != 0 || len(warnings) != 0 {
		t.Fatalf("unexpected import result added=%d updated=%d skipped=%d warnings=%v", added, updated, skipped, warnings)
	}

	var raw, normalized, provider, resolution string
	if err := database.Conn().QueryRow(`SELECT model_raw, model_normalized, provider, model_resolution FROM usage_events`).Scan(&raw, &normalized, &provider, &resolution); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if raw != "deepseek-v4-pro-202606" || normalized != "deepseek-v4-pro" || provider != "custom" || resolution != model.ModelResolutionThreadSettings {
		t.Fatalf("unexpected model/provider raw=%q normalized=%q provider=%q resolution=%q", raw, normalized, provider, resolution)
	}
}

func TestImportPersistsParsedRequestCount(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	requestCount := int64(7)
	records := []*fingerprint.ParsedRecord{{
		Agent:         "copilot",
		Provider:      "github",
		Model:         "gpt-5.4",
		TimestampMs:   1,
		DedupeID:      "copilot-summary",
		SessionID:     "session-1",
		InputTokens:   10,
		OutputTokens:  5,
		TotalTokens:   15,
		RequestCount:  &requestCount,
		SourceProduct: "copilot-session-state",
	}}
	added, updated, skipped, warnings := importParsedRecords(database, "copilot", records)
	if added != 1 || updated != 0 || skipped != 0 || len(warnings) != 0 {
		t.Fatalf("unexpected first import added=%d updated=%d skipped=%d warnings=%v", added, updated, skipped, warnings)
	}
	added, updated, skipped, warnings = importParsedRecords(database, "copilot", records)
	if added != 0 || updated != 0 || skipped != 1 || len(warnings) != 0 {
		t.Fatalf("unexpected repeated import added=%d updated=%d skipped=%d warnings=%v", added, updated, skipped, warnings)
	}
	var stored int64
	if err := database.Conn().QueryRow(`SELECT request_count FROM usage_events`).Scan(&stored); err != nil {
		t.Fatalf("read request_count: %v", err)
	}
	if stored != requestCount {
		t.Fatalf("request_count=%d want=%d", stored, requestCount)
	}
}

func TestCopilotLiveRequestCountImport(t *testing.T) {
	if os.Getenv("COPILOT_LIVE_TEST") != "1" {
		t.Skip("set COPILOT_LIVE_TEST=1 to validate the local Copilot session-state corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	adapter := adapters.NewCopilotAdapter()
	files, err := adapter.Discover([]string{filepath.Join(home, ".copilot", "session-state")})
	if err != nil {
		t.Fatalf("discover Copilot corpus: %v", err)
	}
	var records []*fingerprint.ParsedRecord
	var sourceKnownEvents, sourceRequests, sourceTokens int64
	for _, file := range files {
		parsed, err := adapter.ParseFile(file)
		if err != nil {
			t.Fatalf("parse Copilot corpus: %v", err)
		}
		for _, record := range parsed {
			sourceTokens += record.TotalTokens
			if record.RequestCount != nil {
				sourceKnownEvents++
				sourceRequests += *record.RequestCount
			}
		}
		records = append(records, parsed...)
	}
	if len(records) == 0 || sourceKnownEvents == 0 || sourceRequests == 0 || sourceTokens == 0 {
		t.Fatalf("live corpus has no request-count usage: files=%d records=%d known_events=%d", len(files), len(records), sourceKnownEvents)
	}

	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer database.Close()
	added, updated, skipped, warnings := importParsedRecords(database, "copilot", records)
	if updated != 0 || skipped != 0 || len(warnings) != 0 {
		t.Fatalf("unexpected live import result added=%d updated=%d skipped=%d warnings=%d", added, updated, skipped, len(warnings))
	}
	var dbEvents, dbKnownEvents, dbRequests, dbTokens int64
	if err := database.Conn().QueryRow(`SELECT COUNT(*), COUNT(request_count), COALESCE(SUM(request_count), 0), COALESCE(SUM(total_tokens), 0) FROM usage_events`).Scan(&dbEvents, &dbKnownEvents, &dbRequests, &dbTokens); err != nil {
		t.Fatalf("aggregate temp db: %v", err)
	}
	if dbEvents != int64(len(records)) || dbKnownEvents != sourceKnownEvents || dbRequests != sourceRequests || dbTokens != sourceTokens {
		t.Fatalf("source/db mismatch records=%d/%d known=%d/%d requests=%d/%d tokens=%d/%d", len(records), dbEvents, sourceKnownEvents, dbKnownEvents, sourceRequests, dbRequests, sourceTokens, dbTokens)
	}
	added, updated, skipped, warnings = importParsedRecords(database, "copilot", records)
	if added != 0 || updated != 0 || skipped != len(records) || len(warnings) != 0 {
		t.Fatalf("live reimport not idempotent added=%d updated=%d skipped=%d warnings=%d", added, updated, skipped, len(warnings))
	}
}

func TestParseImportFileProcessesStableRecentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, processed, warning := parseImportFile(fakeImportAdapter{}, path, time.Now().Add(-time.Hour))
	if warning != "" {
		t.Fatalf("unexpected warning: %s", warning)
	}
	if !processed || len(records) != 1 {
		t.Fatalf("expected stable recent file to be processed, processed=%v records=%d", processed, len(records))
	}
}

func TestParseImportFileReturnsNonFatalAdapterWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "warning.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, processed, warning := parseImportFile(fakeWarningImportAdapter{}, path, time.Now().Add(time.Hour))
	if !processed || len(records) != 1 {
		t.Fatalf("expected valid records to survive a parse warning, processed=%v records=%d", processed, len(records))
	}
	for _, want := range []string{"fake-warning parse warning", path, "line 1 invalid_token_totals"} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning missing %q: %s", want, warning)
		}
	}
}

type fakeImportAdapter struct{}

func (fakeImportAdapter) Name() string { return "fake" }

func (fakeImportAdapter) Discover(paths []string) ([]string, error) { return nil, nil }

func (fakeImportAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	return []*fingerprint.ParsedRecord{{Agent: "fake", TimestampMs: 1, TotalTokens: 1}}, nil
}

type fakeWarningImportAdapter struct{ fakeImportAdapter }

func (fakeWarningImportAdapter) Name() string { return "fake-warning" }

func (fakeWarningImportAdapter) ParseFileWithWarnings(path string) ([]*fingerprint.ParsedRecord, []string, error) {
	return []*fingerprint.ParsedRecord{{Agent: "fake-warning", TimestampMs: 1, TotalTokens: 1}}, []string{"line 1 invalid_token_totals"}, nil
}

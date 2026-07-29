package cmd

import (
	"database/sql"
	"fmt"
	"io"
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
	if len(records) != 1 {
		t.Fatalf("expected ccusage policy to suppress unchanged cumulative snapshot, got %d", len(records))
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

func TestImportCodexForkReplay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeImportCodexFixture(t, parent, []string{
		importCodexSessionMeta("2026-01-01T00:00:00Z", "parent", ""),
		importCodexUsage("2026-01-01T00:00:01Z", 10, 2, 12, "gpt-5.6-sol"),
		importCodexUsage("2026-01-01T00:00:02Z", 20, 4, 24, "gpt-5.6-sol"),
	})
	writeImportCodexFixture(t, child, []string{
		importCodexSessionMeta("2026-01-01T00:00:03Z", "child", "parent"),
		importCodexUsage("2026-01-01T00:00:03Z", 10, 2, 12, ""),
		importCodexUsage("2026-01-01T00:00:03Z", 20, 4, 24, ""),
		importCodexUsage("2026-01-01T00:00:05Z", 30, 6, 36, "gpt-5.6-terra"),
	})

	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	adapter := adapters.NewCodexAdapter()
	result := importAdapterFiles(database, adapter, []string{child, parent}, time.Now().Add(time.Hour))
	if result.files != 2 || result.added != 3 || result.updated != 0 || result.skipped != 0 || len(result.warnings) != 0 {
		t.Fatalf("unexpected first import result: %+v", result)
	}

	var events, totalTokens, unknown, rawUsage int64
	if err := database.Conn().QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_tokens), 0),
		       COALESCE(SUM(CASE WHEN model_normalized = 'unknown' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN raw_usage_json IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM usage_events
	`).Scan(&events, &totalTokens, &unknown, &rawUsage); err != nil {
		t.Fatalf("read imported replay fixture: %v", err)
	}
	if events != 3 || totalTokens != 36 || unknown != 0 || rawUsage != 0 {
		t.Fatalf("unexpected imported aggregates events=%d tokens=%d unknown=%d raw=%d", events, totalTokens, unknown, rawUsage)
	}

	result = importAdapterFiles(database, adapter, []string{child, parent}, time.Now().Add(time.Hour))
	if result.added != 0 || result.updated != 0 || result.skipped != 3 || len(result.warnings) != 0 {
		t.Fatalf("second import must be idempotent: %+v", result)
	}
	diagnostics := adapter.ImportDiagnostics()
	assertImportDiagnostic(t, diagnostics, "codex_replay_exact", 2, 24)
	assertImportDiagnostic(t, diagnostics, "codex_replay_events", 2, 0)
}

func TestImportCodexQuarantinesOnlyUncertainChild(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	standalone := filepath.Join(dir, "standalone.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeImportCodexFixture(t, standalone, []string{
		importCodexSessionMeta("2026-01-01T00:00:00Z", "standalone", ""),
		importCodexUsage("2026-01-01T00:00:01Z", 10, 1, 11, "gpt-5.6-sol"),
	})
	writeImportCodexFixture(t, child, []string{
		importCodexSessionMeta("2026-01-01T00:00:02Z", "sensitive-child-id", "unavailable-parent-id"),
		importCodexUsage("2026-01-01T00:00:03Z", 20, 2, 22, ""),
	})

	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	adapter := adapters.NewCodexAdapter()
	result := importAdapterFiles(database, adapter, []string{standalone, child}, time.Now().Add(time.Hour))
	if result.added != 1 || len(result.warnings) != 1 {
		t.Fatalf("uncertain child must not block standalone usage: %+v", result)
	}
	if strings.Contains(result.warnings[0], child) || strings.Contains(result.warnings[0], "sensitive-child-id") || strings.Contains(result.warnings[0], "unavailable-parent-id") {
		t.Fatalf("quarantine warning leaked private identity: %s", result.warnings[0])
	}
	var events int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&events); err != nil {
		t.Fatalf("count imported events: %v", err)
	}
	if events != 1 {
		t.Fatalf("quarantined child wrote usage: events=%d", events)
	}
	assertImportDiagnosticCount(t, adapter.ImportDiagnostics(), "codex_replay_unresolved", 1)
}

func TestImportPreparationRunsBeforeParsingAndPostProcessing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	adapter := &preparingPostImportAdapter{}
	result := importAdapterFiles(database, adapter, []string{path}, time.Now().Add(time.Hour))
	if result.added != 1 || len(result.warnings) != 0 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	if got := strings.Join(adapter.calls, ","); got != "prepare,parse,postprocess" {
		t.Fatalf("unexpected preparation order: %s", got)
	}
	if _, ok := any(adapters.NewCodexAdapter()).(adapters.RecordPostProcessor); ok {
		t.Fatal("Codex adapter must remain streaming and not implement RecordPostProcessor")
	}
}

func TestImportPreparationReceivesOnlyStableFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	adapter := &preparingPostImportAdapter{}
	result := importAdapterFiles(database, adapter, []string{missing, path}, time.Now().Add(time.Hour))
	if result.added != 1 || len(result.warnings) != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	if len(adapter.preparedPaths) != 1 || adapter.preparedPaths[0] != path {
		t.Fatalf("PrepareFileSet paths=%v want only %q", adapter.preparedPaths, path)
	}
}

func TestFormatImportDiagnosticUsesExplicitUnits(t *testing.T) {
	tests := []struct {
		name       string
		diagnostic adapters.ImportDiagnostic
		want       string
	}{
		{
			name:       "file count",
			diagnostic: adapters.ImportDiagnostic{Code: "codex_fork_files", Unit: adapters.ImportDiagnosticUnitCount, Count: 2},
			want:       "codex_fork_files: count=2",
		},
		{
			name:       "zero file count keeps unit",
			diagnostic: adapters.ImportDiagnostic{Code: "codex_parent_missing", Unit: adapters.ImportDiagnosticUnitCount},
			want:       "codex_parent_missing: count=0",
		},
		{
			name:       "replay usage",
			diagnostic: adapters.ImportDiagnostic{Code: "codex_replay_exact", Unit: adapters.ImportDiagnosticUnitUsage, Events: 3, Tokens: 36},
			want:       "codex_replay_exact: events=3 tokens=36",
		},
		{
			name:       "token count",
			diagnostic: adapters.ImportDiagnostic{Code: "codex_replay_tokens", Unit: adapters.ImportDiagnosticUnitTokens, Tokens: 36},
			want:       "codex_replay_tokens: tokens=36",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatImportDiagnostic(tt.diagnostic); got != tt.want {
				t.Fatalf("formatImportDiagnostic()=%q want %q", got, tt.want)
			}
		})
	}
}

func TestDoctorCodexReplayDiagnosticsPrintExplicitUnits(t *testing.T) {
	output := captureStdout(t, func() {
		printCodexReplayDiagnostics([]adapters.ImportDiagnostic{
			{Code: "codex_fork_files", Unit: adapters.ImportDiagnosticUnitCount, Count: 2},
			{Code: "codex_replay_exact", Unit: adapters.ImportDiagnosticUnitUsage, Events: 3, Tokens: 36},
		})
	})
	for _, want := range []string{
		"Codex fork replay:",
		"codex_fork_files: count=2",
		"codex_replay_exact: events=3 tokens=36",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "codex_fork_files: events=") {
		t.Fatalf("file count used event unit:\n%s", output)
	}
}

func TestImportCommandCompletesWithWarningsWithoutReturningError(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	child := filepath.Join(sessionDir, "child.jsonl")
	writeImportCodexFixture(t, child, []string{
		importCodexSessionMeta("2026-01-01T00:00:02Z", "child", "missing-parent"),
		importCodexUsage("2026-01-01T00:00:03Z", 20, 2, 22, "gpt-5.6-sol"),
	})

	cfg := config.Default()
	cfg.Import.GracingMinutes = 0
	cfg.Agents = config.AgentsConfig{
		Codex: config.AgentConfig{
			Enabled:         true,
			Paths:           []string{sessionDir},
			DuplicatePolicy: adapters.CodexDuplicatePolicyLedger,
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := discardStdout(t, func() error { return importCmd.RunE(importCmd, nil) }); err != nil {
		t.Fatalf("import command returned an error for a warning-only run: %v", err)
	}
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open import database: %v", err)
	}
	defer database.Close()
	var status, errorSummary string
	var skipped int
	if err := database.Conn().QueryRow(`
		SELECT status, error, events_skipped
		FROM import_runs
		ORDER BY started_at_ms DESC
		LIMIT 1
	`).Scan(&status, &errorSummary, &skipped); err != nil {
		t.Fatalf("read import run: %v", err)
	}
	if status != "completed_with_warnings" || errorSummary == "" {
		t.Fatalf("warning run status=%q error=%q", status, errorSummary)
	}
	if skipped != 0 {
		t.Fatalf("source diagnostics or quarantine must not count as duplicates: skipped=%d", skipped)
	}
}

func TestImportPreparationFailureSkipsOnlyFailingAdapter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	failing := &preparingPostImportAdapter{prepareErr: fmt.Errorf("cannot inspect %s", path)}
	failed := importAdapterFiles(database, failing, []string{path}, time.Now().Add(time.Hour))
	if failed.added != 0 || len(failed.warnings) != 1 || len(failing.calls) != 1 || failing.calls[0] != "prepare" {
		t.Fatalf("failed preparation did not fail closed: result=%+v calls=%v", failed, failing.calls)
	}
	if strings.Contains(failed.warnings[0], path) {
		t.Fatalf("preparation warning leaked source path: %s", failed.warnings[0])
	}
	succeeded := importAdapterFiles(database, fakeImportAdapter{}, []string{path}, time.Now().Add(time.Hour))
	if succeeded.added != 1 || len(succeeded.warnings) != 0 {
		t.Fatalf("unrelated adapter did not continue: %+v", succeeded)
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

type preparingPostImportAdapter struct {
	calls         []string
	preparedPaths []string
	prepareErr    error
}

func (a *preparingPostImportAdapter) Name() string { return "preparing-post" }

func (a *preparingPostImportAdapter) Discover(paths []string) ([]string, error) { return paths, nil }

func (a *preparingPostImportAdapter) PrepareFileSet(paths []string) error {
	a.calls = append(a.calls, "prepare")
	a.preparedPaths = append([]string(nil), paths...)
	return a.prepareErr
}

func (a *preparingPostImportAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	a.calls = append(a.calls, "parse")
	return []*fingerprint.ParsedRecord{{
		Agent:       a.Name(),
		Model:       "unknown",
		TimestampMs: 1,
		DedupeID:    "prepared-record",
		TotalTokens: 1,
	}}, nil
}

func (a *preparingPostImportAdapter) PostProcessRecords(records []*fingerprint.ParsedRecord) []*fingerprint.ParsedRecord {
	a.calls = append(a.calls, "postprocess")
	return records
}

func writeImportCodexFixture(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write Codex fixture: %v", err)
	}
}

func importCodexSessionMeta(timestamp, sessionID, parentID string) string {
	if parentID == "" {
		return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"source":"cli"}}`, timestamp, sessionID)
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"forked_from_id":%q}}`, timestamp, sessionID, parentID)
}

func importCodexUsage(timestamp string, input, output, total int64, modelName string) string {
	modelField := ""
	if modelName != "" {
		modelField = fmt.Sprintf(`,"model":%q`, modelName)
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d},"total_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}%s}}}`, timestamp, input, output, total, input, output, total, modelField)
}

func assertImportDiagnostic(t *testing.T, diagnostics []adapters.ImportDiagnostic, code string, wantEvents, wantTokens int64) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != code {
			continue
		}
		if diagnostic.Events != wantEvents || diagnostic.Tokens != wantTokens {
			t.Fatalf("diagnostic %s events=%d tokens=%d want events=%d tokens=%d", code, diagnostic.Events, diagnostic.Tokens, wantEvents, wantTokens)
		}
		return
	}
	t.Fatalf("missing diagnostic %s", code)
}

func assertImportDiagnosticCount(t *testing.T, diagnostics []adapters.ImportDiagnostic, code string, wantCount int64) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != code {
			continue
		}
		if diagnostic.Unit != adapters.ImportDiagnosticUnitCount || diagnostic.Count != wantCount {
			t.Fatalf("diagnostic %s unit=%q count=%d want unit=%q count=%d", code, diagnostic.Unit, diagnostic.Count, adapters.ImportDiagnosticUnitCount, wantCount)
		}
		return
	}
	t.Fatalf("missing diagnostic %s", code)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	previous := os.Stdout
	os.Stdout = writer
	fn()
	os.Stdout = previous
	if err := writer.Close(); err != nil {
		_ = reader.Close()
		t.Fatalf("close stdout writer: %v", err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		_ = reader.Close()
		t.Fatalf("read stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return string(output)
}

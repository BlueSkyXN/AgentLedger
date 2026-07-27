package cmd

import (
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

func TestImportAggregatesOmittedUsageEvidenceWarnings(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	records := []*fingerprint.ParsedRecord{
		{Agent: "claude", DedupeID: "one", TimestampMs: 1, TotalTokens: 1, EvidenceOmitted: true},
		{Agent: "claude", DedupeID: "two", TimestampMs: 2, TotalTokens: 1, EvidenceOmitted: true},
	}
	added, updated, skipped, warnings := importParsedRecords(database, "claude", records)
	if added != 2 || updated != 0 || skipped != 0 {
		t.Fatalf("unexpected import result added=%d updated=%d skipped=%d", added, updated, skipped)
	}
	if len(warnings) != 1 || warnings[0] != "claude usage evidence omitted for 2 parsed record(s)" {
		t.Fatalf("expected one aggregate evidence warning, got %v", warnings)
	}

	var rawCount int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events WHERE raw_usage_json IS NOT NULL`).Scan(&rawCount); err != nil {
		t.Fatalf("count raw evidence: %v", err)
	}
	if rawCount != 0 {
		t.Fatalf("expected omitted evidence to remain NULL/empty, got %d persisted values", rawCount)
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

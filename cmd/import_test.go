package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/config"
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

type fakeImportAdapter struct{}

func (fakeImportAdapter) Name() string { return "fake" }

func (fakeImportAdapter) Discover(paths []string) ([]string, error) { return nil, nil }

func (fakeImportAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	return []*fingerprint.ParsedRecord{{Agent: "fake", TimestampMs: 1, TotalTokens: 1}}, nil
}

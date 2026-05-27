package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

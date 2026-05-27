package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestCodexLastTokenUsageDirectCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := `{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","session_id":"A","payload":{"type":"token_count","model":"gpt-5-codex","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":25,"output_tokens":50,"reasoning_output_tokens":10,"total_tokens":160}}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec.InputTokens != 75 || rec.CacheReadTokens != 25 || rec.OutputTokens != 50 || rec.ReasoningTokens != 10 || rec.TotalTokens != 160 {
		t.Fatalf("unexpected tokens: input=%d cache=%d output=%d reasoning=%d total=%d", rec.InputTokens, rec.CacheReadTokens, rec.OutputTokens, rec.ReasoningTokens, rec.TotalTokens)
	}
	if rec.TokenAccountingMethod != model.AccCodexLastTokenUsage || rec.SourceTotalTokens == nil || *rec.SourceTotalTokens != 160 {
		t.Fatalf("unexpected accounting method=%s source_total=%v", rec.TokenAccountingMethod, rec.SourceTotalTokens)
	}
	if rec.RawInputTokens == nil || *rec.RawInputTokens != 100 {
		t.Fatalf("expected raw input tokens 100, got %v", rec.RawInputTokens)
	}
	if rec.AccountingProfile != CodexDuplicatePolicyLedger {
		t.Fatalf("unexpected accounting profile=%s", rec.AccountingProfile)
	}
	if rec.Model != "gpt-5-codex" || rec.ModelIsFallback {
		t.Fatalf("unexpected model=%s fallback=%v", rec.Model, rec.ModelIsFallback)
	}
}

func TestCodexCumulativeDeltaMultiSessionAndCounterReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-01-01T00:00:00Z","payload":{"base_instructions":"skip me"}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:00:01Z","session_id":"A","payload":{"type":"token_count","info":null}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","session_id":"A","model":"gpt-5","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":80,"output_tokens":20,"total_tokens":100}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:02:00Z","session_id":"B","model":"gpt-5","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":40,"output_tokens":10,"total_tokens":50}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:03:00Z","session_id":"A","model":"gpt-5","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:04:00Z","session_id":"B","model":"gpt-5","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":15,"output_tokens":5,"total_tokens":20}}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records after skip/reset-zero, got %d", len(records))
	}
	expectedTotals := []int64{100, 50, 50}
	expectedSessions := []string{"A", "B", "A"}
	for i, expected := range expectedTotals {
		if records[i].TotalTokens != expected || records[i].SessionID != expectedSessions[i] {
			t.Fatalf("record %d expected session=%s total=%d got session=%s total=%d", i, expectedSessions[i], expected, records[i].SessionID, records[i].TotalTokens)
		}
		if records[i].TokenAccountingMethod != model.AccCodexTotalDelta {
			t.Fatalf("record %d method=%s", i, records[i].TokenAccountingMethod)
		}
	}
	if records[2].SourceTotalTokens == nil || *records[2].SourceTotalTokens != 150 {
		t.Fatalf("expected raw cumulative source total 150, got %v", records[2].SourceTotalTokens)
	}
}

func TestCodexSkipsDuplicateLastTokenUsageSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":80,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":100},"last_token_usage":{"input_tokens":80,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":100}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:03Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":80,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":100},"last_token_usage":{"input_tokens":80,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":100}},"rate_limits":{"primary":{"used_percent":10}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:02:00Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":120,"cached_input_tokens":30,"output_tokens":30,"reasoning_output_tokens":0,"total_tokens":150},"last_token_usage":{"input_tokens":40,"cached_input_tokens":20,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":50}}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected duplicate snapshot to be skipped, got %d records", len(records))
	}
	if records[0].TotalTokens != 100 || records[1].TotalTokens != 50 {
		t.Fatalf("unexpected totals: %d, %d", records[0].TotalTokens, records[1].TotalTokens)
	}
	if records[1].SourceTotalTokens == nil || *records[1].SourceTotalTokens != 150 {
		t.Fatalf("expected raw cumulative source total 150, got %v", records[1].SourceTotalTokens)
	}
}

func TestCodexCCUsageCompatiblePolicyKeepsTimestampDistinctSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":80,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":100},"last_token_usage":{"input_tokens":80,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":100}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:03Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":80,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":100},"last_token_usage":{"input_tokens":80,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":100}},"rate_limits":{"primary":{"used_percent":10}}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapterWithOptions(CodexOptions{DuplicatePolicy: CodexDuplicatePolicyCCUsageCompatible}).ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected timestamp-distinct duplicate snapshots to be kept, got %d records", len(records))
	}
	for _, rec := range records {
		if rec.AccountingProfile != CodexDuplicatePolicyCCUsageCompatible {
			t.Fatalf("unexpected accounting profile=%s", rec.AccountingProfile)
		}
	}
}

func TestCodexCountsSameLastUsageWhenCumulativeTotalChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15},"last_token_usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:02:00Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30},"last_token_usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected same last usage with changed cumulative total to count twice, got %d records", len(records))
	}
	if records[0].TotalTokens != 15 || records[1].TotalTokens != 15 {
		t.Fatalf("unexpected totals: %d, %d", records[0].TotalTokens, records[1].TotalTokens)
	}
}

func TestCodexCachedInputClampReasoningFallbackAndTurnContextModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","session_id":"A","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":125,"output_tokens":50,"reasoning_output_tokens":10}}}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5-codex"}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","session_id":"A","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	fallback := records[0]
	if fallback.Model != "gpt-5" || !fallback.ModelIsFallback {
		t.Fatalf("expected gpt-5 fallback, model=%s fallback=%v", fallback.Model, fallback.ModelIsFallback)
	}
	if fallback.InputTokens != 0 || fallback.CacheReadTokens != 100 || fallback.TotalTokens != 150 {
		t.Fatalf("expected cached clamp and computed total, input=%d cache=%d total=%d", fallback.InputTokens, fallback.CacheReadTokens, fallback.TotalTokens)
	}
	if records[1].Model != "gpt-5-codex" || records[1].ModelIsFallback {
		t.Fatalf("expected turn_context model, model=%s fallback=%v", records[1].Model, records[1].ModelIsFallback)
	}
}

func TestCodexTaskCompleteTimingAttachesToPreviousUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:00:10Z","session_id":"A","payload":{"type":"token_count","model":"gpt-5-codex","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":40,"reasoning_output_tokens":5,"total_tokens":145}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:00:12Z","session_id":"A","payload":{"type":"task_complete","turn_id":"turn-a","duration_ms":12000,"time_to_first_token_ms":1500,"completed_at":1767225612}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec.TotalDurationMs != 12000 || rec.TTFTMs != 1500 {
		t.Fatalf("expected timing duration=12000 ttft=1500, got duration=%d ttft=%d", rec.TotalDurationMs, rec.TTFTMs)
	}
	if rec.CompletedAtMs != 1767225612000 || rec.RequestStartedAtMs != 1767225600000 || rec.FirstTokenAtMs != 1767225601500 {
		t.Fatalf("unexpected timing anchors completed=%d started=%d first=%d", rec.CompletedAtMs, rec.RequestStartedAtMs, rec.FirstTokenAtMs)
	}
	if rec.TurnID != "turn-a" {
		t.Fatalf("expected turn id turn-a, got %q", rec.TurnID)
	}
}

func TestCodexSessionPathIDFromNestedSessionsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex", "sessions", "2026", "05", "27", "rollout-abc.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	data := `{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].SessionPathID != "2026/05/27/rollout-abc" {
		t.Fatalf("unexpected session path id=%q", records[0].SessionPathID)
	}
}

func TestCodexSkipsSessionTokenCountWithOnlyTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := `{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","session_id":"A","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":20757}}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected total-only session token_count to be skipped, got %d records", len(records))
	}
}

func TestCodexDiscoverNormalizesHomeRootToSessions(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	archived := filepath.Join(root, "archived_sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.MkdirAll(archived, 0o755); err != nil {
		t.Fatalf("mkdir archived: %v", err)
	}

	paths := normalizeCodexDiscoverPaths([]string{root, sessions, archived})
	expected := []string{sessions, archived}
	if len(paths) != len(expected) {
		t.Fatalf("expected paths %v, got %v", expected, paths)
	}
	for i := range expected {
		if filepath.Clean(paths[i]) != filepath.Clean(expected[i]) {
			t.Fatalf("path %d expected %s got %s", i, expected[i], paths[i])
		}
	}
}

func TestCodexHeadlessUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"timestamp":"2026-01-01T00:00:00Z","model":"gpt-5-codex","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","response":{"model":"gpt-5-codex","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	for i, rec := range records {
		if rec.TokenAccountingMethod != model.AccCodexHeadlessUsage {
			t.Fatalf("record %d method=%s", i, rec.TokenAccountingMethod)
		}
		if rec.ObservabilityLevel != "full" {
			t.Fatalf("record %d observability=%s", i, rec.ObservabilityLevel)
		}
	}
}

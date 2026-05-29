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
	// 累计计数器在 session B 从 50 回落到 20（compact 重置），该段应整段计入而非丢弃。
	if len(records) != 4 {
		t.Fatalf("expected 4 records (counter reset segment counted), got %d", len(records))
	}
	expectedTotals := []int64{100, 50, 50, 20}
	expectedSessions := []string{"A", "B", "A", "B"}
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
	if records[3].SourceTotalTokens == nil || *records[3].SourceTotalTokens != 20 {
		t.Fatalf("expected reset-segment cumulative source total 20, got %v", records[3].SourceTotalTokens)
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

func TestCodexTotalDeltaCapturesGapsThatLastTokenUsageMisses(t *testing.T) {
	// 一个 token_count 区间内发生多次调用时，last_token_usage 只记最后一次、会漏掉
	// 中间调用，而累计 total_token_usage 捕获全部。默认应据累计 delta 还原真实增量。
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":0,"total_tokens":100},"last_token_usage":{"input_tokens":100,"output_tokens":0,"total_tokens":100}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:02:00Z","session_id":"A","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":480,"output_tokens":20,"total_tokens":500},"last_token_usage":{"input_tokens":80,"output_tokens":0,"total_tokens":80}}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	accurate, err := NewCodexAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(accurate) != 2 {
		t.Fatalf("expected 2 records, got %d", len(accurate))
	}
	// 默认: 100 + (500-100)=400，合计 500 = 最终累计，捕获了 last 漏掉的 320。
	if accurate[0].TotalTokens != 100 || accurate[1].TotalTokens != 400 {
		t.Fatalf("accurate totals expected 100,400 got %d,%d", accurate[0].TotalTokens, accurate[1].TotalTokens)
	}
	if accurate[1].InputTokens != 380 || accurate[1].OutputTokens != 20 {
		t.Fatalf("accurate delta expected input=380 output=20 got input=%d output=%d", accurate[1].InputTokens, accurate[1].OutputTokens)
	}

	compat, err := NewCodexAdapterWithOptions(CodexOptions{DuplicatePolicy: CodexDuplicatePolicyCCUsageCompatible}).ParseFile(path)
	if err != nil {
		t.Fatalf("parse compat: %v", err)
	}
	// ccusage 口径: 直接用 last → 100 + 80 = 180，漏掉中间 320（即 ccusage 的低估侧）。
	if len(compat) != 2 || compat[1].TotalTokens != 80 {
		t.Fatalf("compat expected record1 total=80 (last-only), got %d records last=%d", len(compat), compat[len(compat)-1].TotalTokens)
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

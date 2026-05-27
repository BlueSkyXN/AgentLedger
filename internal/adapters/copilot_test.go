package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestCopilotCacheReadSubtractsInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copilot.jsonl")
	line := `{"name":"chat span","traceId":"trace","spanId":"span","timestamp":"2026-01-01T00:00:00Z","attributes":{"gen_ai.usage.input_tokens":19452,"gen_ai.usage.output_tokens":1200,"gen_ai.usage.cache_read.input_tokens":123,"gen_ai.usage.total_tokens":20652,"gen_ai.response.model":"gpt-4.1","gen_ai.conversation.id":"session","gen_ai.response.id":"resp"}}`
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCopilotAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec.InputTokens != 19329 || rec.CacheReadTokens != 123 || rec.OutputTokens != 1200 || rec.TotalTokens != 20652 {
		t.Fatalf("unexpected tokens input=%d cache=%d output=%d total=%d", rec.InputTokens, rec.CacheReadTokens, rec.OutputTokens, rec.TotalTokens)
	}
	if rec.MessageID != "trace:span" || rec.RequestID != "resp" {
		t.Fatalf("unexpected identity message_id=%s request_id=%s", rec.MessageID, rec.RequestID)
	}
	if rec.TokenAccountingMethod != model.AccCopilotOtelParts || rec.ObservabilityLevel != "full" {
		t.Fatalf("unexpected method=%s observability=%s", rec.TokenAccountingMethod, rec.ObservabilityLevel)
	}
	if rec.SourceTotalTokens == nil || *rec.SourceTotalTokens != 20652 {
		t.Fatalf("expected source total 20652, got %v", rec.SourceTotalTokens)
	}
}

func TestCopilotTotalOnlyFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copilot.jsonl")
	line := `{"name":"inference log","traceId":"trace","spanId":"span","timestamp":"2026-01-01T00:00:00Z","attributes":{"gen_ai.usage.total_tokens":123,"gen_ai.response.model":"gpt-4.1","gen_ai.conversation.id":"session"}}`
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCopilotAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec.TotalTokens != 123 || rec.SourceTotalTokens == nil || *rec.SourceTotalTokens != 123 {
		t.Fatalf("unexpected total=%d source_total=%v", rec.TotalTokens, rec.SourceTotalTokens)
	}
	if rec.TokenAccountingMethod != model.AccCopilotOtelTotalFallback || rec.ObservabilityLevel != "inferred" {
		t.Fatalf("unexpected method=%s observability=%s", rec.TokenAccountingMethod, rec.ObservabilityLevel)
	}
}

func TestCopilotDedupesByCandidatePriorityBeforeEmit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copilot.jsonl")
	data := "" +
		`{"name":"inference log","traceId":"trace","spanId":"span","timestamp":"2026-01-01T00:00:00Z","attributes":{"gen_ai.usage.input_tokens":100,"gen_ai.usage.output_tokens":50,"gen_ai.usage.total_tokens":150,"gen_ai.response.model":"gpt-4.1","gen_ai.conversation.id":"session"}}` + "\n" +
		`{"name":"chat span","traceId":"trace","spanId":"span","timestamp":"2026-01-01T00:00:01Z","attributes":{"gen_ai.usage.input_tokens":10,"gen_ai.usage.output_tokens":2,"gen_ai.usage.total_tokens":12,"gen_ai.response.model":"gpt-4.1","gen_ai.conversation.id":"session"}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCopilotAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected deduped single record, got %d", len(records))
	}
	if records[0].InputTokens != 10 || records[0].OutputTokens != 2 || records[0].TotalTokens != 12 {
		t.Fatalf("expected chat span candidate to win, input=%d output=%d total=%d", records[0].InputTokens, records[0].OutputTokens, records[0].TotalTokens)
	}
}

func TestCopilotDiscoverNoOtelFilesIsSilent(t *testing.T) {
	t.Setenv("COPILOT_OTEL_FILE_EXPORTER_PATH", "")
	files, err := NewCopilotAdapter().Discover([]string{filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatalf("discover should be silent: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %v", files)
	}
}

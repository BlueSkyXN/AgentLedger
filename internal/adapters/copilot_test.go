package adapters

import (
	"encoding/json"
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
	if rec.SourceProduct != "copilot-otel" || rec.AccountingProfile != "input_includes_cache_read" {
		t.Fatalf("unexpected source_product=%s accounting_profile=%s", rec.SourceProduct, rec.AccountingProfile)
	}
	if rec.RawInputTokens == nil || *rec.RawInputTokens != 19452 {
		t.Fatalf("expected raw input 19452, got %v", rec.RawInputTokens)
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

func TestCopilotSessionShutdownModelMetrics(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session-state", "session-123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	path := filepath.Join(dir, "events.jsonl")
	line := `{"type":"session.shutdown","timestamp":"2026-01-01T00:00:00Z","data":{"sessionId":"session-123","modelMetrics":{"gpt-5.4":{"usage":{"inputTokens":1000,"outputTokens":200,"cacheReadTokens":300,"cacheWriteTokens":40,"reasoningTokens":50},"requests":{"count":3,"cost":0.0123}},"claude-opus-4.6":{"usage":{"inputTokens":10,"outputTokens":2,"cacheReadTokens":3,"cacheWriteTokens":4,"reasoningTokens":0},"requests":{"count":1,"cost":0.004}}}}}`
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCopilotAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 model metric records, got %d", len(records))
	}
	byModel := map[string]bool{}
	for _, rec := range records {
		byModel[rec.Model] = true
		if rec.SessionID != "session-123" || rec.SourceProduct != "copilot-session-state" {
			t.Fatalf("unexpected session/source model=%s session=%s product=%s", rec.Model, rec.SessionID, rec.SourceProduct)
		}
		if rec.TokenAccountingMethod != model.AccCopilotSessionMetrics || rec.ObservabilityLevel != "session_summary" {
			t.Fatalf("unexpected method=%s observability=%s", rec.TokenAccountingMethod, rec.ObservabilityLevel)
		}
		if rec.AccountingProfile != "input_includes_cache_read" || rec.SessionPathID != "session-123" {
			t.Fatalf("unexpected accounting_profile=%s session_path_id=%s", rec.AccountingProfile, rec.SessionPathID)
		}
		if rec.DedupeID == "" || rec.MessageID == "" || rec.RequestID == "" {
			t.Fatalf("expected stable identity for model=%s", rec.Model)
		}
		if rec.SourceTotalTokens != nil {
			t.Fatalf("session metric has no source total; got %v", rec.SourceTotalTokens)
		}
		if rec.RawJSON == "" {
			t.Fatalf("expected raw usage envelope for model=%s", rec.Model)
		}
		if rec.CostUSD != nil {
			t.Fatalf("requests.cost is not USD and should not be recorded as CostUSD: %v", rec.CostUSD)
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(rec.RawJSON), &raw); err != nil {
			t.Fatalf("raw json should be valid for model=%s: %v", rec.Model, err)
		}
		shutdownID, _ := raw["shutdown_id"].(string)
		sessionPathID, _ := raw["session_path_id"].(string)
		if shutdownID == "" || sessionPathID != "session-123" {
			t.Fatalf("raw envelope missing shutdown/session ids: %v", raw)
		}
		if requests, ok := raw["requests"].(map[string]interface{}); !ok || requests["cost"] == nil {
			t.Fatalf("requests.cost should remain available in raw envelope: %v", raw)
		}
		if rec.Model == "gpt-5.4" {
			if rec.InputTokens != 700 || rec.OutputTokens != 200 || rec.CacheReadTokens != 300 || rec.CacheCreationTokens != 40 || rec.ReasoningTokens != 50 || rec.TotalTokens != 1290 {
				t.Fatalf("unexpected gpt tokens input=%d output=%d cacheRead=%d cacheWrite=%d reasoning=%d total=%d", rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.CacheCreationTokens, rec.ReasoningTokens, rec.TotalTokens)
			}
			if rec.RawInputTokens == nil || *rec.RawInputTokens != 1000 {
				t.Fatalf("expected raw gpt input 1000, got %v", rec.RawInputTokens)
			}
		}
		if rec.Model == "claude-opus-4.6" {
			if rec.InputTokens != 7 || rec.OutputTokens != 2 || rec.CacheReadTokens != 3 || rec.CacheCreationTokens != 4 || rec.ReasoningTokens != 0 || rec.TotalTokens != 16 {
				t.Fatalf("unexpected claude tokens input=%d output=%d cacheRead=%d cacheWrite=%d reasoning=%d total=%d", rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.CacheCreationTokens, rec.ReasoningTokens, rec.TotalTokens)
			}
		}
	}
	if !byModel["gpt-5.4"] || !byModel["claude-opus-4.6"] {
		t.Fatalf("missing model records: %v", byModel)
	}
}

func TestCopilotSessionShutdownFallsBackToDirectorySessionID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session-state", "session-from-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	path := filepath.Join(dir, "events.jsonl")
	line := `{"type":"session.shutdown","timestamp":"2026-01-01T00:00:00Z","data":{"modelMetrics":{"gpt-5.4":{"usage":{"inputTokens":1,"outputTokens":2,"cacheReadTokens":3,"cacheWriteTokens":4,"reasoningTokens":5},"requests":{"count":1}}}}}`
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
	if records[0].SessionID != "session-from-dir" {
		t.Fatalf("expected directory session id, got %q", records[0].SessionID)
	}
}

func TestCopilotSessionShutdownKeepsMultipleShutdownSegments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session-state", "session-123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	path := filepath.Join(dir, "events.jsonl")
	data := "" +
		`{"type":"session.shutdown","id":"shutdown-1","timestamp":"2026-01-01T00:00:00Z","data":{"sessionId":"session-123","modelMetrics":{"gpt-5.4":{"usage":{"inputTokens":10,"outputTokens":2,"cacheReadTokens":3,"cacheWriteTokens":4,"reasoningTokens":5},"requests":{"count":1}}}}}` + "\n" +
		`{"type":"session.resume","timestamp":"2026-01-01T00:01:00Z","data":{"sessionId":"session-123"}}` + "\n" +
		`{"type":"session.shutdown","id":"shutdown-2","timestamp":"2026-01-01T00:02:00Z","data":{"sessionId":"session-123","modelMetrics":{"gpt-5.4":{"usage":{"inputTokens":20,"outputTokens":3,"cacheReadTokens":4,"cacheWriteTokens":5,"reasoningTokens":6},"requests":{"count":1}}}}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCopilotAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 shutdown segment records, got %d", len(records))
	}
	if records[0].DedupeID == records[1].DedupeID {
		t.Fatalf("multiple shutdown segments for the same model must not collapse: %s", records[0].DedupeID)
	}
	var total int64
	for _, rec := range records {
		total += rec.TotalTokens
	}
	if total != 55 {
		t.Fatalf("unexpected segment total sum: %d", total)
	}
}

func TestCopilotSessionContextCapturesProjectPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session-state", "session-from-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	path := filepath.Join(dir, "events.jsonl")
	data := "" +
		`{"type":"session.start","timestamp":"2026-01-01T00:00:00Z","data":{"sessionId":"session-real","context":{"gitRoot":"/repo/from/start","cwd":"/repo/cwd"}}}` + "\n" +
		`{"type":"session.context_changed","timestamp":"2026-01-01T00:01:00Z","data":{"gitRoot":"/repo/from/context-change"}}` + "\n" +
		`{"type":"session.shutdown","id":"shutdown-1","timestamp":"2026-01-01T00:02:00Z","data":{"modelMetrics":{"gpt-5.4":{"usage":{"inputTokens":10,"outputTokens":2,"cacheReadTokens":3,"cacheWriteTokens":4,"reasoningTokens":5},"requests":{"count":1}}}}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := NewCopilotAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].SessionID != "session-real" || records[0].SessionPathID != "session-from-dir" {
		t.Fatalf("unexpected session ids session=%s path=%s", records[0].SessionID, records[0].SessionPathID)
	}
	if records[0].ProjectPath != "/repo/from/context-change" {
		t.Fatalf("expected latest project context, got %q", records[0].ProjectPath)
	}
}

func TestCopilotSessionCacheReadClampsToRawInput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session-state", "session-123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	path := filepath.Join(dir, "events.jsonl")
	line := `{"type":"session.shutdown","id":"shutdown-1","timestamp":"2026-01-01T00:00:00Z","data":{"modelMetrics":{"gpt-5.4":{"usage":{"inputTokens":10,"outputTokens":2,"cacheReadTokens":50,"cacheWriteTokens":4,"reasoningTokens":5},"requests":{"count":1}}}}}`
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
	if rec.InputTokens != 0 || rec.CacheReadTokens != 10 || rec.TotalTokens != 21 {
		t.Fatalf("unexpected clamp result input=%d cacheRead=%d total=%d", rec.InputTokens, rec.CacheReadTokens, rec.TotalTokens)
	}
	if rec.RawInputTokens == nil || *rec.RawInputTokens != 10 {
		t.Fatalf("expected raw input 10, got %v", rec.RawInputTokens)
	}
}

func TestCopilotDiscoverNormalizesHomeRootToKnownUsageDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "otel"), 0o755); err != nil {
		t.Fatalf("mkdir otel: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "session-state"), 0o755); err != nil {
		t.Fatalf("mkdir session-state: %v", err)
	}

	paths := normalizeCopilotDiscoverPaths([]string{root, filepath.Join(root, "otel")})
	expectedOtel := filepath.Join(root, "otel")
	expectedSession := filepath.Join(root, "session-state")
	if len(paths.otel) != 1 || len(paths.sessionState) != 1 || len(paths.other) != 0 {
		t.Fatalf("expected one otel and one session-state path, got otel=%v session-state=%v other=%v", paths.otel, paths.sessionState, paths.other)
	}
	if filepath.Clean(paths.otel[0]) != filepath.Clean(expectedOtel) {
		t.Fatalf("expected otel path %s got %s", expectedOtel, paths.otel[0])
	}
	if filepath.Clean(paths.sessionState[0]) != filepath.Clean(expectedSession) {
		t.Fatalf("expected session-state path %s got %s", expectedSession, paths.sessionState[0])
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

func TestCopilotDiscoverPrefersOtelOverSessionStateWhenBothExist(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".copilot")
	sessionDir := filepath.Join(root, "session-state", "session-1")
	if err := os.MkdirAll(filepath.Join(root, "otel"), 0o755); err != nil {
		t.Fatalf("mkdir otel: %v", err)
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	sessionEvents := filepath.Join(sessionDir, "events.jsonl")
	otelFile := filepath.Join(root, "otel", "telemetry.jsonl")
	for _, path := range []string{sessionEvents, otelFile} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	files, err := NewCopilotAdapter().Discover([]string{root})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	got := map[string]bool{}
	for _, file := range files {
		got[file] = true
	}
	if !got[otelFile] || got[sessionEvents] {
		t.Fatalf("expected otel files to suppress session-state fallback, got %v", files)
	}
}

func TestCopilotDiscoverFallsBackToFilteredSessionStateEvents(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".copilot")
	sessionDir := filepath.Join(root, "session-state", "session-1")
	nestedDir := filepath.Join(sessionDir, "files", "snapshot", "logs")
	if err := os.MkdirAll(filepath.Join(root, "otel"), 0o755); err != nil {
		t.Fatalf("mkdir otel: %v", err)
	}
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	sessionEvents := filepath.Join(sessionDir, "events.jsonl")
	nestedJSONL := filepath.Join(nestedDir, "config-audit.jsonl")
	for _, path := range []string{sessionEvents, nestedJSONL} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	files, err := NewCopilotAdapter().Discover([]string{root})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	got := map[string]bool{}
	for _, file := range files {
		got[file] = true
	}
	if !got[sessionEvents] {
		t.Fatalf("expected session-state events fallback, got %v", files)
	}
	if got[nestedJSONL] {
		t.Fatalf("nested session snapshot JSONL should not be discovered: %v", files)
	}
}

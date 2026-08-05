package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestGeminiUsageIdentityAndAccounting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".gemini", "sessions", "session-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events.jsonl")
	line := `{"event_id":"event-1","session_id":"session-1","timestamp":"2026-01-01T00:00:00Z","model":"gemini-2.5-pro","usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"cachedContentTokenCount":30,"thoughtsTokenCount":5,"toolUsePromptTokenCount":7,"totalTokenCount":132}}`
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	records, err := NewGeminiAdapter().ParseFile(path)
	if err != nil || len(records) != 1 {
		t.Fatalf("parse: records=%d err=%v", len(records), err)
	}
	record := records[0]
	if record.InputTokens != 77 || record.OutputTokens != 20 || record.CacheReadTokens != 30 || record.ReasoningTokens != 5 || record.TotalTokens != 132 {
		t.Fatalf("unexpected Gemini accounting: %#v", record)
	}
	if record.RawInputTokens == nil || *record.RawInputTokens != 107 || record.SourceTotalTokens == nil || *record.SourceTotalTokens != 132 {
		t.Fatalf("missing Gemini source diagnostics: raw=%v total=%v", record.RawInputTokens, record.SourceTotalTokens)
	}
	if record.TokenAccountingMethod != model.AccGeminiUsage || record.AccountingProfile != "gemini_usage_v1" || record.ObservabilityLevel != "full" {
		t.Fatalf("unexpected Gemini contract: %#v", record)
	}

	_, firstEventID, strategy, _, err := fingerprint.ComputeIdentity(record)
	if err != nil || strategy != fingerprint.StrategyNativeEvent {
		t.Fatalf("compute first identity: strategy=%s err=%v", strategy, err)
	}
	changed := *record
	changed.Model = "gemini-3-pro"
	changed.InputTokens = 1
	changed.OutputTokens = 2
	changed.CacheReadTokens = 3
	changed.ReasoningTokens = 4
	changed.TotalTokens = 10
	_, changedEventID, _, _, err := fingerprint.ComputeIdentity(&changed)
	if err != nil {
		t.Fatalf("compute changed identity: %v", err)
	}
	if firstEventID != changedEventID {
		t.Fatalf("model/token enrichment changed native Gemini identity: %q != %q", firstEventID, changedEventID)
	}
}

func TestGeminiMissingStableSessionIsRejectedByIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.jsonl")
	line := `{"event_id":"event-1","timestamp":"2026-01-01T00:00:00Z","usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	records, err := NewGeminiAdapter().ParseFile(path)
	if err != nil || len(records) != 1 {
		t.Fatalf("parse: records=%d err=%v", len(records), err)
	}
	if _, _, _, _, err := fingerprint.ComputeIdentity(records[0]); fingerprint.IdentityErrorCode(err) != "missing_session" {
		t.Fatalf("expected missing_session, got %v", err)
	}
}

func TestGeminiWithoutNativeEventUsesSessionRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gemini", "sessions", "session-1", "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"session_id":"session-1","timestamp":"2026-01-01T00:00:00Z","usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	records, err := NewGeminiAdapter().ParseFile(path)
	if err != nil || len(records) != 1 {
		t.Fatalf("parse: records=%d err=%v", len(records), err)
	}
	if records[0].IdentityKind != "record" || records[0].IdentitySubkey != "line:1" {
		t.Fatalf("unexpected record fallback: kind=%q subkey=%q", records[0].IdentityKind, records[0].IdentitySubkey)
	}
	if _, _, strategy, _, err := fingerprint.ComputeIdentity(records[0]); err != nil || strategy != fingerprint.StrategySessionRecord {
		t.Fatalf("strategy=%s err=%v", strategy, err)
	}
}

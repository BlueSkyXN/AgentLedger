package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestTraeWorkCNAdapterParsesSanitizedMessageUsage(t *testing.T) {
	path := writeTraeWorkCNJSONL(t, `{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"synthetic-session","message_id":"synthetic-message","timestamp_ms":1780000000000,"model":"claude-sonnet-4","mode":"work","agent_type":"solo_work_lite","token_usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":160,"cache_creation_input_tokens":10,"cache_read_input_tokens":10,"reasoning_tokens":5,"prompt_tokens_total":1000,"completion_tokens_total":400,"last_turn_total_tokens":160,"max_tokens":200000}}`)

	records, warnings, err := NewTraeWorkCNAdapter().ParseFileWithWarnings(path)
	if err != nil {
		t.Fatalf("ParseFileWithWarnings: %v", err)
	}
	if len(warnings) != 0 || len(records) != 1 {
		t.Fatalf("records=%d warnings=%v", len(records), warnings)
	}
	rec := records[0]
	if rec.Agent != "trae-work-cn" || rec.SourceProduct != "trae-work-cn" || rec.Provider != "unknown" {
		t.Fatalf("unexpected source fields: %#v", rec)
	}
	if rec.NativeSessionID != "synthetic-session" || rec.SessionID != "synthetic-session" || rec.NativeEventID != "synthetic-message" || rec.MessageID != "synthetic-message" || rec.IdentityKind != "message" || rec.IdentityScope != "session" {
		t.Fatalf("unexpected identity fields: %#v", rec)
	}
	if rec.TimestampMs != 1780000000000 || rec.Model != "claude-sonnet-4" || rec.ModelNormalized != "claude-sonnet-4" || rec.ModelResolution != model.ModelResolutionDirectEvent || rec.ModelIsFallback {
		t.Fatalf("unexpected timestamp/model fields: %#v", rec)
	}
	if rec.InputTokens != 100 || rec.OutputTokens != 40 || rec.TotalTokens != 160 {
		t.Fatalf("unexpected canonical token fields: %#v", rec)
	}
	if rec.CacheCreationTokens != 0 || rec.CacheReadTokens != 0 || rec.ReasoningTokens != 0 {
		t.Fatalf("cache/reasoning details must not be double-counted: %#v", rec)
	}
	if rec.RawInputTokens == nil || *rec.RawInputTokens != 100 || rec.SourceTotalTokens == nil || *rec.SourceTotalTokens != 160 {
		t.Fatalf("unexpected source token diagnostics: raw=%v source=%v", rec.RawInputTokens, rec.SourceTotalTokens)
	}
	if rec.ObservabilityLevel != "partial" || rec.TokenAccountingMethod != model.AccTraeWorkCNMessageUsage || rec.AccountingProfile != traeWorkCNAccountingProfile || rec.ParserVersion != "trae-work-cn-v1" || rec.Granularity != "message" {
		t.Fatalf("unexpected accounting metadata: %#v", rec)
	}

	assertTraeWorkCNEnvelopeIsPrivate(t, rec.FingerprintJSON)
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(rec.FingerprintJSON), &envelope); err != nil {
		t.Fatalf("unmarshal sanitized envelope: %v", err)
	}
	usage, ok := envelope["token_usage"].(map[string]interface{})
	if !ok || usage["total_tokens"] != float64(160) || usage["cache_read_input_tokens"] != float64(10) || usage["reasoning_tokens"] != float64(5) || usage["prompt_tokens_total"] != float64(1000) {
		t.Fatalf("unexpected sanitized token envelope: %#v", envelope)
	}
	if rec.RawSHA256 == "" || rec.SourceFile != path || rec.LineNumber != 1 {
		t.Fatalf("missing local diagnostics: %#v", rec)
	}
}

func TestTraeWorkCNAdapterFailsClosedOnInvalidOrUnsanitizedLines(t *testing.T) {
	valid := `{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"valid","timestamp_ms":1780000000000,"mode":"code","agent_type":"solo_code_lite","token_usage":{"total_tokens":1}}`
	invalid := []string{
		`{not-json}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"content","timestamp_ms":1780000000000,"content":"private","token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"user","timestamp_ms":1780000000000,"user_info":{"name":"private"},"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"nested","timestamp_ms":1780000000000,"token_usage":{"total_tokens":1,"query":"private"}}`,
		`{"schema":"other","session_id":"session","message_id":"schema","timestamp_ms":1780000000000,"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","message_id":"session","timestamp_ms":1780000000000,"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","timestamp_ms":1780000000000,"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"timestamp","token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"timestamp-float","timestamp_ms":1780000000000.5,"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"usage","timestamp_ms":1780000000000}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"total","timestamp_ms":1780000000000,"token_usage":{"prompt_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"negative","timestamp_ms":1780000000000,"token_usage":{"prompt_tokens":-1,"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"float","timestamp_ms":1780000000000,"token_usage":{"completion_tokens":0.5,"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"zero","timestamp_ms":1780000000000,"token_usage":{"total_tokens":0}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"overflow","timestamp_ms":1780000000000,"token_usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":3}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"integer-overflow","timestamp_ms":1780000000000,"token_usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1,"total_tokens":9223372036854775807}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"mode","timestamp_ms":1780000000000,"mode":"chat","token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"agent","timestamp_ms":1780000000000,"agent_type":"assistant","token_usage":{"total_tokens":1}}`,
	}
	lines := append(invalid, valid)
	records, warnings, err := NewTraeWorkCNAdapter().ParseFileWithWarnings(writeTraeWorkCNJSONL(t, strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("ParseFileWithWarnings: %v", err)
	}
	if len(records) != 1 || records[0].NativeEventID != "valid" {
		t.Fatalf("invalid lines must be skipped and valid data retained: %#v", records)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one aggregated warning, got %#v", warnings)
	}
	for _, want := range []string{
		"skipped 18 invalid TRAE Work CN snapshot line(s)",
		"invalid_agent_type=1",
		"invalid_json=1",
		"invalid_mode=1",
		"invalid_schema=3",
		"invalid_timestamp=1",
		"invalid_token_totals=3",
		"invalid_token_value=2",
		"missing_required_fields=6",
	} {
		if !strings.Contains(warnings[0], want) {
			t.Fatalf("warning missing %q: %s", want, warnings[0])
		}
	}
	for _, private := range []string{"private", "content\"", "user_info", "query"} {
		if strings.Contains(warnings[0], private) {
			t.Fatalf("warning leaked rejected source field %q: %s", private, warnings[0])
		}
	}
}

func TestTraeWorkCNMessageIdentityIsStableAcrossFilesAndUsageCorrections(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"same-session","message_id":"same-message","timestamp_ms":1780000000000,"model":"auto","token_usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":20}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"same-session","message_id":"same-message","timestamp_ms":1780000000000,"model":"claude-sonnet-4","token_usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":180}}`,
	}
	var eventIDs []string
	for index, line := range lines {
		path := filepath.Join(dir, fmt.Sprintf("snapshot-%d.jsonl", index))
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		records, err := NewTraeWorkCNAdapter().ParseFile(path)
		if err != nil || len(records) != 1 {
			t.Fatalf("parse snapshot %d: records=%d err=%v", index, len(records), err)
		}
		_, eventID, strategy, _, err := fingerprint.ComputeIdentity(records[0])
		if err != nil {
			t.Fatalf("compute identity: %v", err)
		}
		if strategy != fingerprint.StrategyNativeMessage {
			t.Fatalf("unexpected strategy %q", strategy)
		}
		eventIDs = append(eventIDs, eventID)
	}
	if eventIDs[0] != eventIDs[1] {
		t.Fatalf("model/token/path changes altered native message identity: %q != %q", eventIDs[0], eventIDs[1])
	}
}

func TestTraeWorkCNAdapterUsesUnknownFallbackForMissingOrAutoModel(t *testing.T) {
	path := writeTraeWorkCNJSONL(t, strings.Join([]string{
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"missing","timestamp_ms":1780000000000,"token_usage":{"total_tokens":5}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"auto","timestamp_ms":1780000000001,"model":"auto","token_usage":{"prompt_tokens":2,"total_tokens":5}}`,
	}, "\n"))
	records, err := NewTraeWorkCNAdapter().ParseFile(path)
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	if records[0].Model != "unknown" || records[0].ModelNormalized != "unknown" || records[0].ModelResolution != model.ModelResolutionUnknown || !records[0].ModelIsFallback || records[0].RawInputTokens != nil {
		t.Fatalf("unexpected missing-model fallback: %#v", records[0])
	}
	if records[1].Model != "auto" || records[1].ModelNormalized != "unknown" || records[1].ModelResolution != model.ModelResolutionUnknown || !records[1].ModelIsFallback || records[1].RawInputTokens == nil || *records[1].RawInputTokens != 2 {
		t.Fatalf("unexpected auto-model fallback: %#v", records[1])
	}
}

func TestTraeWorkCNDiscoverOnlyReturnsJSONLSnapshots(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "usage.jsonl"),
		filepath.Join(root, "nested", "usage.JSONL"),
		filepath.Join(root, "usage.json"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := NewTraeWorkCNAdapter().Discover([]string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(files) != 2 || filepath.Ext(files[0]) == ".json" || filepath.Ext(files[1]) == ".json" {
		t.Fatalf("unexpected discovered files: %#v", files)
	}
}

func writeTraeWorkCNJSONL(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertTraeWorkCNEnvelopeIsPrivate(t *testing.T, raw string) {
	t.Helper()
	for _, forbidden := range []string{
		"synthetic-session", "synthetic-message", "session_id", "message_id",
		"content", "query", "user_info", "credential", "https://", "url",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("sanitized envelope leaked %q: %s", forbidden, raw)
		}
	}
}

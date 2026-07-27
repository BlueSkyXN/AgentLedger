package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestWorkBuddyAdapterParsesUsageWithPrivacyEnvelope(t *testing.T) {
	path := writeWorkBuddyJSONL(t, strings.Join([]string{
		`not json`,
		`{"id":"skip-message","timestamp":1710000000000,"sessionId":"session-1","cwd":"/private/project","content":"must-not-be-saved","providerData":{"model":"deepseek-v4-pro","usage":{"requests":1},"rawUsage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}}`,
		`{"id":"req-1","timestamp":1710000000000,"sessionId":"session-1","cwd":"/private/project","content":"secret content","providerData":{"messageId":"message-1","conversationRequestId":"turn-1","model":"deepseek-v4-pro-202606","requestModelId":"custom-local:origin-deepseek-v4-pro","requestModelName":"Private Local Model","usage":{"requests":1},"rawUsage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":140,"prompt_tokens_details":{"cached_tokens":30},"prompt_cache_write_tokens":20,"completion_tokens_details":{"reasoning_tokens":7},"credit":123.45},"rawContent":"secret","url":"https://internal.invalid"}}`,
	}, "\n"))

	records, err := NewWorkBuddyAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one valid usage record, got %d", len(records))
	}
	rec := records[0]
	if rec.Agent != "workbuddy" || rec.SourceProduct != "workbuddy" || rec.Provider != "custom" {
		t.Fatalf("unexpected source/provider: agent=%q product=%q provider=%q", rec.Agent, rec.SourceProduct, rec.Provider)
	}
	if rec.Model != "deepseek-v4-pro-202606" || rec.ModelNormalized != "deepseek-v4-pro" {
		t.Fatalf("unexpected model raw=%q normalized=%q", rec.Model, rec.ModelNormalized)
	}
	if rec.DedupeID != "req-1" || rec.RequestID != "req-1" || rec.MessageID != "message-1" || rec.TurnID != "turn-1" || rec.SessionID != "session-1" || rec.ProjectPath != "/private/project" {
		t.Fatalf("unexpected identity fields: %#v", rec)
	}
	if rec.InputTokens != 50 || rec.CacheReadTokens != 30 || rec.CacheCreationTokens != 20 || rec.OutputTokens != 40 || rec.ReasoningTokens != 7 || rec.TotalTokens != 140 {
		t.Fatalf("unexpected token split: %#v", rec)
	}
	if rec.RawInputTokens == nil || *rec.RawInputTokens != 100 || rec.SourceTotalTokens == nil || *rec.SourceTotalTokens != 140 {
		t.Fatalf("unexpected source token diagnostics: raw=%v source=%v", rec.RawInputTokens, rec.SourceTotalTokens)
	}
	if rec.ObservabilityLevel != "full" || rec.TokenAccountingMethod != model.AccWorkBuddyRawUsage || rec.AccountingProfile != workBuddyAccountingProfile {
		t.Fatalf("unexpected accounting metadata: observability=%q method=%q profile=%q", rec.ObservabilityLevel, rec.TokenAccountingMethod, rec.AccountingProfile)
	}
	if rec.CostUSD != nil {
		t.Fatalf("credit must not become recorded USD cost: %v", rec.CostUSD)
	}
	assertWorkBuddyEnvelopeIsPrivate(t, rec.RawJSON)

	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(rec.RawJSON), &envelope); err != nil {
		t.Fatalf("unmarshal raw envelope: %v", err)
	}
	if envelope["route_kind"] != "custom-local" || envelope["usage_model"] != "deepseek-v4-pro-202606" || envelope["cached_tokens"] != float64(30) || envelope["cache_write_tokens"] != float64(20) || envelope["reasoning_tokens"] != float64(7) {
		t.Fatalf("unexpected raw envelope: %#v", envelope)
	}
}

func TestWorkBuddyAdapterPartialUsageAndAliases(t *testing.T) {
	path := writeWorkBuddyJSONL(t, strings.Join([]string{
		`{"id":"req-k3","timestamp":1710000000,"sessionId":"session","cwd":"/private","providerData":{"model":"k3","requestModelId":"k3","usage":{"requests":1},"rawUsage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}}`,
		`{"id":"req-kimi","timestamp":1710000001,"sessionId":"session","cwd":"/private","providerData":{"model":"kimi-k3-2","requestModelId":"kimi-k3-2","usage":{"requests":1},"rawUsage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_cache_write_tokens":0}}}`,
		`{"id":"req-auto","timestamp":1710000002,"sessionId":"session","cwd":"/private","providerData":{"model":"auto","requestModelId":"auto","usage":{"requests":1},"rawUsage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_write_tokens":0,"completion_tokens_details":{"reasoning_tokens":0}}}}`,
		`{"id":"req-unknown","timestamp":1710000003,"sessionId":"session","cwd":"/private","providerData":{"model":"other-model-v9","requestModelId":"other-model-v9","usage":{"requests":1},"rawUsage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_write_tokens":0,"completion_tokens_details":{"reasoning_tokens":0}}}}`,
	}, "\n"))

	records, err := NewWorkBuddyAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("expected four records, got %d", len(records))
	}
	if records[0].ModelNormalized != "kimi-k3" || records[0].Provider != "workbuddy" || records[0].ObservabilityLevel != "partial" || records[0].TimestampMs != 1710000000000 {
		t.Fatalf("unexpected k3 record: %#v", records[0])
	}
	if records[1].ModelNormalized != "kimi-k3" || records[1].ObservabilityLevel != "partial" {
		t.Fatalf("unexpected kimi record: %#v", records[1])
	}
	if records[2].Model != "auto" || records[2].ModelNormalized != "unknown" || records[2].ModelResolution != model.ModelResolutionUnknown || !records[2].ModelIsFallback || records[2].Provider != "workbuddy" || records[2].ObservabilityLevel != "full" {
		t.Fatalf("unexpected auto record: %#v", records[2])
	}
	if records[3].ModelNormalized != "other-model-v9" {
		t.Fatalf("unknown model must not use family fallback: %#v", records[3])
	}
}

func TestWorkBuddyAdapterSkipsInvalidUsage(t *testing.T) {
	valid := `{"id":"valid","timestamp":1710000000000,"sessionId":"session","cwd":"/private","providerData":{"model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_write_tokens":0,"completion_tokens_details":{"reasoning_tokens":0}}}}`
	invalid := []string{
		`{"timestamp":1710000000000,"sessionId":"session","cwd":"/private","providerData":{"model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}}`,
		`{"id":"not-numeric-timestamp","timestamp":"1710000000000","sessionId":"session","cwd":"/private","providerData":{"model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}}`,
		`{"id":"many-requests","timestamp":1710000000000,"sessionId":"session","cwd":"/private","providerData":{"model":"hy3","usage":{"requests":2},"rawUsage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}}`,
		`{"id":"mismatch","timestamp":1710000000000,"sessionId":"session","cwd":"/private","providerData":{"model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":4}}}`,
		`{"id":"negative","timestamp":1710000000000,"sessionId":"session","cwd":"/private","providerData":{"model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":-1,"completion_tokens":1,"total_tokens":0}}}`,
		`{"id":"cache-overflow","timestamp":1710000000000,"sessionId":"session","cwd":"/private","providerData":{"model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_write_tokens":1}}}`,
		`{"id":"reasoning-overflow","timestamp":1710000000000,"sessionId":"session","cwd":"/private","providerData":{"model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"completion_tokens_details":{"reasoning_tokens":2}}}}`,
	}
	lines := append([]string{"{not valid json"}, invalid...)
	lines = append(lines, valid)
	records, warnings, err := NewWorkBuddyAdapter().ParseFileWithWarnings(writeWorkBuddyJSONL(t, strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(records) != 1 || records[0].DedupeID != "valid" || records[0].ModelNormalized != "hy3" {
		t.Fatalf("invalid records should be skipped and later valid line retained: %#v", records)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one aggregated diagnostic warning, got %#v", warnings)
	}
	for _, want := range []string{"skipped 8 invalid WorkBuddy line(s)", "invalid_json=1", "invalid_request_count=1", "invalid_token_details=2", "invalid_token_totals=2", "missing_required_fields=2", "line 1 invalid_json", "3 more"} {
		if !strings.Contains(warnings[0], want) {
			t.Fatalf("warning missing %q: %s", want, warnings[0])
		}
	}
}

func TestWorkBuddyAdapterUsesRootIDRatherThanMessageIDForDedupe(t *testing.T) {
	lines := []string{
		`{"id":"root-a","timestamp":1710000000000,"sessionId":"session","cwd":"/private","providerData":{"messageId":"shared-message","conversationRequestId":"shared-turn","model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_write_tokens":0,"completion_tokens_details":{"reasoning_tokens":0}}}}`,
		`{"id":"root-b","timestamp":1710000000001,"sessionId":"session","cwd":"/private","providerData":{"messageId":"shared-message","conversationRequestId":"shared-turn","model":"hy3","usage":{"requests":1},"rawUsage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_write_tokens":0,"completion_tokens_details":{"reasoning_tokens":0}}}}`,
	}
	records, err := NewWorkBuddyAdapter().ParseFile(writeWorkBuddyJSONL(t, strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("shared message id must not collapse distinct root ids: %#v", records)
	}
	if records[0].DedupeID != "root-a" || records[1].DedupeID != "root-b" || records[0].MessageID != "shared-message" || records[1].TurnID != "shared-turn" {
		t.Fatalf("unexpected identity mapping: %#v", records)
	}
}

func TestWorkBuddyDiscoverRecursesButExcludesNonSessionDirectories(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "project", "session.jsonl"),
		filepath.Join(root, "project", "subagents", "agent.jsonl"),
		filepath.Join(root, "project", "logs", "ignored.jsonl"),
		filepath.Join(root, "project", "traces", "ignored.jsonl"),
		filepath.Join(root, "project", "audit-log", "ignored.jsonl"),
		filepath.Join(root, "project", "file-history", "ignored.jsonl"),
		filepath.Join(root, "project", "blobs", "ignored.jsonl"),
		filepath.Join(root, "project", "tool-output", "ignored.jsonl"),
		filepath.Join(root, "project", "other.json"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := NewWorkBuddyAdapter().Discover([]string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected session and subagent files only, got %#v", files)
	}
	for _, file := range files {
		if strings.Contains(file, "ignored") || filepath.Ext(file) != ".jsonl" {
			t.Fatalf("unexpected discovery result: %s", file)
		}
	}
}

func TestWorkBuddyLiveCorpus(t *testing.T) {
	if os.Getenv("WORKBUDDY_LIVE_TEST") != "1" {
		t.Skip("set WORKBUDDY_LIVE_TEST=1 to validate the local WorkBuddy corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	adapter := NewWorkBuddyAdapter()
	files, err := adapter.Discover([]string{filepath.Join(home, ".workbuddy", "projects")})
	if err != nil {
		t.Fatalf("discover live corpus: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no WorkBuddy JSONL files discovered")
	}

	var events, custom, auto int
	var total int64
	for _, file := range files {
		records, err := adapter.ParseFile(file)
		if err != nil {
			t.Fatalf("parse live WorkBuddy file: %v", err)
		}
		for _, rec := range records {
			events++
			total += rec.TotalTokens
			if rec.Provider == "custom" {
				custom++
			}
			if rec.Model == "auto" && rec.ModelNormalized == "unknown" {
				auto++
			}
			if rec.Agent != "workbuddy" || rec.SourceProduct != "workbuddy" || rec.DedupeID == "" || rec.ProjectPath == "" || rec.TotalTokens <= 0 {
				t.Fatalf("invalid live WorkBuddy record metadata")
			}
			assertWorkBuddyEnvelopeIsPrivate(t, rec.RawJSON)
		}
	}
	if events == 0 || total <= 0 {
		t.Fatalf("live corpus produced no usage: events=%d total=%d", events, total)
	}
	t.Logf("validated WorkBuddy corpus: files=%d events=%d total_tokens=%d custom=%d auto=%d", len(files), events, total, custom, auto)
}

func writeWorkBuddyJSONL(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertWorkBuddyEnvelopeIsPrivate(t *testing.T, raw string) {
	t.Helper()
	for _, forbidden := range []string{"credit", "content", "rawContent", "cwd", "secret", "https://", "url"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("raw envelope leaked %q: %s", forbidden, raw)
		}
	}
}

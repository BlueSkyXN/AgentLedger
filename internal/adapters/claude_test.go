package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeClaudeUsageFile(t *testing.T, lines ...string) string {
	t.Helper()
	return writeClaudeUsageFileForProject(t, "project-a", lines...)
}

func writeClaudeUsageFileForProject(t *testing.T, project string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".claude", "projects", project, "session-a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "chat.jsonl")
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestClaudeAdapterDedupesByMessageIDAndRequestIDKeepingLargestUsage(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","uuid":"uuid-low","timestamp":"2026-01-02T03:04:05Z","requestId":"req-1","sessionId":"session-real","message":{"id":"msg-1","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
		`{"type":"assistant","uuid":"uuid-high","timestamp":"2026-01-02T03:04:06Z","requestId":"req-1","sessionId":"session-real","message":{"id":"msg-1","model":"claude-sonnet","usage":{"input_tokens":20,"output_tokens":10,"cache_creation_input_tokens":5,"cache_read_input_tokens":0}}}`,
		`{"type":"assistant","uuid":"uuid-other-request","timestamp":"2026-01-02T03:04:07Z","requestId":"req-2","sessionId":"session-real","message":{"id":"msg-1","model":"claude-sonnet","usage":{"input_tokens":7,"output_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
	)

	adapter := NewClaudeAdapter()
	records, err := adapter.ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	records = adapter.PostProcessRecords(records)

	if len(records) != 2 {
		t.Fatalf("expected 2 deduped records, got %d", len(records))
	}
	foundReq1 := false
	foundReq2 := false
	for _, rec := range records {
		if rec.MessageID != "msg-1" {
			t.Fatalf("expected raw Claude message id, got %q", rec.MessageID)
		}
		switch rec.RequestID {
		case "req-1":
			foundReq1 = true
			if rec.InputTokens != 20 || rec.OutputTokens != 10 || rec.CacheCreationTokens != 5 {
				t.Fatalf("expected largest req-1 usage, got input=%d output=%d cache_create=%d", rec.InputTokens, rec.OutputTokens, rec.CacheCreationTokens)
			}
			if rec.IdentitySubkey != "msg-1:req-1" {
				t.Fatalf("unexpected req-1 identity subkey %q", rec.IdentitySubkey)
			}
		case "req-2":
			foundReq2 = true
			if rec.IdentitySubkey != "msg-1:req-2" {
				t.Fatalf("unexpected req-2 identity subkey %q", rec.IdentitySubkey)
			}
		default:
			t.Fatalf("unexpected request id %q", rec.RequestID)
		}
	}
	if !foundReq1 || !foundReq2 {
		t.Fatalf("missing deduped requests req1=%v req2=%v", foundReq1, foundReq2)
	}
}

func TestClaudeAdapterFallbacksToUUIDWhenMessageIDMissing(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","uuid":"uuid-only","timestamp":"2026-01-02T03:04:05Z","requestId":"req-1","message":{"model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].MessageID != "uuid-only" || records[0].NativeEventID != "uuid-only" {
		t.Fatalf("expected uuid fallback, message=%q native=%q", records[0].MessageID, records[0].NativeEventID)
	}
}

func TestClaudeAdapterSkipsSyntheticZeroAndTreatsNullSpeedAsAbsent(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","uuid":"synthetic","timestamp":"2026-01-02T03:04:05Z","message":{"id":"msg-synthetic","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
		`{"type":"assistant","uuid":"null-speed","timestamp":"2026-01-02T03:04:06Z","message":{"id":"msg-null","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5,"speed":null}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected only synthetic usage to be skipped, got %d", len(records))
	}
	if records[0].MessageID != "msg-null" || records[0].Model != "claude-sonnet" || records[0].UsageSpeed != "" {
		t.Fatalf("null speed must be treated as absent: %#v", records[0])
	}
}

func TestClaudeAdapterKeepsUsageWithOptionalNullFields(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","uuid":"optional-null","timestamp":"2026-01-02T03:04:05Z","cwd":null,"costUSD":null,"sessionId":null,"message":{"id":"msg-null-optional","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":null,"cache_read_input_tokens":null}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected optional null fields to preserve usage, got %d records", len(records))
	}
	if records[0].InputTokens != 10 || records[0].OutputTokens != 5 {
		t.Fatalf("unexpected usage: %#v", records[0])
	}
}

func TestClaudeAdapterFastModelSuffix(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","uuid":"uuid-fast","timestamp":"2026-01-02T03:04:05Z","message":{"id":"msg-fast","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5,"speed":"fast"}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Model != "claude-sonnet-fast" || records[0].UsageSpeed != "fast" {
		t.Fatalf("expected fast suffix, model=%q speed=%q", records[0].Model, records[0].UsageSpeed)
	}
}

func TestClaudeAdapterKeepsOpenCoworkAsProjectPath(t *testing.T) {
	path := writeClaudeUsageFileForProject(t, "-Users-test-Github-open-cowork",
		`{"type":"assistant","uuid":"uuid-cowork","timestamp":"2026-01-02T03:04:05Z","cwd":"/Users/test/Github/open-cowork","message":{"id":"msg-cowork","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ProjectPath != "/Users/test/Github/open-cowork" {
		t.Fatalf("expected cwd project path, got %q", records[0].ProjectPath)
	}
	if records[0].SourceProduct != "claude-code" {
		t.Fatalf("unexpected Claude source product %q", records[0].SourceProduct)
	}
}

func TestClaudeAdapterParsesNestedAgentProgressUsage(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"data":{"message":{"timestamp":"2026-01-02T03:04:05Z","requestId":"req-nested","isSidechain":true,"message":{"id":"msg-nested","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec.MessageID != "msg-nested" || rec.RequestID != "req-nested" || !rec.IsSidechain {
		t.Fatalf("unexpected nested identity message=%q request=%q sidechain=%v", rec.MessageID, rec.RequestID, rec.IsSidechain)
	}
	if rec.InputTokens != 10 || rec.OutputTokens != 5 || rec.CacheCreationTokens != 2 || rec.CacheReadTokens != 3 {
		t.Fatalf("unexpected nested usage input=%d output=%d cache_create=%d cache_read=%d", rec.InputTokens, rec.OutputTokens, rec.CacheCreationTokens, rec.CacheReadTokens)
	}
}

func TestClaudeAdapterSidechainReplayPrefersNonSidechain(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","uuid":"uuid-side","timestamp":"2026-01-02T03:04:05Z","isSidechain":true,"requestId":"req-side","message":{"id":"msg-replay","model":"claude-sonnet","usage":{"input_tokens":100,"output_tokens":50}}}`,
		`{"type":"assistant","uuid":"uuid-main","timestamp":"2026-01-02T03:04:06Z","isSidechain":false,"requestId":"req-main","message":{"id":"msg-replay","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5}}}`,
	)

	adapter := NewClaudeAdapter()
	records, err := adapter.ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	records = adapter.PostProcessRecords(records)
	if len(records) != 1 {
		t.Fatalf("expected sidechain replay to dedupe, got %d", len(records))
	}
	if records[0].RequestID != "req-main" || records[0].IsSidechain {
		t.Fatalf("expected non-sidechain record to win, request=%q sidechain=%v", records[0].RequestID, records[0].IsSidechain)
	}
}

func TestClaudeAdapterUsesFullSourceOnlyForFingerprint(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","uuid":"uuid-compact","timestamp":"2026-01-02T03:04:05Z","cwd":"/private/project","content":"secret message body","thinking":"secret reasoning","isSidechain":true,"costUSD":1.25,"message":{"id":"msg-compact","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5,"speed":"fast"}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	rec := records[0]
	if rec.Model != "claude-sonnet-fast" || rec.InputTokens != 10 || rec.OutputTokens != 5 || !rec.IsSidechain {
		t.Fatalf("structured Claude parsing changed: %#v", rec)
	}
	if rec.FingerprintJSON == "" || rec.RawSHA256 != sha256Hex([]byte(rec.FingerprintJSON)) {
		t.Fatalf("fingerprint source state invalid: %#v", rec)
	}
	if !strings.Contains(rec.FingerprintJSON, "secret message body") || !strings.Contains(rec.FingerprintJSON, "/private/project") {
		t.Fatalf("full source must remain available for fingerprinting: %s", rec.FingerprintJSON)
	}
}

func TestClaudeAdapterUsesWrappedSourceForFingerprint(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"data":{"message":{"timestamp":"2026-01-02T03:04:05Z","requestId":"req-wrapped","isSidechain":true,"content":"secret wrapper","message":{"id":"msg-wrapped","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5}}}}}`,
	)
	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 || !strings.Contains(records[0].FingerprintJSON, "secret wrapper") {
		t.Fatalf("wrapped fingerprint source invalid: %#v", records)
	}
}

func TestClaudeAdapterKeepsFingerprintSourceWhenIdentityIsMissing(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"timestamp":"2026-01-02T03:04:05Z","message":{"model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5}}}`,
	)
	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	rec := records[0]
	if rec.FingerprintJSON == "" || rec.RawSHA256 != sha256Hex([]byte(rec.FingerprintJSON)) {
		t.Fatalf("missing-identity fingerprint source invalid: %#v", rec)
	}
}

func TestClaudeDiscoverPathsExpandLegacyRootToProjectsAndXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatalf("mkdir legacy projects: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "claude", "projects"), 0o755); err != nil {
		t.Fatalf("mkdir xdg projects: %v", err)
	}

	paths := normalizeClaudeDiscoverPaths([]string{"~/.claude"})
	wantLegacy := filepath.Join(home, ".claude", "projects")
	wantXDG := filepath.Join(home, ".config", "claude", "projects")
	if len(paths) != 2 || paths[0] != wantLegacy || paths[1] != wantXDG {
		t.Fatalf("unexpected normalized paths: %#v", paths)
	}
}

func TestClaudeAdapterParsesCacheCreationTTLSplit(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","timestamp":"2026-01-02T03:04:05Z","requestId":"req-5m","sessionId":"s","message":{"id":"msg-5m","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":300,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":300,"ephemeral_1h_input_tokens":0}}}}`,
		`{"type":"assistant","timestamp":"2026-01-02T03:04:06Z","requestId":"req-1h","sessionId":"s","message":{"id":"msg-1h","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":400,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":400}}}}`,
		`{"type":"assistant","timestamp":"2026-01-02T03:04:07Z","requestId":"req-mixed","sessionId":"s","message":{"id":"msg-mixed","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":500,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":380,"ephemeral_1h_input_tokens":120}}}}`,
		`{"type":"assistant","timestamp":"2026-01-02T03:04:08Z","requestId":"req-legacy","sessionId":"s","message":{"id":"msg-legacy","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":600,"cache_read_input_tokens":0}}}`,
		`{"type":"assistant","timestamp":"2026-01-02T03:04:09Z","requestId":"req-over","sessionId":"s","message":{"id":"msg-over","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":50,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_1h_input_tokens":70}}}}`,
		`{"type":"assistant","timestamp":"2026-01-02T03:04:10Z","requestId":"req-empty","sessionId":"s","message":{"id":"msg-empty","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{}}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]*int64{
		"msg-5m":     int64Ptr(0),
		"msg-1h":     int64Ptr(400),
		"msg-mixed":  int64Ptr(120),
		"msg-legacy": nil,
		"msg-over":   int64Ptr(50),
		"msg-empty":  nil,
	}
	if len(records) != len(want) {
		t.Fatalf("expected %d records, got %d", len(want), len(records))
	}
	for _, rec := range records {
		expected, ok := want[rec.MessageID]
		if !ok {
			t.Fatalf("unexpected message %q", rec.MessageID)
		}
		got := rec.CacheCreation1hTokens
		switch {
		case expected == nil && got != nil:
			t.Fatalf("%s: expected unknown 1h split, got %d", rec.MessageID, *got)
		case expected != nil && got == nil:
			t.Fatalf("%s: expected 1h split %d, got unknown", rec.MessageID, *expected)
		case expected != nil && *got != *expected:
			t.Fatalf("%s: expected 1h split %d, got %d", rec.MessageID, *expected, *got)
		}
	}
}

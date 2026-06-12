package adapters

import (
	"os"
	"path/filepath"
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
			if rec.DedupeID != "msg-1:req-1" {
				t.Fatalf("unexpected req-1 dedupe id %q", rec.DedupeID)
			}
		case "req-2":
			foundReq2 = true
			if rec.DedupeID != "msg-1:req-2" {
				t.Fatalf("unexpected req-2 dedupe id %q", rec.DedupeID)
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
	if records[0].MessageID != "uuid-only" || records[0].DedupeID != "uuid-only" {
		t.Fatalf("expected uuid fallback, message=%q dedupe=%q", records[0].MessageID, records[0].DedupeID)
	}
}

func TestClaudeAdapterSkipsSyntheticZeroAndUnsupportedNull(t *testing.T) {
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","uuid":"synthetic","timestamp":"2026-01-02T03:04:05Z","message":{"id":"msg-synthetic","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
		`{"type":"assistant","uuid":"null-speed","timestamp":"2026-01-02T03:04:06Z","message":{"id":"msg-null","model":"claude-sonnet","usage":{"input_tokens":10,"output_tokens":5,"speed":null}}}`,
	)

	records, err := NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected unsupported/synthetic records to be skipped, got %d", len(records))
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
	if records[0].SourceProduct != "" {
		t.Fatalf("Claude adapter should not infer source product from project path, got %q", records[0].SourceProduct)
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

package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeCodexAcceptsLineLargerThanTenMiB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-large.jsonl")

	const prefix = `{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","session_id":"large-line","payload":{"type":"token_count","model":"gpt-5-codex","info":{"last_token_usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}},"padding":"`
	const suffix = `"}` + "\n"
	const payloadBytes = 10*1024*1024 + 1024

	var fixture strings.Builder
	fixture.Grow(len(prefix) + payloadBytes + len(suffix))
	fixture.WriteString(prefix)
	fixture.WriteString(strings.Repeat("x", payloadBytes))
	fixture.WriteString(suffix)
	if err := os.WriteFile(path, []byte(fixture.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	diagnostics, err := AnalyzeCodex([]string{dir}, CodexDuplicatePolicyLedger)
	if err != nil {
		t.Fatalf("analyze Codex file larger than 10 MiB: %v", err)
	}
	if diagnostics.Files != 1 || diagnostics.Lines != 1 || diagnostics.TokenCountEvents != 1 {
		t.Fatalf(
			"unexpected diagnostics: files=%d lines=%d token_count_events=%d",
			diagnostics.Files,
			diagnostics.Lines,
			diagnostics.TokenCountEvents,
		)
	}
	if diagnostics.LedgerStats.Events != 1 || diagnostics.LedgerStats.TotalTokens != 2 {
		t.Fatalf(
			"unexpected ledger stats: events=%d total_tokens=%d",
			diagnostics.LedgerStats.Events,
			diagnostics.LedgerStats.TotalTokens,
		)
	}
}

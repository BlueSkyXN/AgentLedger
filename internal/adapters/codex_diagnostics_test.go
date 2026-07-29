package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexPolicyComparisonRejectsDifferentQuarantineOutcome(t *testing.T) {
	ledger := []codexReplayFileOutcome{{exactEvents: 1, exactTokens: 12}}
	ccusage := []codexReplayFileOutcome{{exactEvents: 1, exactTokens: 12, quarantined: true}}

	err := validateCodexReplayPolicyOutcomes(ledger, ccusage)
	if err == nil || !strings.Contains(err.Error(), "quarantine") {
		t.Fatalf("different quarantine outcome must make comparison inconclusive, got %v", err)
	}
}

func TestCodexPolicyComparisonAllowsDifferentAccountingTokensForSameSkipSet(t *testing.T) {
	ledger := []codexReplayFileOutcome{{exactEvents: 1, exactTokens: 12}}
	ccusage := []codexReplayFileOutcome{{exactEvents: 1, exactTokens: 20}}

	if err := validateCodexReplayPolicyOutcomes(ledger, ccusage); err != nil {
		t.Fatalf("policy-specific token amounts must not change replay skip parity: %v", err)
	}
}

func TestAnalyzeCodexDetectsSnapshotDriftBetweenPolicies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-drift.jsonl")
	fixture := `{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}}` + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	mutated := false
	_, err := analyzeCodexDiscoveredFiles(
		[]string{path},
		[]string{dir},
		CodexDuplicatePolicyLedger,
		func(current string) {
			if mutated {
				return
			}
			mutated = true
			file, openErr := os.OpenFile(current, os.O_APPEND|os.O_WRONLY, 0)
			if openErr != nil {
				t.Fatalf("open fixture for drift injection: %v", openErr)
			}
			if _, writeErr := file.WriteString("\n"); writeErr != nil {
				_ = file.Close()
				t.Fatalf("mutate fixture: %v", writeErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				t.Fatalf("close mutated fixture: %v", closeErr)
			}
		},
	)
	if err == nil || !strings.Contains(err.Error(), "inconclusive") || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("snapshot drift must make policy comparison inconclusive, got %v", err)
	}
}

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

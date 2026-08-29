package adapters

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func writeCursorLog(t *testing.T, root, run, name string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, "logs", run, "window1", "exthost", cursorAgentExecDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func cursorUsageLine(timestamp, action, modelName string, used, input, output, cacheRead, cacheWrite int64) string {
	return fmt.Sprintf(`%s [info] Setting token details for client token ring {"action":%q,"cacheReadTokens":%d,"cacheWriteTokens":%d,"inputTokens":%d,"maxTokens":1000000,"modelName":%q,"outputTokens":%d,"usedTokens":%d}`,
		timestamp, action, cacheRead, cacheWrite, input, modelName, output, used)
}

func TestCursorAdapterDiscoversOnlyAgentExecLogs(t *testing.T) {
	root := t.TempDir()
	want := writeCursorLog(t, root, "run-a", "Cursor Agent Exec.workspaceId-synthetic.log", "not usage")
	otherDir := filepath.Join(root, "logs", "run-a", "window1", "exthost", cursorAgentExecDir)
	for _, name := range []string{"Cursor Structured Logs.log", "Cursor Plugins.log", "Cursor Agent Exec.txt"} {
		if err := os.WriteFile(filepath.Join(otherDir, name), []byte("ignored\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wrongDir := filepath.Join(root, "logs", "run-a", "window1", "exthost", "other.extension")
	if err := os.MkdirAll(wrongDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrongDir, "Cursor Agent Exec.log"), []byte("ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrongParent := filepath.Join(root, "logs", "run-a", "window1", "not-exthost", cursorAgentExecDir)
	if err := os.MkdirAll(wrongParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrongParent, "Cursor Agent Exec.log"), []byte("ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := NewCursorAdapter().Discover([]string{filepath.Join(root, "logs")})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != want {
		t.Fatalf("unexpected Cursor discovery: %#v", files)
	}
}

func TestCursorAdapterParsesExplicitUsageAndSplitsCache(t *testing.T) {
	root := t.TempDir()
	path := writeCursorLog(t, root, "run-a", "Cursor Agent Exec.log",
		"2026-08-20 09:50:33.000 [info] unrelated line",
		cursorUsageLine("2026-08-20 09:50:34.089", "userMessageAction", "gpt-5.6-sol(max)", 161476, 161323, 153, 160256, 0),
	)
	records, warnings, err := NewCursorAdapter().ParseFileWithWarnings(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || len(records) != 1 {
		t.Fatalf("records=%d warnings=%v", len(records), warnings)
	}
	rec := records[0]
	wantTimestamp := time.Date(2026, 8, 20, 9, 50, 34, 89_000_000, time.Local).UnixMilli()
	if rec.TimestampMs != wantTimestamp || rec.Agent != "cursor" || rec.SourceProduct != "cursor-agent-exec" || rec.Provider != "unknown" {
		t.Fatalf("unexpected source facts: %#v", rec)
	}
	if rec.Model != "gpt-5.6-sol(max)" || rec.ModelNormalized != "gpt-5.6-sol" || rec.ModelResolution != model.ModelResolutionDirectEvent {
		t.Fatalf("unexpected model facts: %#v", rec)
	}
	if rec.InputTokens != 1067 || rec.CacheReadTokens != 160256 || rec.CacheCreationTokens != 0 || rec.OutputTokens != 153 || rec.TotalTokens != 161476 {
		t.Fatalf("unexpected token split: %#v", rec)
	}
	if rec.RawInputTokens == nil || *rec.RawInputTokens != 161323 || rec.SourceTotalTokens == nil || *rec.SourceTotalTokens != 161476 {
		t.Fatalf("unexpected source totals: %#v", rec)
	}
	if rec.IdentityScope != "global" || rec.IdentitySubkey != "userMessageAction" || rec.TokenAccountingMethod != model.AccCursorAgentExec || rec.AccountingProfile != cursorAccountingProfile || rec.ObservabilityLevel != "partial" {
		t.Fatalf("unexpected identity/accounting: %#v", rec)
	}
	if rec.SessionPathID != "cursor-agent-exec/2026-08-20" || strings.Contains(rec.SessionPathID, root) {
		t.Fatalf("session fallback must be a stable local-date bucket: %q", rec.SessionPathID)
	}
	_, _, strategy, _, err := fingerprint.ComputeIdentity(rec)
	if err != nil || strategy != fingerprint.StrategyContentFallback {
		t.Fatalf("unexpected identity strategy=%q err=%v", strategy, err)
	}
}

func TestCursorAdapterSkipsZeroAndAggregatesInvalidUsage(t *testing.T) {
	valid := cursorUsageLine("2026-08-20 09:50:34.089", "userMessageAction", "gpt-5.6-sol", 15, 10, 5, 3, 0)
	lines := []string{
		cursorUsageLine("2026-08-20 09:50:30.000", "userMessageAction", "gpt-5.6-sol", 0, 0, 0, 0, 0),
		"bad-time [info] Setting token details for client token ring {}",
		"2026-08-20 09:50:31.000 [info] Setting token details for client token ring {not-json}",
		"2026-08-20 09:50:32.000 [info] Setting token details for client token ring {\"action\":\"userMessageAction\"}",
		cursorUsageLine("2026-08-20 09:50:33.000", "userMessageAction", "gpt-5.6-sol", 15, 10, 5, 11, 0),
		cursorUsageLine("2026-08-20 09:50:33.500", "userMessageAction", "gpt-5.6-sol", 16, 10, 5, 0, 0),
		valid,
	}
	path := writeCursorLog(t, t.TempDir(), "run-a", "Cursor Agent Exec.log", lines...)
	records, warnings, err := NewCursorAdapter().ParseFileWithWarnings(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].TotalTokens != 15 {
		t.Fatalf("invalid/zero usage should be skipped: %#v", records)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one aggregate warning: %#v", warnings)
	}
	for _, want := range []string{"skipped 5 invalid Cursor usage line(s)", "invalid_json=1", "invalid_timestamp=1", "invalid_token_details=1", "invalid_token_totals=1", "missing_required_fields=1"} {
		if !strings.Contains(warnings[0], want) {
			t.Fatalf("warning missing %q: %s", want, warnings[0])
		}
	}
}

func TestCursorAdapterSemanticDedupeIsPathIndependent(t *testing.T) {
	line := cursorUsageLine("2026-08-20 09:50:34.089", "resumeAction", "grok-4.6", 110, 100, 10, 80, 0)
	root := t.TempDir()
	paths := []string{
		writeCursorLog(t, root, "run-a", "Cursor Agent Exec.log", line),
		writeCursorLog(t, root, "run-b", "Cursor Agent Exec.1.log", line),
	}
	adapter := NewCursorAdapter()
	var raw []*fingerprint.ParsedRecord
	var eventIDs []string
	for _, path := range paths {
		records, err := adapter.ParseFile(path)
		if err != nil || len(records) != 1 {
			t.Fatalf("parse %s records=%d err=%v", filepath.Base(path), len(records), err)
		}
		_, eventID, _, _, err := fingerprint.ComputeIdentity(records[0])
		if err != nil {
			t.Fatal(err)
		}
		eventIDs = append(eventIDs, eventID)
		raw = append(raw, records...)
	}
	if eventIDs[0] != eventIDs[1] {
		t.Fatalf("copied usage changed event identity: %q != %q", eventIDs[0], eventIDs[1])
	}
	deduped := adapter.PostProcessRecords(raw)
	if len(deduped) != 1 || adapter.semanticDuplicates != 1 {
		t.Fatalf("deduped=%d duplicates=%d", len(deduped), adapter.semanticDuplicates)
	}
	diagnostics := adapter.ImportDiagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Code != "cursor_semantic_duplicates" || diagnostics[0].Events != 1 {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
}

func TestCursorAdapterActionSeparatesSameTimestampAndUsage(t *testing.T) {
	path := writeCursorLog(t, t.TempDir(), "run-a", "Cursor Agent Exec.log",
		cursorUsageLine("2026-08-20 09:50:34.089", "userMessageAction", "gpt-5.6-sol", 15, 10, 5, 0, 0),
		cursorUsageLine("2026-08-20 09:50:34.089", "resumeAction", "gpt-5.6-sol", 15, 10, 5, 0, 0),
	)
	adapter := NewCursorAdapter()
	records, err := adapter.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records = adapter.PostProcessRecords(records)
	if len(records) != 2 {
		t.Fatalf("actions must separate otherwise identical usage: %#v", records)
	}
	_, first, _, _, _ := fingerprint.ComputeIdentity(records[0])
	_, second, _, _, _ := fingerprint.ComputeIdentity(records[1])
	if first == second {
		t.Fatal("different actions produced the same event ID")
	}
}

func TestCursorLiveCorpus(t *testing.T) {
	if os.Getenv("CURSOR_LIVE_TEST") != "1" {
		t.Skip("set CURSOR_LIVE_TEST=1 to validate the local Cursor corpus")
	}
	adapter := NewCursorAdapter()
	files, err := adapter.Discover(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no Cursor Agent Exec logs discovered")
	}
	var records []*fingerprint.ParsedRecord
	for _, path := range files {
		parsed, warnings, err := adapter.ParseFileWithWarnings(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(warnings) != 0 {
			t.Fatalf("live Cursor parser warnings: %v", warnings)
		}
		records = append(records, parsed...)
	}
	records = adapter.PostProcessRecords(records)
	if len(records) == 0 {
		t.Fatal("no non-zero Cursor usage records parsed")
	}
	var total int64
	for _, record := range records {
		if record.RawInputTokens == nil || record.SourceTotalTokens == nil ||
			*record.RawInputTokens != record.InputTokens+record.CacheReadTokens+record.CacheCreationTokens ||
			*record.SourceTotalTokens != *record.RawInputTokens+record.OutputTokens || record.TotalTokens != *record.SourceTotalTokens {
			t.Fatalf("live Cursor accounting mismatch: %#v", record)
		}
		if _, _, _, _, err := fingerprint.ComputeIdentity(record); err != nil {
			t.Fatalf("live Cursor identity: %v", err)
		}
		total += record.TotalTokens
	}
	t.Logf("Cursor live corpus: files=%d events=%d tokens=%d semantic_duplicates=%d", len(files), len(records), total, adapter.semanticDuplicates)
}

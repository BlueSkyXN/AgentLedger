package db

import (
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func BenchmarkRepeatedExactCodexUpsert(b *testing.B) {
	benchmarkRepeatedExactCodexUpsert(b, false)
}

func BenchmarkRepeatedExactCodexUpsertWithUnrelatedRedactedRow(b *testing.B) {
	benchmarkRepeatedExactCodexUpsert(b, true)
}

func benchmarkRepeatedExactCodexUpsert(b *testing.B, seedRedactedRow bool) {
	database, err := Open(filepath.Join(b.TempDir(), "agent-ledger.db"))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer database.Close()

	event := &model.UsageEvent{
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "openai",
		ModelRaw:        "gpt-5-codex",
		ModelNormalized: "gpt-5-codex",
		TimestampMs:     1,
		SessionID:       "benchmark-session",
		MessageID:       "benchmark-message",
		SourceFile:      "/synthetic/benchmark.jsonl",
		LineNumber:      1,
		RawSHA256:       "benchmark-raw-hash",
		RawUsageJSON:    `{"type":"event_msg","payload":{"type":"token_count"}}`,
		InputTokens:     15,
		OutputTokens:    5,
		TotalTokens:     20,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	setUsageEventFingerprintForTest(event)
	if status, err := database.UpsertEvent(event); err != nil || status != "inserted" {
		b.Fatalf("initial upsert status=%s err=%v", status, err)
	}
	if seedRedactedRow {
		redacted := *event
		redacted.EventID = "benchmark-unrelated-redacted"
		redacted.DedupeKey = redacted.EventID
		redacted.SessionID = "benchmark-unrelated-session"
		redacted.MessageID = ""
		redacted.SourceFile = "/synthetic/unrelated-redacted.jsonl"
		redacted.LineNumber = 2
		redacted.RawSHA256 = "benchmark-unrelated-raw-hash"
		redacted.RawUsageJSON = ""
		if err := insertEvent(database.Conn(), &redacted); err != nil {
			b.Fatalf("insert unrelated redacted row: %v", err)
		}
		if _, err := database.Conn().Exec(`UPDATE usage_events SET source_file = NULL WHERE event_id = ?`, redacted.EventID); err != nil {
			b.Fatalf("redact unrelated row: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if status, err := database.UpsertEvent(event); err != nil || status != "skipped" {
			b.Fatalf("repeated upsert status=%s err=%v", status, err)
		}
	}
}

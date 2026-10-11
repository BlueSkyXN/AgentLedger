package adapters

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func writeSourceFormat(t *testing.T, root, relative, text string) string {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGeminiStoredChatAndStatsFormats(t *testing.T) {
	message := `{"id":"m","type":"gemini","timestamp":"2026-01-01T00:00:10Z","model":"gemini-2.5-pro","tokens":{"input":100,"output":20,"cached":30,"thoughts":5,"tool":2,"total":127}}`
	stats := `{"models":{"gemini-2.5-pro":{"tokens":{"input":100,"output":20,"cached":30,"thoughts":5,"tool":2,"total":127}}}}`
	for _, fixture := range []struct{ name, ext, content string }{
		{"chat", ".json", `{"sessionId":"s","startTime":"2026-01-01T00:00:00Z","messages":[` + message + `]}`},
		{"jsonl", ".jsonl", `{"sessionId":"s","startTime":"2026-01-01T00:00:00Z"}` + "\n" + message + "\n" + message},
		{"stats", ".json", `{"sessionId":"s","timestamp":"2026-01-01T00:00:10Z","stats":` + stats + `}`},
		{"result", ".json", `{"sessionId":"s","timestamp":"2026-01-01T00:00:10Z","result":{"stats":` + stats + `}}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := writeSourceFormat(t, t.TempDir(), ".gemini/tmp/p/chats/s"+fixture.ext, fixture.content)
			adapter := NewGeminiAdapter()
			records, err := adapter.ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			records = adapter.PostProcessRecords(records)
			if len(records) != 1 {
				t.Fatalf("records=%d", len(records))
			}
			rec := records[0]
			if rec.InputTokens != 72 || rec.CacheReadTokens != 30 || rec.OutputTokens != 20 || rec.ReasoningTokens != 5 || rec.TotalTokens != 127 {
				t.Fatalf("usage=%+v", rec)
			}
			if _, _, _, _, err := fingerprint.ComputeIdentity(rec); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGeminiMessageAndStatsDoNotDoubleCount(t *testing.T) {
	lines := `{"sessionId":"s","startTime":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"id":"m","type":"gemini","timestamp":"2026-01-01T00:00:10Z","model":"gemini-2.5-pro","tokens":{"input":100,"output":20,"cached":30,"total":120}}` + "\n" +
		`{"timestamp":"2026-01-01T00:01:00Z","stats":{"models":{"gemini-2.5-pro":{"tokens":{"input":70,"cached":30,"output":20,"total":120}}}}}`
	path := writeSourceFormat(t, t.TempDir(), ".gemini/tmp/p/chats/s.jsonl", lines)
	adapter := NewGeminiAdapter()
	records, err := adapter.ParseFile(path)
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	if records[1].InputTokens != 70 || records[1].TotalTokens != 120 {
		t.Fatalf("stats usage=%+v", records[1])
	}
	records = adapter.PostProcessRecords(records)
	if len(records) != 1 || records[0].TotalTokens != 120 || records[0].Granularity != "request" {
		t.Fatalf("records=%+v", records)
	}
}

func TestClaudeNestedAdvisorAndRequestlessSidechain(t *testing.T) {
	lines := `{"sessionId":"s","data":{"message":{"timestamp":"2026-01-01T00:00:00Z","isSidechain":true,"message":{"id":"m","model":"claude-sonnet-4","usage":{"input_tokens":10,"output_tokens":2,"iterations":[{"type":"advisor_message","model":"claude-opus-4","input_tokens":20,"output_tokens":3}]}}}}}` + "\n" +
		`{"sessionId":"s","timestamp":"2026-01-01T00:00:10Z","message":{"id":"m","model":"claude-sonnet-4","usage":{"input_tokens":10,"output_tokens":2,"iterations":[{"type":"advisor_message","model":"claude-opus-4","input_tokens":20,"output_tokens":3}]}}}`
	path := writeSourceFormat(t, t.TempDir(), "projects/p/s.jsonl", lines)
	adapter := NewClaudeAdapter()
	records, err := adapter.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records = adapter.PostProcessRecords(records)
	if len(records) != 2 {
		t.Fatalf("records=%d", len(records))
	}
	for _, rec := range records {
		if rec.IsSidechain {
			t.Fatal("sidechain replay survived")
		}
	}
}

func codexFormatUsage(timestamp string, input, output int) string {
	return fmt.Sprintf(`{"type":"event_msg","timestamp":%q,"payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}}}`, timestamp, input, output, input+output)
}

func TestCodexCompactionPairingAndCoverage(t *testing.T) {
	request := `{"type":"token_usage_record","timestamp":"2026-01-01T00:00:01Z","payload":{"response_id":"compact","usage":{"input_tokens":300,"output_tokens":20,"total_tokens":320}}}`
	marker := `{"type":"compacted","timestamp":"2026-01-01T00:00:03Z","payload":{"compaction_response_id":"compact"}}`
	base := `{"type":"turn_context","payload":{"model":"gpt-6-astra"}}` + "\n" + codexFormatUsage("2026-01-01T00:00:00Z", 100, 20) + "\n"
	for _, fixture := range []struct {
		name, tail string
		want       int64
	}{
		{"remote", request + "\n" + marker, 440},
		{"marker_first", marker + "\n" + request, 440},
		{"unmatched", request, 120},
		{"duplicate", request + "\n" + marker + "\n" + request + "\n" + marker, 440},
		{"covered", request + "\n" + codexFormatUsage("2026-01-01T00:00:02Z", 400, 40) + "\n" + marker, 440},
		{"equal_later_request", request + "\n" + marker + "\n" + codexFormatUsage("2026-01-01T00:00:04Z", 400, 40), 760},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := writeSourceFormat(t, t.TempDir(), "sessions/s.jsonl", base+fixture.tail)
			adapter := NewCodexAdapter()
			records, err := adapter.ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			records = adapter.PostProcessRecords(records)
			var total int64
			for _, rec := range records {
				total += rec.TotalTokens
			}
			if total != fixture.want {
				t.Fatalf("total=%d want=%d", total, fixture.want)
			}
		})
	}
}

func TestCodexTierInheritanceAndUnknownClear(t *testing.T) {
	lines := `{"type":"turn_context","payload":{"model":"gpt-6-astra"}}` + "\n"
	for index, tier := range []string{`"priority"`, `null`, `"standard"`, `"unrecognized"`} {
		settings := `{"model":"gpt-6-astra"}`
		if tier != "null" {
			settings = `{"service_tier":` + tier + `}`
		}
		lines += `{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":` + settings + `}}` + "\n" + codexFormatUsage(fmt.Sprintf("2026-01-01T00:00:%02dZ", index+1), 10*(index+1), 0) + "\n"
	}
	path := writeSourceFormat(t, t.TempDir(), "sessions/s.jsonl", lines)
	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil || len(records) != 4 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	for i, want := range []string{"priority", "priority", "default", ""} {
		if got := model.ServiceTierFromAccountingProfile("codex", records[i].AccountingProfile); got != want {
			t.Fatalf("tier %d=%q want=%q", i, got, want)
		}
	}
}

func TestCodexArchiveDiscoveryPrefersActiveCopy(t *testing.T) {
	root := t.TempDir()
	active := writeSourceFormat(t, root, "sessions/day/s.jsonl", "{}")
	writeSourceFormat(t, root, "archived_sessions/day/s.jsonl", "{}")
	archived := writeSourceFormat(t, root, "archived_sessions/day/other.jsonl", "{}")
	files, err := NewCodexAdapter().Discover([]string{filepath.Join(root, "sessions")})
	if err != nil || len(files) != 2 || files[0] != active || files[1] != archived {
		t.Fatalf("files=%v err=%v", files, err)
	}
}

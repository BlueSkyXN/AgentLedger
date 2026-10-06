package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func TestCodexSemanticDedupePreservesCumulativeProgress(t *testing.T) {
	for _, nativeIDs := range []bool{false, true} {
		t.Run(fmt.Sprintf("native_ids=%t", nativeIDs), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			line := func(id string, input, output, total int) string {
				identity := ""
				if nativeIDs {
					identity = fmt.Sprintf(`"event_id":%q,`, id)
				}
				return fmt.Sprintf(`{%s"type":"event_msg","timestamp":"2026-01-01T00:00:10Z","session_id":"synthetic-session","model":"gpt-5","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}}}`, identity, input, output, total)
			}
			data := strings.Join([]string{line("first", 80, 20, 100), line("second", 160, 40, 200)}, "\n")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			database, err := db.Open(filepath.Join(t.TempDir(), "import.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			records, err := adapters.NewCodexAdapter().ParseFile(path)
			if err != nil || len(records) != 2 || records[0].TotalTokens != 100 || records[1].TotalTokens != 100 {
				t.Fatalf("cumulative parse: records=%+v err=%v", records, err)
			}
			added, updated, skipped, rejected, warnings := importParsedRecords(database, "codex", records)
			if added != 2 || updated != 0 || skipped != 0 || rejected != 0 || len(warnings) != 0 {
				t.Fatalf("first import=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
			}
			if err := os.WriteFile(path, []byte("{}\n{}\n"+data), 0o600); err != nil {
				t.Fatal(err)
			}
			records, err = adapters.NewCodexAdapter().ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			added, updated, skipped, rejected, warnings = importParsedRecords(database, "codex", records)
			if added != 0 || updated != 0 || skipped != 2 || rejected != 0 || len(warnings) != 0 {
				t.Fatalf("rewritten import=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
			}
			var count, total int64
			if err := database.Conn().QueryRow(`SELECT COUNT(*), SUM(total_tokens) FROM usage_events`).Scan(&count, &total); err != nil {
				t.Fatal(err)
			}
			if count != 2 || total != 200 {
				t.Fatalf("usage changed after rewrite: events=%d total=%d", count, total)
			}
		})
	}
}

func TestClaudeNativeMessagesKeepUsageAndRefillTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeSource := func(split string) {
		t.Helper()
		var lines []string
		for _, id := range []string{"message-a", "message-b"} {
			lines = append(lines, fmt.Sprintf(`{"type":"assistant","timestamp":"2026-01-01T00:00:10Z","sessionId":"synthetic-session","requestId":"request-%s","message":{"id":%q,"model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":500,"cache_read_input_tokens":0%s}}}`, id, id, split))
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	writeSource("")
	records, err := adapters.NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	added, updated, skipped, rejected, warnings := importParsedRecords(database, "claude", records)
	if added != 2 || updated != 0 || skipped != 0 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("native messages import=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}
	writeSource(`,"cache_creation":{"ephemeral_5m_input_tokens":380,"ephemeral_1h_input_tokens":120}`)
	records, err = adapters.NewClaudeAdapter().ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	added, updated, skipped, rejected, warnings = importParsedRecords(database, "claude", records)
	if added != 0 || updated != 2 || skipped != 0 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("TTL refill import=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}
	added, updated, skipped, rejected, warnings = importParsedRecords(database, "claude", records)
	if added != 0 || updated != 0 || skipped != 2 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("repeat import=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}
	var count, total, oneHour int64
	if err := database.Conn().QueryRow(`SELECT COUNT(*), SUM(total_tokens), SUM(cache_creation_1h_tokens) FROM usage_events`).Scan(&count, &total, &oneHour); err != nil {
		t.Fatal(err)
	}
	if count != 2 || total != 1060 || oneHour != 240 {
		t.Fatalf("native message usage lost: events=%d total=%d 1h=%d", count, total, oneHour)
	}
}

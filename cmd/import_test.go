package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestImportReconcileCountsAndStableIdentity(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	base := &fingerprint.ParsedRecord{
		Agent: "codex", SourceProduct: "codex-cli", Provider: "unknown",
		Model: "unknown", ModelNormalized: "unknown", ModelResolution: model.ModelResolutionUnknown, ModelIsFallback: true,
		TimestampMs: 1_700_000_000_000, NativeSessionID: "native-session", SessionID: "native-session",
		NativeEventID: "native-event", IdentityKind: "event", IdentityScope: "session",
		ParserVersion: "codex-v1", Granularity: "request", InputTokens: 3, TotalTokens: 3,
	}
	added, updated, skipped, rejected, warnings := importParsedRecords(database, "codex", []*fingerprint.ParsedRecord{base})
	if added != 1 || updated != 0 || skipped != 0 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("first import counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}

	supplement := *base
	supplement.Provider = "openai"
	supplement.Model = "gpt-test"
	supplement.ModelNormalized = "gpt-test"
	supplement.ModelResolution = model.ModelResolutionDirectEvent
	supplement.ModelIsFallback = false
	added, updated, skipped, rejected, warnings = importParsedRecords(database, "codex", []*fingerprint.ParsedRecord{&supplement})
	if added != 0 || updated != 1 || skipped != 0 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("supplement counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}
	added, updated, skipped, rejected, warnings = importParsedRecords(database, "codex", []*fingerprint.ParsedRecord{&supplement})
	if added != 0 || updated != 0 || skipped != 1 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("repeat counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}

	conflict := supplement
	conflict.TotalTokens = 4
	added, updated, skipped, rejected, warnings = importParsedRecords(database, "codex", []*fingerprint.ParsedRecord{&conflict})
	if added != 0 || updated != 0 || skipped != 0 || rejected != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "token_conflict") {
		t.Fatalf("conflict counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}
	if strings.Contains(warnings[0], separatorForTest()) {
		t.Fatalf("warning leaked a source path: %q", warnings[0])
	}
}

func TestImportRejectsMissingSessionAndInvalidTimestamp(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	records := []*fingerprint.ParsedRecord{
		{Agent: "gemini", SourceProduct: "gemini-cli", TimestampMs: 1, NativeEventID: "e", IdentityKind: "event", ParserVersion: "gemini-v1", Granularity: "request"},
		{Agent: "gemini", SourceProduct: "gemini-cli", TimestampMs: 0, NativeSessionID: "s", NativeEventID: "e2", IdentityKind: "event", ParserVersion: "gemini-v1", Granularity: "request"},
	}
	added, updated, skipped, rejected, warnings := importParsedRecords(database, "gemini", records)
	if added != 0 || updated != 0 || skipped != 0 || rejected != 2 || len(warnings) != 2 {
		t.Fatalf("invalid identity counts=%d/%d/%d/%d warnings=%v", added, updated, skipped, rejected, warnings)
	}
	var count int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected records persisted count=%d err=%v", count, err)
	}
}

func TestTotalForAccountingProfileUsesOverlapRules(t *testing.T) {
	codex := &model.UsageEvent{InputTokens: 2, OutputTokens: 5, ReasoningTokens: 3, CacheReadTokens: 4, TokenAccountingMethod: model.AccCodexTotalDelta}
	if got := totalForAccountingProfile(codex); got != 11 {
		t.Fatalf("codex total=%d", got)
	}
	workbuddy := &model.UsageEvent{InputTokens: 2, OutputTokens: 5, ReasoningTokens: 3, CacheReadTokens: 4, CacheCreationTokens: 1, TokenAccountingMethod: model.AccWorkBuddyRawUsage}
	if got := totalForAccountingProfile(workbuddy); got != 12 {
		t.Fatalf("workbuddy total=%d", got)
	}
}

func TestImportWarningsAreRedactedBeforePersistence(t *testing.T) {
	privateFile := filepath.Join(t.TempDir(), "private-session.jsonl")
	warnings := []string{"failed to parse " + privateFile + ": malformed JSON"}
	sanitized := sanitizeImportWarnings(warnings, []string{privateFile})
	if len(sanitized) != 1 || strings.Contains(sanitized[0], privateFile) || !strings.Contains(sanitized[0], "[source]") {
		t.Fatalf("warning was not redacted: %v", sanitized)
	}
}

func separatorForTest() string {
	return string(filepath.Separator)
}

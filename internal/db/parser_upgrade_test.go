package db

import (
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func legacyClaudeFixture(t *testing.T) *model.UsageEvent {
	t.Helper()
	event := testEvent("legacy-claude", "unused", 10)
	event.Channel, event.SourceProduct = "claude", "claude-code"
	event.ParserVersion, event.IdentityStrategy = "claude-v1", "native_message"
	event.MessageID, event.RequestID = "reused", ""
	event.EventID = fingerprint.ClaudeLegacyMessageEventID(event.SessionKey, event.MessageID)
	event.ContentSHA256 = mustContentSHA256(t, event)
	return event
}

func TestClaudeParserUpgradeMergeBothDirections(t *testing.T) {
	for _, legacyDestination := range []bool{true, false} {
		destination, source := openTestDatabase(t), openTestDatabase(t)
		defer destination.Close()
		defer source.Close()
		legacy := legacyClaudeFixture(t)
		current := canonicalParserEvent(legacy)
		left, right := legacy, current
		if !legacyDestination {
			left, right = current, legacy
		}
		if err := insertEvent(destination.Conn(), left); err != nil {
			t.Fatal(err)
		}
		if err := insertEvent(source.Conn(), right); err != nil {
			t.Fatal(err)
		}
		if result, err := destination.MergeFrom(source.path); err != nil || result.Added != 0 {
			t.Fatalf("merge=%+v err=%v", result, err)
		}
		var count int
		var id string
		if err := destination.Conn().QueryRow(`SELECT COUNT(*), MAX(event_id) FROM usage_events`).Scan(&count, &id); err != nil {
			t.Fatal(err)
		}
		if count != 1 || id != current.EventID {
			t.Fatalf("count=%d id=%s", count, id)
		}
		if _, err := destination.MergeFrom(source.path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCopilotLegacyAccountingUpgradeIsNarrowAndIdempotent(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	raw := int64(100)
	legacy := testEvent("copilot-old", "unused", 135)
	legacy.Channel, legacy.SourceProduct = "copilot", "copilot-session-state"
	legacy.ParserVersion, legacy.TokenAccountingMethod = "copilot-session-state-v1", model.AccCopilotSessionMetrics
	legacy.AccountingProfile = "input_includes_cache_read"
	legacy.InputTokens, legacy.OutputTokens, legacy.CacheReadTokens, legacy.CacheCreationTokens, legacy.ReasoningTokens = 70, 20, 30, 10, 5
	legacy.RawInputTokens = &raw
	legacy.ContentSHA256 = mustContentSHA256(t, legacy)
	if err := insertEvent(database.Conn(), legacy); err != nil {
		t.Fatal(err)
	}
	current := canonicalParserEvent(legacy)
	if current.TotalTokens != 120 || current.InputTokens != 60 {
		t.Fatalf("upgrade=%+v", current)
	}
	if status, err := database.UpsertEvent(current); err != nil || status != ReconcileUpdated {
		t.Fatalf("status=%s err=%v", status, err)
	}
	if status, err := database.UpsertEvent(legacy); err != nil || status != ReconcileSkipped {
		t.Fatalf("older parser status=%s err=%v", status, err)
	}
	stored, err := selectEvent(database.Conn(), current.EventID)
	if err != nil || stored.TotalTokens != 120 || stored.ContentSHA256 != mustContentSHA256(t, stored) {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	invalid := *legacy
	invalid.ParserVersion = "third-party-parser"
	if canonicalParserEvent(&invalid).TotalTokens != 135 {
		t.Fatal("unrecognized parser was rewritten")
	}
}

func TestCodexTierCanEnrichButCannotConflict(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	base := testEvent("codex-tier", "unused", 10)
	base.Channel, base.SourceProduct = "codex", "codex-cli"
	base.AccountingProfile = "ledger"
	base.ContentSHA256 = mustContentSHA256(t, base)
	if _, err := database.UpsertEvent(base); err != nil {
		t.Fatal(err)
	}
	known := *base
	known.AccountingProfile = "ledger;service_tier=priority"
	known.ContentSHA256 = mustContentSHA256(t, &known)
	if status, err := database.UpsertEvent(&known); err != nil || status != ReconcileUpdated {
		t.Fatalf("enrich=%s err=%v", status, err)
	}
	if status, err := database.UpsertEvent(base); err != nil || status != ReconcileSkipped {
		t.Fatalf("weaker=%s err=%v", status, err)
	}
	known.AccountingProfile = "ledger;service_tier=default"
	known.ContentSHA256 = mustContentSHA256(t, &known)
	if status, err := database.UpsertEvent(&known); err == nil || status != ReconcileRejected {
		t.Fatalf("conflict=%s err=%v", status, err)
	}
}

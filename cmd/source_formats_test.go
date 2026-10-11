package cmd

import (
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
	"github.com/BlueSkyXN/AgentLedger/internal/pricing"
)

func TestGeminiChatJSONLRepeatedImport(t *testing.T) {
	line := `{"id":"m","type":"gemini","timestamp":"2026-01-01T00:00:01Z","model":"gemini-2.5-pro","tokens":{"input":100,"cached":30,"output":20,"total":120}}`
	path := writeAccountingFixture(t, t.TempDir(), ".gemini/tmp/p/chats/s.jsonl", `{"sessionId":"s","startTime":"2026-01-01T00:00:00Z"}`+"\n"+line+"\n"+line)
	database := accountingDatabase(t)
	importAccountingFixtures(t, database, adapters.NewGeminiAdapter(), path)
	assertAccountingTotals(t, database, 1, 120)
	result := importAccountingFixtures(t, database, adapters.NewGeminiAdapter(), path)
	if result.added != 0 || result.updated != 0 {
		t.Fatalf("repeat writes=%+v", result)
	}
}

func TestCodexCompactionAndServiceTierReachStoredPricing(t *testing.T) {
	lines := `{"type":"turn_context","payload":{"model":"gpt-6-astra"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"service_tier":"priority"}}}` + "\n" +
		`{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}}` + "\n" +
		`{"type":"token_usage_record","timestamp":"2026-01-01T00:00:01Z","payload":{"response_id":"compact","usage":{"input_tokens":300,"output_tokens":20,"total_tokens":320}}}` + "\n" +
		`{"type":"compacted","timestamp":"2026-01-01T00:00:02Z","payload":{"compaction_response_id":"compact"}}`
	path := writeAccountingFixture(t, t.TempDir(), "sessions/s.jsonl", lines)
	database := accountingDatabase(t)
	importAccountingFixtures(t, database, adapters.NewCodexAdapter(), path)
	assertAccountingTotals(t, database, 2, 440)
	result := importAccountingFixtures(t, database, adapters.NewCodexAdapter(), path)
	if result.added != 0 || result.updated != 0 {
		t.Fatalf("repeat=%+v", result)
	}
	var event pricing.Event
	if err := database.Conn().QueryRow(`SELECT channel, model_normalized, accounting_profile, input_tokens, output_tokens, total_tokens FROM usage_events ORDER BY timestamp_ms LIMIT 1`).Scan(&event.Channel, &event.Model, &event.AccountingProfile, &event.InputTokens, &event.OutputTokens, &event.TotalTokens); err != nil {
		t.Fatal(err)
	}
	if model.ServiceTierFromAccountingProfile(event.Channel, event.AccountingProfile) != "priority" {
		t.Fatalf("profile=%q", event.AccountingProfile)
	}
	profile, err := pricing.LoadDefaultProfile()
	if err != nil {
		t.Fatal(err)
	}
	estimator, err := pricing.NewEstimator(profile)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := estimator.Estimate(event); err != nil || got.RuleID != "gpt-6-astra-fast" {
		t.Fatalf("estimate=%+v err=%v", got, err)
	}
}

package pricing

import (
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestCodexPersistedTierReachesPricing(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatal(err)
	}
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Channel: "codex", Model: "gpt-6-astra", AccountingProfile: "ledger;service_tier=priority", InputTokens: 100, TotalTokens: 100}
	got, err := estimator.Estimate(event)
	if err != nil || got.RuleID != "gpt-6-astra-fast" {
		t.Fatalf("estimate=%+v err=%v", got, err)
	}
	event.AccountingProfile = "ledger;service_tier=default"
	standard, err := estimator.Estimate(event)
	if err != nil || standard.CostMicroUSD >= got.CostMicroUSD {
		t.Fatalf("standard=%+v fast=%+v err=%v", standard, got, err)
	}
}

func TestCopilotSessionReasoningIsNotPricedTwice(t *testing.T) {
	estimator, err := NewEstimator(testProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Provider: "openai", Channel: "codex", Model: "gpt-test", TokenAccountingMethod: model.AccCopilotSessionMetrics, AccountingProfile: model.CopilotSessionInclusiveProfile, OutputTokens: 20, ReasoningTokens: 5, TotalTokens: 20}
	got, err := estimator.Estimate(event)
	if err != nil || got.CostMicroUSD != 200 {
		t.Fatalf("estimate=%+v err=%v", got, err)
	}
}

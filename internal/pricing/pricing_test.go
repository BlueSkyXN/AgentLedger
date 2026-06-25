package pricing

import (
	"testing"
	"time"
)

func TestDefaultProfileLoads(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatalf("load default profile: %v", err)
	}
	if profile.ID == "" || len(profile.Rules) == 0 {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}

func TestDefaultProfilePricesUserSuppliedModels(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatalf("load default profile: %v", err)
	}
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatalf("estimator: %v", err)
	}

	tests := []struct {
		name         string
		event        Event
		wantRuleID   string
		wantMicroUSD int64
	}{
		{
			name: "claude fable uses claude cache multipliers",
			event: Event{
				Model:               "claude-fable-5",
				InputTokens:         1_000_000,
				OutputTokens:        1_000_000,
				CacheCreationTokens: 1_000_000,
				CacheReadTokens:     1_000_000,
			},
			wantRuleID:   "claude-fable-5",
			wantMicroUSD: 73_500_000,
		},
		{
			name: "kimi k2.5 uses cache hit and miss prices",
			event: Event{
				Model:           "kimi-k2.5",
				InputTokens:     1_000_000,
				OutputTokens:    1_000_000,
				CacheReadTokens: 1_000_000,
			},
			wantRuleID:   "kimi-k2.5",
			wantMicroUSD: 3_700_000,
		},
		{
			name: "doubao seed cny prices are converted at 6.8",
			event: Event{
				Model:           "doubao-seed-2.0-pro",
				InputTokens:     85,
				OutputTokens:    85,
				CacheReadTokens: 85,
			},
			wantRuleID:   "doubao-seed-2.0-pro",
			wantMicroUSD: 744,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimate, err := estimator.Estimate(tt.event)
			if err != nil {
				t.Fatalf("estimate: %v", err)
			}
			if !estimate.Priced || estimate.RuleID != tt.wantRuleID {
				t.Fatalf("expected priced rule %q, got %+v", tt.wantRuleID, estimate)
			}
			if estimate.CostMicroUSD != tt.wantMicroUSD {
				t.Fatalf("expected %d micro USD, got %d", tt.wantMicroUSD, estimate.CostMicroUSD)
			}
		})
	}
}

func TestEstimateUsesTokenBucketsNotTotalTokens(t *testing.T) {
	profile := testProfile(t)
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatalf("estimator: %v", err)
	}
	estimate, err := estimator.Estimate(Event{
		TimestampMs:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
		Provider:        "openai",
		Channel:         "codex",
		Model:           "gpt-test",
		InputTokens:     100,
		OutputTokens:    100,
		CacheReadTokens: 1000,
		TotalTokens:     999999,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if !estimate.Priced {
		t.Fatalf("expected priced estimate: %+v", estimate)
	}
	// 100*2 + 100*10 + 1000*0.5 = 1700 micro USD.
	if estimate.CostMicroUSD != 1700 {
		t.Fatalf("expected bucket-based cost 1700, got %d", estimate.CostMicroUSD)
	}
}

func TestRulePriorityAndLongContextCondition(t *testing.T) {
	profile := testProfile(t)
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatalf("estimator: %v", err)
	}
	standard, err := estimator.Estimate(Event{Provider: "openai", Channel: "codex", Model: "gpt-test", InputTokens: 999, OutputTokens: 1, ObservabilityLevel: "full"})
	if err != nil {
		t.Fatalf("standard estimate: %v", err)
	}
	long, err := estimator.Estimate(Event{Provider: "openai", Channel: "codex", Model: "gpt-test", InputTokens: 1000, OutputTokens: 1, ObservabilityLevel: "full"})
	if err != nil {
		t.Fatalf("long estimate: %v", err)
	}
	if standard.RuleID != "openai:gpt-test" {
		t.Fatalf("expected standard rule, got %+v", standard)
	}
	if long.RuleID != "openai:gpt-test-long" {
		t.Fatalf("expected long rule, got %+v", long)
	}
}

func TestUnknownModelIsMissing(t *testing.T) {
	profile := testProfile(t)
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatalf("estimator: %v", err)
	}
	estimate, err := estimator.Estimate(Event{Provider: "openai", Channel: "codex", Model: "new-model", InputTokens: 1, TotalTokens: 1})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if estimate.Priced || estimate.Confidence != "missing" || estimate.MissingReason == "" {
		t.Fatalf("expected missing estimate, got %+v", estimate)
	}
}

func testProfile(t *testing.T) *Profile {
	t.Helper()
	data := []byte(`{
	  "schema_version": 1,
	  "id": "test-profile",
	  "currency": "USD",
	  "unit": "usd_per_1m_tokens",
	  "defaults": {"reasoning_policy": "included_in_output", "cache_write_assumption": "treat_as_input", "confidence": "estimated"},
	  "rules": [
	    {
	      "id": "openai:gpt-test-long",
	      "provider": "openai",
	      "channel": "*",
	      "model_patterns": ["gpt-test"],
	      "priority": 100,
	      "basis": "api_equivalent",
	      "condition": {"min_input_side_tokens": 1000, "requires_observability": "full"},
	      "rates": {"input": 4, "cached_input": 1, "output": 20},
	      "confidence": "exact"
	    },
	    {
	      "id": "openai:gpt-test",
	      "provider": "openai",
	      "channel": "*",
	      "model_patterns": ["gpt-test"],
	      "priority": 10,
	      "basis": "api_equivalent",
	      "rates": {"input": 2, "cached_input": 0.5, "output": 10},
	      "confidence": "exact"
	    }
	  ]
	}`)
	profile, err := DecodeProfile(data)
	if err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	return profile
}

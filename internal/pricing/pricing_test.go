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

func TestDefaultProfileClassifiesUnknownAsPolicyZero(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatalf("load default profile: %v", err)
	}
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatalf("estimator: %v", err)
	}

	estimate, err := estimator.Estimate(Event{
		Model:               "unknown",
		InputTokens:         1_000_000,
		OutputTokens:        1_000_000,
		CacheCreationTokens: 1_000_000,
		CacheReadTokens:     1_000_000,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if estimate.Priced || estimate.RuleID != "unknown" || estimate.CostMicroUSD != 0 || estimate.Resolution != ResolutionPolicyZero || estimate.MissingReason != ResolutionMissingModel {
		t.Fatalf("expected explicit policy-zero unknown result, got %+v", estimate)
	}
}

func TestCoverageSeparatesPolicyZeroFromMissingPricingAndOfficialFree(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatalf("load default profile: %v", err)
	}
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatalf("estimator: %v", err)
	}

	events := []Event{
		{Provider: "openai", Channel: "codex", Model: "unknown", TotalTokens: 10},
		{Provider: "custom", Channel: "workbuddy", Model: "unpriced-model", TotalTokens: 20},
		{Provider: "openai", Channel: "codex", Model: "gpt-5.3-codex-spark", TotalTokens: 30},
	}
	var aggregate AggregateCost
	for _, event := range events {
		estimate, err := estimator.Estimate(event)
		if err != nil {
			t.Fatalf("estimate %q: %v", event.Model, err)
		}
		aggregate.Add(event, estimate)
	}
	summary := aggregate.Summary(profile)
	if summary == nil {
		t.Fatal("expected coverage summary")
	}
	if summary.TotalEvents != 3 || summary.TotalTokens != 60 || summary.PricedEvents != 1 || summary.PricedTokens != 30 {
		t.Fatalf("unexpected totals: %+v", summary)
	}
	if summary.PolicyZeroEvents != 1 || summary.PolicyZeroTokens != 10 || len(summary.PolicyZeroModels) != 1 || summary.PolicyZeroModels[0].Reason != ResolutionPolicyZero {
		t.Fatalf("unexpected policy-zero coverage: %+v", summary)
	}
	if len(summary.MissingModels) != 1 || summary.MissingModels[0].Reason != ResolutionMissingPricingRule {
		t.Fatalf("unexpected missing pricing coverage: %+v", summary)
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
		{
			name: "grok composer uses user supplied cached input price",
			event: Event{
				Model:           "grok-composer-2.5-fast",
				InputTokens:     1_000_000,
				OutputTokens:    1_000_000,
				CacheReadTokens: 1_000_000,
			},
			wantRuleID:   "grok-composer-2.5-fast",
			wantMicroUSD: 18_500_000,
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

func TestDefaultProfilePricesCurrentModelIDs(t *testing.T) {
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
			name: "gpt 5.6 sol short context",
			event: Event{
				Model:               "gpt-5.6-sol",
				InputTokens:         50_000,
				OutputTokens:        50_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     50_000,
			},
			wantRuleID:   "gpt-5.6-sol",
			wantMicroUSD: 2_087_500,
		},
		{
			name: "gpt 5.6 terra short context",
			event: Event{
				Model:               "gpt-5.6-terra",
				InputTokens:         50_000,
				OutputTokens:        50_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     50_000,
			},
			wantRuleID:   "gpt-5.6-terra",
			wantMicroUSD: 1_043_750,
		},
		{
			name: "gpt 5.6 luna short context",
			event: Event{
				Model:               "gpt-5.6-luna",
				InputTokens:         50_000,
				OutputTokens:        50_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     50_000,
			},
			wantRuleID:   "gpt-5.6-luna",
			wantMicroUSD: 417_500,
		},
		{
			name: "gpt 5.6 sol long context",
			event: Event{
				Model:               "gpt-5.6-sol",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.6-sol-long",
			wantMicroUSD: 6_500_000,
		},
		{
			name: "gpt 5.6 terra long context",
			event: Event{
				Model:               "gpt-5.6-terra",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.6-terra-long",
			wantMicroUSD: 3_250_000,
		},
		{
			name: "gpt 5.6 luna long context",
			event: Event{
				Model:               "gpt-5.6-luna",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.6-luna-long",
			wantMicroUSD: 1_300_000,
		},
		{
			name: "gpt 5.5 long context",
			event: Event{
				Model:               "gpt-5.5",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.5-long",
			wantMicroUSD: 6_320_000,
		},
		{
			name: "gpt 5.4 long context",
			event: Event{
				Model:               "gpt-5.4",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.4-long",
			wantMicroUSD: 3_160_000,
		},
		{
			name: "glm 5.2 keeps explicit free cache write",
			event: Event{
				Model:               "GLM-5.2",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "glm-5.2",
			wantMicroUSD: 606_000,
		},
		{
			name: "kimi k3 exact model",
			event: Event{
				Model:               "kimi-k3",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "kimi-k3",
			wantMicroUSD: 2_130_000,
		},
		{
			name: "kimi k2.7 code exact alias",
			event: Event{
				Model:               "kimi-for-coding",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "kimi-k2.7-code",
			wantMicroUSD: 609_000,
		},
		{
			name: "kimi k2.7 highspeed compatibility alias",
			event: Event{
				Model:               "kimi-for-coding-highspee",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "kimi-k2.7-code-highspeed",
			wantMicroUSD: 1_218_000,
		},
		{
			name: "grok 4.5 short context",
			event: Event{
				Model:               "grok-4.5",
				InputTokens:         50_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     50_000,
			},
			wantRuleID:   "grok-4.5",
			wantMicroUSD: 815_000,
		},
		{
			name: "grok 4.5 long context",
			event: Event{
				Model:               "grok-4.5",
				InputTokens:         50_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "grok-4.5-long",
			wantMicroUSD: 1_660_000,
		},
		{
			name: "grok 4.3 short context",
			event: Event{
				Model:               "grok-4.3",
				InputTokens:         50_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     50_000,
			},
			wantRuleID:   "grok-4.3",
			wantMicroUSD: 385_000,
		},
		{
			name: "grok 4.3 long context",
			event: Event{
				Model:               "grok-4.3",
				InputTokens:         50_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "grok-4.3-long",
			wantMicroUSD: 790_000,
		},
		{
			name: "claude sonnet 5 intro pricing",
			event: Event{
				TimestampMs:         time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC).UnixMilli(),
				Model:               "claude-sonnet-5",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-sonnet-5-intro",
			wantMicroUSD: 1_420_000,
		},
		{
			name: "claude sonnet 5 standard pricing",
			event: Event{
				TimestampMs:         time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
				Model:               "claude-sonnet-5",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-sonnet-5-standard",
			wantMicroUSD: 2_130_000,
		},
		{
			name: "codex spark is explicitly free",
			event: Event{
				Model:               "gpt-5.3-codex-spark",
				InputTokens:         1_000_000,
				OutputTokens:        1_000_000,
				CacheCreationTokens: 1_000_000,
				CacheReadTokens:     1_000_000,
			},
			wantRuleID:   "gpt-5.3-codex-spark",
			wantMicroUSD: 0,
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

func TestDefaultProfileUsesExactAliasesAndContextBoundaries(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatalf("load default profile: %v", err)
	}
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatalf("estimator: %v", err)
	}

	tests := []struct {
		name       string
		event      Event
		wantRuleID string
		wantPriced bool
	}{
		{name: "sol canonical alias", event: Event{Model: "gpt-5.6", InputTokens: 1}, wantRuleID: "gpt-5.6-sol", wantPriced: true},
		{name: "case and reasoning suffix", event: Event{Model: "GPT-5.6-SOL (reasoning=xhigh)", InputTokens: 1}, wantRuleID: "gpt-5.6-sol", wantPriced: true},
		{name: "gpt threshold minus one", event: Event{Model: "gpt-5.5", InputTokens: 271_999}, wantRuleID: "gpt-5.5", wantPriced: true},
		{name: "gpt threshold", event: Event{Model: "gpt-5.5", InputTokens: 272_000}, wantRuleID: "gpt-5.5-long", wantPriced: true},
		{name: "gpt threshold includes cache input", event: Event{Model: "gpt-5.5", InputTokens: 271_999, CacheReadTokens: 1}, wantRuleID: "gpt-5.5-long", wantPriced: true},
		{name: "grok threshold minus one", event: Event{Model: "grok-4.5", InputTokens: 199_999}, wantRuleID: "grok-4.5", wantPriced: true},
		{name: "grok threshold", event: Event{Model: "grok-4.5", InputTokens: 200_000}, wantRuleID: "grok-4.5-long", wantPriced: true},
		{name: "grok official alias", event: Event{Model: "grok-build-latest", InputTokens: 1}, wantRuleID: "grok-4.5", wantPriced: true},
		{name: "no fuzzy gpt match", event: Event{Model: "tenant/gpt-5.6-sol", InputTokens: 1}, wantPriced: false},
		{name: "user confirmed grok build alias", event: Event{Model: "grok-4.5-build", InputTokens: 1}, wantRuleID: "grok-4.5", wantPriced: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimate, err := estimator.Estimate(tt.event)
			if err != nil {
				t.Fatalf("estimate: %v", err)
			}
			if estimate.Priced != tt.wantPriced || estimate.RuleID != tt.wantRuleID {
				t.Fatalf("expected priced=%v rule=%q, got %+v", tt.wantPriced, tt.wantRuleID, estimate)
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

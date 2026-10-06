package pricing

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestDefaultProfileLoads(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatalf("load default profile: %v", err)
	}
	if profile.ID != "agentledger-pricing-2026-10-05" || profile.CheckedAt != "2026-10-05" || len(profile.Rules) == 0 {
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

func TestCoverageUsesOnlyPricedBucketsForPartialEvent(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatal(err)
	}
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		Provider:              "openai",
		Channel:               "codex",
		Model:                 "gpt-5.6-sol",
		ObservabilityLevel:    "partial",
		TokenAccountingMethod: model.AccCodexTotalDelta,
		InputTokens:           40,
		OutputTokens:          20,
		TotalTokens:           100,
	}
	estimate, err := estimator.Estimate(event)
	if err != nil {
		t.Fatal(err)
	}
	var aggregate AggregateCost
	aggregate.Add(event, estimate)
	summary := aggregate.Summary(profile)
	if summary == nil {
		t.Fatal("expected coverage summary")
	}
	if summary.PricedTokens != 60 || summary.TotalTokens != 100 || summary.TokenCoverageRatio != 0.6 {
		t.Fatalf("partial token coverage = %+v", summary)
	}
	if summary.Confidence != "partial" {
		t.Fatalf("partial confidence = %q", summary.Confidence)
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
			name: "gpt 6 astra short context",
			event: Event{
				Model: "gpt-6-astra", InputTokens: 50_000, OutputTokens: 50_000,
				CacheCreationTokens: 50_000, CacheReadTokens: 50_000,
			},
			wantRuleID: "gpt-6-astra", wantMicroUSD: 3_675_000,
		},
		{
			name: "gpt 6 astra long context",
			event: Event{
				Model: "gpt-6-astra", InputTokens: 100_001, OutputTokens: 100_000,
				CacheCreationTokens: 72_000, CacheReadTokens: 100_000,
			},
			wantRuleID: "gpt-6-astra-long", wantMicroUSD: 11_500_020,
		},
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
			wantMicroUSD: 1_470_000,
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
			wantMicroUSD: 835_000,
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
			wantMicroUSD: 83_500,
		},
		{
			name: "gpt 6 sol short context",
			event: Event{
				Model:               "gpt-6-sol",
				InputTokens:         50_000,
				OutputTokens:        50_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     50_000,
			},
			wantRuleID:   "gpt-6-sol",
			wantMicroUSD: 735_000,
		},
		{
			name: "gpt 6 sol long context",
			event: Event{
				Model:               "gpt-6-sol",
				InputTokens:         100_001,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-6-sol-long",
			wantMicroUSD: 2_300_004,
		},
		{
			name: "gpt 6 luna short context",
			event: Event{
				Model:               "gpt-6-luna",
				InputTokens:         50_000,
				OutputTokens:        50_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     50_000,
			},
			wantRuleID:   "gpt-6-luna",
			wantMicroUSD: 36_750,
		},
		{
			name: "gpt 6 luna long context",
			event: Event{
				Model:               "gpt-6-luna",
				InputTokens:         100_001,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-6-luna-long",
			wantMicroUSD: 115_000,
		},
		{
			name: "gpt 5.6 sol long context",
			event: Event{
				Model:               "gpt-5.6-sol",
				InputTokens:         100_001,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.6-sol-long",
			wantMicroUSD: 4_600_008,
		},
		{
			name: "gpt 5.6 terra long context",
			event: Event{
				Model:               "gpt-5.6-terra",
				InputTokens:         100_001,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.6-terra-long",
			wantMicroUSD: 2_600_004,
		},
		{
			name: "gpt 5.6 luna long context",
			event: Event{
				Model:               "gpt-5.6-luna",
				InputTokens:         100_001,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.6-luna-long",
			wantMicroUSD: 260_000,
		},
		{
			name: "gpt 5.5 long context",
			event: Event{
				Model:               "gpt-5.5",
				InputTokens:         100_001,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.5-long",
			wantMicroUSD: 6_320_010,
		},
		{
			name: "gpt 5.4 long context",
			event: Event{
				Model:               "gpt-5.4",
				InputTokens:         100_001,
				OutputTokens:        100_000,
				CacheCreationTokens: 72_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "gpt-5.4-long",
			wantMicroUSD: 3_160_005,
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
			name: "glm 5.3 original list price",
			event: Event{
				Model:               "glm-5.3",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "glm-5.3",
			wantMicroUSD: 746_000,
		},
		{
			name: "ox alpha uses glm 5.3 flash original list price",
			event: Event{
				Model:               "ox-alpha",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "glm-5.3-flash",
			wantMicroUSD: 83_000,
		},
		{
			name: "deepseek v4 pro dated alias uses peak price",
			event: Event{
				Model:               "deepseek-v4-pro-ga-260813",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "deepseek-v4-pro",
			wantMicroUSD: 664_400,
		},
		{
			name: "hy3 uses tencent cloud provider price",
			event: Event{
				Model:               "hy3",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "hy3",
			wantMicroUSD: 82_500,
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
			name: "grok 4.6 short context",
			event: Event{
				Model:               "grok-4.6",
				InputTokens:         50_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     50_000,
			},
			wantRuleID:   "grok-4.6",
			wantMicroUSD: 825_000,
		},
		{
			name: "grok 4.6 long context",
			event: Event{
				Model:               "grok-4.6",
				InputTokens:         50_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "grok-4.6-long",
			wantMicroUSD: 1_700_000,
		},
		{
			name: "grok 4.6 build alias long context",
			event: Event{
				Model:               "grok-4.6-build",
				InputTokens:         50_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 50_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "grok-4.6-long",
			wantMicroUSD: 1_700_000,
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
			name: "claude opus 5 official pricing",
			event: Event{
				Model:               "claude-opus-5",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-opus-5",
			wantMicroUSD: 3_675_000,
		},
		{
			name: "claude sonnet 5 launch pricing",
			event: Event{
				TimestampMs:         time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC).UnixMilli(),
				Model:               "claude-sonnet-5",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-sonnet-5",
			wantMicroUSD: 1_470_000,
		},
		{
			name: "claude sonnet 5 keeps launch pricing after cancelled increase",
			event: Event{
				TimestampMs:         time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
				Model:               "claude-sonnet-5",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-sonnet-5",
			wantMicroUSD: 1_470_000,
		},
		{
			name: "claude sonnet 5.5 official pricing",
			event: Event{
				Model:               "claude-sonnet-5-5",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-sonnet-5-5",
			wantMicroUSD: 1_470_000,
		},
		{
			name: "claude opus 5.5 official pricing",
			event: Event{
				Model:               "claude-opus-5-5",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-opus-5-5",
			wantMicroUSD: 2_920_000,
		},
		{
			name: "claude fable 5.1 discounted cache reads",
			event: Event{
				Model:               "claude-fable-5-1",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-fable-5-1",
			wantMicroUSD: 7_275_000,
		},
		{
			name: "claude mythos 5.1 shares fable 5.1 pricing",
			event: Event{
				Model:           "claude-mythos-5-1",
				CacheReadTokens: 1_000_000,
			},
			wantRuleID:   "claude-fable-5-1",
			wantMicroUSD: 250_000,
		},
		{
			name: "claude opus 4.1 legacy pricing",
			event: Event{
				Model:               "claude-opus-4-1",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-opus-4-legacy",
			wantMicroUSD: 11_025_000,
		},
		{
			name: "claude opus 4 dated id uses legacy pricing",
			event: Event{
				Model:       "claude-opus-4-20250514",
				InputTokens: 1_000_000,
			},
			wantRuleID:   "claude-opus-4-legacy",
			wantMicroUSD: 15_000_000,
		},
		{
			name: "claude opus 4.8 standard pricing",
			event: Event{
				Model:               "claude-opus-4-8",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-opus-4",
			wantMicroUSD: 3_675_000,
		},
		{
			name: "claude opus 4.8 fast mode",
			event: Event{
				Model:               "claude-opus-4-8-fast",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-opus-4.8-fast",
			wantMicroUSD: 7_350_000,
		},
		{
			name: "claude opus 5.5 fast mode",
			event: Event{
				Model:               "claude-opus-5-5-fast",
				InputTokens:         100_000,
				OutputTokens:        100_000,
				CacheCreationTokens: 100_000,
				CacheReadTokens:     100_000,
			},
			wantRuleID:   "claude-opus-5-5-fast",
			wantMicroUSD: 5_840_000,
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
		{name: "astra at 272k stays short", event: Event{Model: "gpt-6-astra", InputTokens: 272_000}, wantRuleID: "gpt-6-astra", wantPriced: true},
		{name: "astra above 272k is long", event: Event{Model: "gpt-6-astra", InputTokens: 272_001}, wantRuleID: "gpt-6-astra-long", wantPriced: true},
		{name: "astra threshold includes cache", event: Event{Model: "gpt-6-astra", InputTokens: 272_000, CacheReadTokens: 1}, wantRuleID: "gpt-6-astra-long", wantPriced: true},
		{name: "astra context suffix", event: Event{Model: "gpt-6-astra[1m]", InputTokens: 1}, wantRuleID: "gpt-6-astra", wantPriced: true},
		{name: "no fuzzy astra match", event: Event{Model: "gpt-6-astra-preview", InputTokens: 1}, wantPriced: false},
		{name: "case and reasoning suffix", event: Event{Model: "GPT-5.6-SOL (reasoning=xhigh)", InputTokens: 1}, wantRuleID: "gpt-5.6-sol", wantPriced: true},
		{name: "sol max suffix", event: Event{Model: "gpt-5.6-sol(max)", InputTokens: 1}, wantRuleID: "gpt-5.6-sol", wantPriced: true},
		{name: "any parenthetical suffix", event: Event{Model: "gpt-5.6-sol(custom-effort)", InputTokens: 1}, wantRuleID: "gpt-5.6-sol", wantPriced: true},
		{name: "one million context suffix", event: Event{Model: "gpt-5.6-sol[1m]", InputTokens: 1}, wantRuleID: "gpt-5.6-sol", wantPriced: true},
		{name: "gpt threshold minus one", event: Event{Model: "gpt-5.5", InputTokens: 271_999}, wantRuleID: "gpt-5.5", wantPriced: true},
		{name: "gpt threshold stays short", event: Event{Model: "gpt-5.5", InputTokens: 272_000}, wantRuleID: "gpt-5.5", wantPriced: true},
		{name: "gpt threshold above is long", event: Event{Model: "gpt-5.5", InputTokens: 272_001}, wantRuleID: "gpt-5.5-long", wantPriced: true},
		{name: "gpt threshold includes cache input", event: Event{Model: "gpt-5.5", InputTokens: 272_000, CacheReadTokens: 1}, wantRuleID: "gpt-5.5-long", wantPriced: true},
		{name: "sol 6 at 272k stays short", event: Event{Model: "gpt-6-sol", InputTokens: 272_000}, wantRuleID: "gpt-6-sol", wantPriced: true},
		{name: "sol 6 above 272k is long", event: Event{Model: "gpt-6-sol", InputTokens: 272_001}, wantRuleID: "gpt-6-sol-long", wantPriced: true},
		{name: "luna 6 above 272k is long", event: Event{Model: "gpt-6-luna", InputTokens: 272_001}, wantRuleID: "gpt-6-luna-long", wantPriced: true},
		{name: "grok 4.6 threshold minus one", event: Event{Model: "grok-4.6", InputTokens: 199_999}, wantRuleID: "grok-4.6", wantPriced: true},
		{name: "grok 4.6 threshold", event: Event{Model: "grok-4.6", InputTokens: 200_000}, wantRuleID: "grok-4.6-long", wantPriced: true},
		{name: "grok 4.6 build threshold minus one", event: Event{Model: "grok-4.6-build", InputTokens: 199_999}, wantRuleID: "grok-4.6", wantPriced: true},
		{name: "grok 4.6 build threshold", event: Event{Model: "grok-4.6-build", InputTokens: 200_000}, wantRuleID: "grok-4.6-long", wantPriced: true},
		{name: "grok threshold minus one", event: Event{Model: "grok-4.5", InputTokens: 199_999}, wantRuleID: "grok-4.5", wantPriced: true},
		{name: "grok threshold", event: Event{Model: "grok-4.5", InputTokens: 200_000}, wantRuleID: "grok-4.5-long", wantPriced: true},
		{name: "grok official alias", event: Event{Model: "grok-build-latest", InputTokens: 1}, wantRuleID: "grok-4.5", wantPriced: true},
		{name: "kimi k3 256 alias", event: Event{Model: "k3-256", InputTokens: 1}, wantRuleID: "kimi-k3", wantPriced: true},
		{name: "kimi k3 256k alias", event: Event{Model: "k3-256k", InputTokens: 1}, wantRuleID: "kimi-k3", wantPriced: true},
		{name: "hy3 openrouter canonical alias", event: Event{Model: "tencent/hy3", InputTokens: 1}, wantRuleID: "hy3", wantPriced: true},
		{name: "claude opus 5 openrouter canonical alias", event: Event{Model: "anthropic/claude-opus-5", InputTokens: 1}, wantRuleID: "claude-opus-5", wantPriced: true},
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

func TestDefaultProfilePricesFastServiceTier(t *testing.T) {
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
			name: "astra fast short context",
			event: Event{
				Model: "gpt-6-astra", ServiceTier: "fast",
				InputTokens: 50_000, OutputTokens: 50_000,
				CacheCreationTokens: 50_000, CacheReadTokens: 50_000,
			},
			wantRuleID: "gpt-6-astra-fast", wantMicroUSD: 7_350_000,
		},
		{
			name: "astra priority tier long context",
			event: Event{
				Model: "gpt-6-astra", ServiceTier: "priority",
				InputTokens: 100_001, OutputTokens: 100_000,
				CacheCreationTokens: 72_000, CacheReadTokens: 100_000,
			},
			wantRuleID: "gpt-6-astra-fast-long", wantMicroUSD: 23_000_040,
		},
		{
			name: "luna 5.6 fast short context",
			event: Event{
				Model: "gpt-5.6-luna", ServiceTier: "fast",
				InputTokens: 50_000, OutputTokens: 50_000,
				CacheCreationTokens: 50_000, CacheReadTokens: 50_000,
			},
			wantRuleID: "gpt-5.6-luna-fast", wantMicroUSD: 167_000,
		},
		{
			name: "sol canonical alias fast tier",
			event: Event{
				Model: "gpt-5.6", ServiceTier: "fast",
				InputTokens: 1, TotalTokens: 1,
			},
			wantRuleID: "gpt-5.6-sol-fast", wantMicroUSD: 8,
		},
		{
			name: "model without fast rules keeps standard rate",
			event: Event{
				Model: "glm-5.2", ServiceTier: "fast",
				InputTokens: 100_000, OutputTokens: 100_000,
				CacheCreationTokens: 100_000, CacheReadTokens: 100_000,
			},
			wantRuleID: "glm-5.2", wantMicroUSD: 606_000,
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

func TestOutputPricingFollowsAccountingMethod(t *testing.T) {
	profile := testProfile(t)
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		method    string
		output    int64
		reasoning int64
		total     int64
		wantCost  int64
	}{
		{name: "Codex reasoning below output", method: model.AccCodexTotalDelta, output: 5, reasoning: 3, total: 5, wantCost: 50},
		{name: "Codex reasoning equals output", method: model.AccCodexTotalDelta, output: 3, reasoning: 3, total: 3, wantCost: 30},
		{name: "Codex reasoning above output", method: model.AccCodexTotalDelta, output: 2, reasoning: 3, total: 3, wantCost: 30},
		{name: "Copilot reasoning is separate", method: model.AccCopilotOtelParts, output: 2, reasoning: 3, total: 5, wantCost: 50},
		{name: "WorkBuddy reasoning is included", method: model.AccWorkBuddyRawUsage, output: 5, reasoning: 3, total: 5, wantCost: 50},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := Event{
				Provider: "openai", Channel: "codex", Model: "gpt-test",
				OutputTokens: test.output, ReasoningTokens: test.reasoning,
				TotalTokens: test.total, TokenAccountingMethod: test.method,
			}
			estimate, err := estimator.Estimate(event)
			if err != nil {
				t.Fatal(err)
			}
			if estimate.CostMicroUSD != test.wantCost || estimate.PricedTokens != test.total {
				t.Fatalf("estimate=%+v want cost=%d priced tokens=%d", estimate, test.wantCost, test.total)
			}
			var aggregate AggregateCost
			aggregate.Add(event, estimate)
			summary := aggregate.Summary(profile)
			if summary == nil || summary.TokenCoverageRatio != 1 {
				t.Fatalf("coverage=%+v want token ratio 1", summary)
			}
		})
	}
}

func TestMatchedRuleWithoutUsedBucketRateIsUnpriced(t *testing.T) {
	profile, err := DecodeProfile([]byte(`{
	  "schema_version": 1,
	  "id": "input-only",
	  "currency": "USD",
	  "unit": "usd_per_1m_tokens",
	  "rules": [{"id":"input-only","model_patterns":["gpt-test"],"rates":{"input":2}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Model: "gpt-test", OutputTokens: 5, TotalTokens: 5}
	estimate, err := estimator.Estimate(event)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Priced || estimate.PricedTokens != 0 || estimate.MissingReason != ResolutionMissingPricingRate {
		t.Fatalf("used bucket without a rate must be unpriced: %+v", estimate)
	}
	var aggregate AggregateCost
	aggregate.Add(event, estimate)
	summary := aggregate.Summary(profile)
	if summary == nil || summary.PricedEvents != 0 || len(summary.MissingModels) != 1 || summary.MissingModels[0].Reason != ResolutionMissingPricingRate {
		t.Fatalf("unexpected coverage: %+v", summary)
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

func TestRuleMatchesProviderAndChannel(t *testing.T) {
	profile, err := DecodeProfile([]byte(`{
	  "schema_version": 1,
	  "id": "routing",
	  "currency": "USD",
	  "unit": "usd_per_1m_tokens",
	  "rules": [{
	    "id": "openai-codex",
	    "provider": "openai",
	    "channel": "codex",
	    "model_patterns": ["same-model"],
	    "rates": {"input": 1}
	  }]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	estimator, err := NewEstimator(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		event  Event
		priced bool
	}{
		{name: "match", event: Event{Provider: "openai", Channel: "codex", Model: "same-model", InputTokens: 1}, priced: true},
		{name: "wrong provider", event: Event{Provider: "anthropic", Channel: "codex", Model: "same-model", InputTokens: 1}, priced: false},
		{name: "wrong channel", event: Event{Provider: "openai", Channel: "claude", Model: "same-model", InputTokens: 1}, priced: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			estimate, err := estimator.Estimate(test.event)
			if err != nil {
				t.Fatal(err)
			}
			if estimate.Priced != test.priced {
				t.Fatalf("priced=%v want=%v estimate=%+v", estimate.Priced, test.priced, estimate)
			}
		})
	}
}

func TestProfileRejectsInvalidDatesNegativeAndUnsupportedRates(t *testing.T) {
	base := `{
	  "schema_version": 1,
	  "id": "invalid",
	  "currency": "USD",
	  "unit": "usd_per_1m_tokens",
	  "rules": [{"id":"rule","model_patterns":["m"],%s}]
	}`
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "invalid effective date", body: `"effective_from":"2026-02-30","rates":{"input":1}`},
		{name: "reversed date window", body: `"effective_from":"2026-03-02","effective_to":"2026-03-01","rates":{"input":1}`},
		{name: "negative rate", body: `"rates":{"input":-1}`},
		{name: "request rate", body: `"rates":{"request":1}`},
		{name: "reasoning rate", body: `"rates":{"reasoning":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeProfile([]byte(fmt.Sprintf(base, test.body))); err == nil {
				t.Fatal("expected invalid profile error")
			}
		})
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

func TestDefaultProfileClaudeRulesPriceBothCacheWriteTTLs(t *testing.T) {
	profile, err := LoadDefaultProfile()
	if err != nil {
		t.Fatalf("load default profile: %v", err)
	}
	checked := 0
	for _, rule := range profile.Rules {
		isClaude := false
		for _, pattern := range rule.ModelPatterns {
			if strings.Contains(strings.ToLower(pattern), "claude-") {
				isClaude = true
				break
			}
		}
		if !isClaude {
			continue
		}
		checked++
		if rule.Rates.CacheWrite5m == nil || rule.Rates.CacheWrite1h == nil {
			t.Errorf("claude rule %q must define both cache_write_5m and cache_write_1h", rule.ID)
		}
		if rule.Rates.CacheRead == nil {
			t.Errorf("claude rule %q must define cache_read", rule.ID)
		}
	}
	if checked == 0 {
		t.Fatal("expected claude rules in the default profile")
	}
}

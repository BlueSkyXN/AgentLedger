package pricing

import (
	"fmt"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

type Estimate struct {
	CostMicroUSD  int64
	PricedTokens  int64
	RuleID        string
	Basis         string
	Confidence    string
	Resolution    string
	Priced        bool
	MissingReason string
}

func (e *Estimator) Estimate(ev Event) (Estimate, error) {
	match := e.Resolve(ev)
	return e.EstimateMatch(ev, match)
}

func (e *Estimator) EstimateMatch(ev Event, match Match) (Estimate, error) {
	if match.Rule == nil {
		return Estimate{Confidence: "missing", Resolution: match.Resolution, MissingReason: match.MissingReason}, nil
	}
	cost, pricedTokens, approximated, err := estimateWithRule(ev, match.Rule, e.profile)
	if err != nil {
		return Estimate{}, err
	}
	estimate := Estimate{
		CostMicroUSD: cost,
		PricedTokens: pricedTokens,
		RuleID:       match.RuleID,
		Basis:        match.Basis,
		Confidence:   match.Confidence,
		Priced:       pricedTokens > 0 || eventTokens(ev) == 0,
		Resolution:   match.Resolution,
	}
	if !estimate.Priced {
		estimate.Resolution = ResolutionMissingPricingRate
		estimate.MissingReason = ResolutionMissingPricingRate
	}
	if pricedTokens < eventTokens(ev) || approximated {
		estimate.Confidence = combineConfidence(estimate.Confidence, "partial")
	}
	if match.Resolution == ResolutionPolicyZero {
		estimate.Priced = false
		estimate.MissingReason = ResolutionMissingModel
	}
	return estimate, nil
}

type pricedPart struct {
	name   string
	tokens int64
	rate   *Rate
}

// estimateWithRule returns the cost, the number of tokens covered by a rate,
// and whether any bucket was priced with a fallback rate (a lower bound).
func estimateWithRule(ev Event, rule *Rule, profile *Profile) (int64, int64, bool, error) {
	var total int64
	var pricedTokens int64
	pricedOutputTokens := outputTokensForPricing(ev, rule, profile)
	cacheParts, approximated := cacheCreationParts(ev, rule, profile)
	parts := append([]pricedPart{
		{"input", ev.InputTokens, rule.Rates.Input},
		{"output", pricedOutputTokens, rule.Rates.Output},
		{"cache_read", ev.CacheReadTokens, cacheReadRate(rule)},
	}, cacheParts...)
	for _, part := range parts {
		value, err := part.rate.MicroUSD(part.tokens)
		if err != nil {
			return 0, 0, false, fmt.Errorf("%s cost: %w", part.name, err)
		}
		total += value
		if part.rate != nil && part.rate.raw != "" {
			pricedTokens += part.tokens
		}
	}
	if eventTotal := eventTokens(ev); pricedTokens > eventTotal {
		pricedTokens = eventTotal
	} else if pricedTokens == 0 && eventTotal > 0 && allTokenRatesExplicitZero(rule, profile) {
		pricedTokens = eventTotal
	}
	return total, pricedTokens, approximated, nil
}

// cacheCreationParts prices cache writes by TTL when the event carries an
// explicit 1-hour split: the 1-hour share uses cache_write_1h and the rest
// uses cache_write_5m. A rule-wide cache_creation/cache_write rate applies to
// both shares. Without a split, cache_write_assumption picks a single rate.
// A known 1-hour share on a rule without cache_write_1h falls back to the
// single-rate choice and is reported as approximated.
func cacheCreationParts(ev Event, rule *Rule, profile *Profile) ([]pricedPart, bool) {
	single := cacheCreationRate(rule, profile)
	if ev.CacheCreation1hTokens == nil || rule.Rates.CacheCreation != nil || rule.Rates.CacheWrite != nil {
		return []pricedPart{{"cache_creation", ev.CacheCreationTokens, single}}, false
	}
	oneHour := *ev.CacheCreation1hTokens
	if oneHour < 0 {
		oneHour = 0
	}
	if oneHour > ev.CacheCreationTokens {
		oneHour = ev.CacheCreationTokens
	}
	fiveMinuteRate := rule.Rates.CacheWrite5m
	if fiveMinuteRate == nil {
		fiveMinuteRate = single
	}
	oneHourRate := rule.Rates.CacheWrite1h
	approximated := false
	if oneHourRate == nil {
		oneHourRate = single
		approximated = oneHour > 0
	}
	return []pricedPart{
		{"cache_creation_5m", ev.CacheCreationTokens - oneHour, fiveMinuteRate},
		{"cache_creation_1h", oneHour, oneHourRate},
	}, approximated
}

func outputTokensForPricing(ev Event, rule *Rule, profile *Profile) int64 {
	switch ev.TokenAccountingMethod {
	case model.AccCodexLastTokenUsage, model.AccCodexTotalDelta, model.AccCodexHeadlessUsage:
		if ev.ReasoningTokens > ev.OutputTokens {
			return ev.ReasoningTokens
		}
		return ev.OutputTokens
	case model.AccCopilotOtelParts, model.AccCopilotOtelTotalFallback, model.AccCopilotSessionMetrics:
		// Copilot reports reasoning as a separate bucket rather than including it
		// in output_tokens; model pricing charges it at the output rate.
		return ev.OutputTokens + ev.ReasoningTokens
	case model.AccWorkBuddyRawUsage:
		// WorkBuddy completion_tokens already includes reasoning_tokens.
		return ev.OutputTokens
	default:
		switch reasoningPolicy(rule, profile) {
		case "separate_as_output", "priced_as_output":
			return ev.OutputTokens + ev.ReasoningTokens
		}
		return ev.OutputTokens
	}
}

func allTokenRatesExplicitZero(rule *Rule, profile *Profile) bool {
	for _, rate := range []*Rate{rule.Rates.Input, rule.Rates.Output, cacheReadRate(rule), cacheCreationRate(rule, profile)} {
		if rate == nil || rate.raw == "" || rate.Float64() != 0 {
			return false
		}
	}
	return true
}

func cacheReadRate(rule *Rule) *Rate {
	if rule.Rates.CacheRead != nil {
		return rule.Rates.CacheRead
	}
	return rule.Rates.CachedInput
}

func cacheCreationRate(rule *Rule, profile *Profile) *Rate {
	if rule.Rates.CacheCreation != nil {
		return rule.Rates.CacheCreation
	}
	if rule.Rates.CacheWrite != nil {
		return rule.Rates.CacheWrite
	}
	assumption := strings.ToLower(firstNonEmpty(rule.CacheWriteAssumption, profile.Defaults.CacheWriteAssumption))
	switch assumption {
	case "5m_if_unknown", "assume_5m":
		if rule.Rates.CacheWrite5m != nil {
			return rule.Rates.CacheWrite5m
		}
	case "1h_if_unknown", "assume_1h":
		if rule.Rates.CacheWrite1h != nil {
			return rule.Rates.CacheWrite1h
		}
	}
	if rule.Rates.CacheWrite5m != nil {
		return rule.Rates.CacheWrite5m
	}
	if assumption == "treat_as_input" {
		return rule.Rates.Input
	}
	return rule.Rates.Input
}

func reasoningPolicy(rule *Rule, profile *Profile) string {
	return strings.ToLower(firstNonEmpty(rule.ReasoningPolicy, profile.Defaults.ReasoningPolicy, "included_in_output"))
}

func MicroUSDToUSD(value int64) float64 {
	return float64(value) / 1_000_000
}

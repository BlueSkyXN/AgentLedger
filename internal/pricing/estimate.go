package pricing

import (
	"fmt"
	"strings"
)

type Estimate struct {
	CostMicroUSD  int64
	RuleID        string
	Basis         string
	Confidence    string
	Priced        bool
	MissingReason string
}

func (e *Estimator) Estimate(ev Event) (Estimate, error) {
	match := e.Resolve(ev)
	return e.EstimateMatch(ev, match)
}

func (e *Estimator) EstimateMatch(ev Event, match Match) (Estimate, error) {
	if match.Rule == nil {
		return Estimate{Confidence: "missing", MissingReason: match.MissingReason}, nil
	}
	cost, err := estimateWithRule(ev, match.Rule, e.profile)
	if err != nil {
		return Estimate{}, err
	}
	return Estimate{
		CostMicroUSD: cost,
		RuleID:       match.RuleID,
		Basis:        match.Basis,
		Confidence:   match.Confidence,
		Priced:       true,
	}, nil
}

func estimateWithRule(ev Event, rule *Rule, profile *Profile) (int64, error) {
	var total int64
	parts := []struct {
		name   string
		tokens int64
		rate   *Rate
	}{
		{"input", ev.InputTokens, rule.Rates.Input},
		{"output", ev.OutputTokens, rule.Rates.Output},
		{"cache_read", ev.CacheReadTokens, cacheReadRate(rule)},
		{"cache_creation", ev.CacheCreationTokens, cacheCreationRate(rule, profile)},
	}
	for _, part := range parts {
		value, err := part.rate.MicroUSD(part.tokens)
		if err != nil {
			return 0, fmt.Errorf("%s cost: %w", part.name, err)
		}
		total += value
	}
	if reasoningPolicy(rule, profile) == "separate" {
		value, err := rule.Rates.Reasoning.MicroUSD(ev.ReasoningTokens)
		if err != nil {
			return 0, fmt.Errorf("reasoning cost: %w", err)
		}
		total += value
	}
	return total, nil
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

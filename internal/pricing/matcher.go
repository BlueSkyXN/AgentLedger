package pricing

import (
	"path"
	"strings"
	"time"
)

type Event struct {
	TimestampMs           int64
	Channel               string
	Provider              string
	Model                 string
	SourceProduct         string
	ObservabilityLevel    string
	TokenAccountingMethod string
	AccountingProfile     string
	InputTokens           int64
	OutputTokens          int64
	CacheCreationTokens   int64
	CacheReadTokens       int64
	ReasoningTokens       int64
	TotalTokens           int64
}

type Match struct {
	Rule          *Rule
	RuleID        string
	Basis         string
	Confidence    string
	MissingReason string
}

type Estimator struct {
	profile *Profile
}

func NewEstimator(profile *Profile) (*Estimator, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	return &Estimator{profile: profile}, nil
}

func (e *Estimator) Profile() *Profile {
	return e.profile
}

func (e *Estimator) Resolve(ev Event) Match {
	model := strings.ToLower(strings.TrimSpace(ev.Model))
	if model == "" || model == "unknown" {
		return Match{Confidence: "missing", MissingReason: "missing_model"}
	}
	modelAliases := pricingModelAliases(model)
	for i := range e.profile.Rules {
		rule := &e.profile.Rules[i]
		if !matchesAnyPattern(rule.ModelPatterns, modelAliases...) {
			continue
		}
		if !matchesEffectiveWindow(rule, ev.TimestampMs) {
			continue
		}
		if !matchesCondition(rule.Condition, ev) {
			continue
		}
		confidence := firstNonEmpty(rule.Confidence, e.profile.Defaults.Confidence, "estimated")
		return Match{Rule: rule, RuleID: rule.ID, Basis: firstNonEmpty(rule.Basis, "api_equivalent"), Confidence: confidence}
	}
	return Match{Confidence: "missing", MissingReason: "missing_pricing_rule"}
}

func matchesAnyPattern(patterns []string, values ...string) bool {
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		for _, value := range values {
			if pattern == value {
				return true
			}
			if ok, err := path.Match(pattern, value); err == nil && ok {
				return true
			}
		}
	}
	return false
}

func pricingModelAliases(model string) []string {
	model = strings.ToLower(strings.TrimSpace(model))
	aliases := []string{model}
	if base, ok := stripReasoningSuffix(model); ok && base != model {
		aliases = append(aliases, base)
	}
	return aliases
}

func stripReasoningSuffix(model string) (string, bool) {
	if !strings.HasSuffix(model, ")") {
		return model, false
	}
	start := strings.LastIndex(model, "(")
	if start <= 0 {
		return model, false
	}
	suffix := strings.TrimSpace(model[start+1 : len(model)-1])
	if !isReasoningSuffix(suffix) {
		return model, false
	}
	return strings.TrimSpace(model[:start]), true
}

func isReasoningSuffix(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "reasoning=")
	value = strings.TrimPrefix(value, "reasoning:")
	value = strings.TrimPrefix(value, "effort=")
	value = strings.TrimPrefix(value, "effort:")
	switch value {
	case "minimal", "low", "medium", "high", "xhigh", "x-high":
		return true
	default:
		return false
	}
}

func matchesEffectiveWindow(rule *Rule, timestampMs int64) bool {
	if rule.EffectiveFrom == "" && rule.EffectiveTo == "" {
		return true
	}
	ts := time.UnixMilli(timestampMs).UTC()
	if rule.EffectiveFrom != "" {
		from, err := time.Parse("2006-01-02", rule.EffectiveFrom)
		if err == nil && ts.Before(from) {
			return false
		}
	}
	if rule.EffectiveTo != "" {
		to, err := time.Parse("2006-01-02", rule.EffectiveTo)
		if err == nil && !ts.Before(to.AddDate(0, 0, 1)) {
			return false
		}
	}
	return true
}

func matchesCondition(condition Condition, ev Event) bool {
	if condition.RequiresObservability != "" && !strings.EqualFold(condition.RequiresObservability, ev.ObservabilityLevel) {
		return false
	}
	if condition.MinInputSideTokens != nil {
		inputSide := ev.InputTokens + ev.CacheCreationTokens + ev.CacheReadTokens
		if inputSide < *condition.MinInputSideTokens {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

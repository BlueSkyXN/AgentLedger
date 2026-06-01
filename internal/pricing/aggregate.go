package pricing

import (
	"fmt"
	"sort"
)

type AggregateCost struct {
	EstimatedCostMicroUSD int64
	Coverage              Coverage
}

type Coverage struct {
	PricedEvents int64
	TotalEvents  int64
	PricedTokens int64
	TotalTokens  int64
	confidence   string
	missing      map[string]*MissingModel
}

type CoverageSummary struct {
	ProfileID          string         `json:"profile_id"`
	Currency           string         `json:"currency"`
	PricedEvents       int64          `json:"priced_events"`
	TotalEvents        int64          `json:"total_events"`
	PricedTokens       int64          `json:"priced_tokens"`
	TotalTokens        int64          `json:"total_tokens"`
	EventCoverageRatio float64        `json:"event_coverage_ratio"`
	TokenCoverageRatio float64        `json:"token_coverage_ratio"`
	CoverageRatio      float64        `json:"coverage_ratio"`
	Confidence         string         `json:"confidence"`
	MissingModels      []MissingModel `json:"missing_models,omitempty"`
}

type MissingModel struct {
	Provider string `json:"provider"`
	Channel  string `json:"channel"`
	Model    string `json:"model"`
	Reason   string `json:"reason"`
	Events   int64  `json:"events"`
	Tokens   int64  `json:"tokens"`
}

func (a *AggregateCost) Add(ev Event, estimate Estimate) {
	a.Coverage.Add(ev, estimate)
	if estimate.Priced {
		a.EstimatedCostMicroUSD += estimate.CostMicroUSD
	}
}

func (c *Coverage) Add(ev Event, estimate Estimate) {
	tokens := eventTokens(ev)
	c.TotalEvents++
	c.TotalTokens += tokens
	if estimate.Priced {
		c.PricedEvents++
		c.PricedTokens += tokens
		c.confidence = combineConfidence(c.confidence, estimate.Confidence)
		return
	}
	c.confidence = combineConfidence(c.confidence, "missing")
	if c.missing == nil {
		c.missing = make(map[string]*MissingModel)
	}
	reason := estimate.MissingReason
	if reason == "" {
		reason = "missing_pricing_rule"
	}
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", ev.Provider, ev.Channel, ev.Model, reason)
	item := c.missing[key]
	if item == nil {
		item = &MissingModel{Provider: ev.Provider, Channel: ev.Channel, Model: ev.Model, Reason: reason}
		c.missing[key] = item
	}
	item.Events++
	item.Tokens += tokens
}

func (a AggregateCost) Summary(profile *Profile) *CoverageSummary {
	return a.Coverage.Summary(profile)
}

func (c Coverage) Summary(profile *Profile) *CoverageSummary {
	if c.TotalEvents == 0 && c.TotalTokens == 0 {
		return nil
	}
	eventRatio := 0.0
	if c.TotalEvents > 0 {
		eventRatio = float64(c.PricedEvents) / float64(c.TotalEvents)
	}
	tokenRatio := 0.0
	if c.TotalTokens > 0 {
		tokenRatio = float64(c.PricedTokens) / float64(c.TotalTokens)
	}
	confidence := c.confidence
	if confidence == "" {
		confidence = "missing"
	}
	missing := make([]MissingModel, 0, len(c.missing))
	for _, item := range c.missing {
		missing = append(missing, *item)
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Tokens == missing[j].Tokens {
			return missing[i].Model < missing[j].Model
		}
		return missing[i].Tokens > missing[j].Tokens
	})
	return &CoverageSummary{
		ProfileID:          profile.ID,
		Currency:           profile.Currency,
		PricedEvents:       c.PricedEvents,
		TotalEvents:        c.TotalEvents,
		PricedTokens:       c.PricedTokens,
		TotalTokens:        c.TotalTokens,
		EventCoverageRatio: eventRatio,
		TokenCoverageRatio: tokenRatio,
		CoverageRatio:      tokenRatio,
		Confidence:         confidence,
		MissingModels:      missing,
	}
}

func eventTokens(ev Event) int64 {
	if ev.TotalTokens > 0 {
		return ev.TotalTokens
	}
	return ev.InputTokens + ev.OutputTokens + ev.CacheCreationTokens + ev.CacheReadTokens + ev.ReasoningTokens
}

func combineConfidence(current, next string) string {
	if current == "" {
		return next
	}
	if confidenceRank(next) > confidenceRank(current) {
		return next
	}
	return current
}

func confidenceRank(value string) int {
	switch value {
	case "exact":
		return 1
	case "estimated":
		return 2
	case "approximate":
		return 3
	case "partial":
		return 4
	case "missing":
		return 5
	default:
		return 2
	}
}

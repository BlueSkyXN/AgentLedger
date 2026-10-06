package db

import (
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func contentSHA256ForEvent(event *model.UsageEvent) (string, error) {
	return fingerprint.ComputeContentSHA256(&fingerprint.ParsedRecord{
		Agent:                 event.Channel,
		Provider:              event.Provider,
		Model:                 event.ModelRaw,
		ModelNormalized:       event.ModelNormalized,
		ModelResolution:       event.ModelResolution,
		ModelIsFallback:       event.ModelIsFallback,
		TimestampMs:           event.TimestampMs,
		Granularity:           event.EventGranularity,
		InputTokens:           event.InputTokens,
		OutputTokens:          event.OutputTokens,
		ReasoningTokens:       event.ReasoningTokens,
		CacheCreationTokens:   event.CacheCreationTokens,
		CacheReadTokens:       event.CacheReadTokens,
		TotalTokens:           event.TotalTokens,
		SourceTotalTokens:     event.SourceTotalTokens,
		RawInputTokens:        event.RawInputTokens,
		CacheCreation1hTokens: event.CacheCreation1hTokens,
		SourceProduct:         event.SourceProduct,
		ObservabilityLevel:    event.ObservabilityLevel,
		TokenAccountingMethod: event.TokenAccountingMethod,
		AccountingProfile:     event.AccountingProfile,
	})
}

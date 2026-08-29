package db

import (
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestValidateCursorAgentExecAccounting(t *testing.T) {
	event := testEvent("cursor-accounting", "cursor-hash", 140)
	event.Channel = "cursor"
	event.SourceProduct = "cursor-agent-exec"
	event.TokenAccountingMethod = model.AccCursorAgentExec
	event.AccountingProfile = "cursor_agent_exec_v1"
	event.ObservabilityLevel = "partial"
	event.InputTokens = 50
	event.CacheReadTokens = 30
	event.CacheCreationTokens = 20
	event.OutputTokens = 40
	event.TotalTokens = 140
	rawInput := int64(100)
	sourceTotal := int64(140)
	event.RawInputTokens = &rawInput
	event.SourceTotalTokens = &sourceTotal
	if err := ValidateEvent(event); err != nil {
		t.Fatalf("valid Cursor accounting: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*model.UsageEvent)
	}{
		{name: "missing raw input", mutate: func(event *model.UsageEvent) { event.RawInputTokens = nil }},
		{name: "raw input mismatch", mutate: func(event *model.UsageEvent) { value := int64(101); event.RawInputTokens = &value }},
		{name: "source total mismatch", mutate: func(event *model.UsageEvent) { value := int64(141); event.SourceTotalTokens = &value }},
		{name: "canonical total mismatch", mutate: func(event *model.UsageEvent) { event.TotalTokens = 141 }},
		{name: "unsupported reasoning", mutate: func(event *model.UsageEvent) { event.ReasoningTokens = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := *event
			test.mutate(&copy)
			if err := ValidateEvent(&copy); !IsRejectError(err) {
				t.Fatalf("expected Cursor accounting rejection, got %v", err)
			}
		})
	}
}

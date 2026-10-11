package db

import (
	"database/sql"
	"errors"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func canonicalParserEvent(event *model.UsageEvent) *model.UsageEvent {
	if event == nil {
		return nil
	}
	updated := *event
	if event.Channel == "claude" && event.SourceProduct == "claude-code" &&
		event.ParserVersion == "claude-v1" && event.IdentityScope == "session" &&
		event.IdentityStrategy == "native_message" && event.RequestID == "" && event.MessageID != "" &&
		event.EventID == fingerprint.ClaudeLegacyMessageEventID(event.SessionKey, event.MessageID) {
		updated.EventID = fingerprint.ClaudeRequestlessEventID(event.SessionKey, event.MessageID, event.TimestampMs)
		updated.ParserVersion = "claude-v2"
	}
	if event.Channel == "copilot" && event.SourceProduct == "copilot-session-state" &&
		event.ParserVersion == "copilot-session-state-v1" && event.AccountingProfile == "input_includes_cache_read" &&
		event.TokenAccountingMethod == model.AccCopilotSessionMetrics && event.RawInputTokens != nil &&
		event.InputTokens+event.CacheReadTokens == *event.RawInputTokens &&
		event.CacheCreationTokens <= event.InputTokens && event.ReasoningTokens <= event.OutputTokens &&
		event.TotalTokens == event.InputTokens+event.OutputTokens+event.CacheReadTokens+event.CacheCreationTokens+event.ReasoningTokens {
		updated.InputTokens -= event.CacheCreationTokens
		updated.TotalTokens = *event.RawInputTokens + event.OutputTokens
		updated.ParserVersion = "copilot-session-state-v2"
		updated.AccountingProfile = model.CopilotSessionInclusiveProfile
	}
	if updated.AccountingProfile != event.AccountingProfile {
		if hash, err := contentSHA256ForEvent(&updated); err == nil {
			updated.ContentSHA256 = hash
		}
	}
	return &updated
}

func migrateClaudeRequestlessEvent(tx *sql.Tx, incoming *model.UsageEvent) error {
	if incoming.Channel != "claude" || incoming.SourceProduct != "claude-code" ||
		incoming.ParserVersion != "claude-v2" || incoming.RequestID != "" || incoming.MessageID == "" ||
		incoming.EventID != fingerprint.ClaudeRequestlessEventID(incoming.SessionKey, incoming.MessageID, incoming.TimestampMs) {
		return nil
	}
	legacyID := fingerprint.ClaudeLegacyMessageEventID(incoming.SessionKey, incoming.MessageID)
	legacy, err := selectEvent(tx, legacyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	canonical := canonicalParserEvent(legacy)
	if canonical.EventID == legacyID {
		return nil
	}
	if err := validateStoredEvent(legacy); err != nil {
		return err
	}
	if _, err := selectEvent(tx, canonical.EventID); err == nil {
		return reject("legacy_identity_collision")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(`UPDATE usage_events SET event_id = ?, parser_version = ? WHERE event_id = ?`, canonical.EventID, canonical.ParserVersion, legacyID)
	return err
}

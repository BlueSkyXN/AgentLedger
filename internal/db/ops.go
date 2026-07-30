package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

type ImportRun struct {
	ID            string
	StartedAtMs   int64
	FinishedAtMs  int64
	Status        string
	FilesScanned  int
	EventsAdded   int
	EventsUpdated int
	EventsSkipped int
	Error         string
}

func (d *Database) StartImportRun(runID string) error {
	_, err := d.conn.Exec(`
        INSERT INTO import_runs (id, started_at_ms, status)
        VALUES (?, ?, 'running')
    `, runID, time.Now().UnixMilli())
	return err
}

func (d *Database) FinishImportRun(runID string, filesScanned, eventsAdded, eventsUpdated, eventsSkipped int) error {
	return d.FinishImportRunWithStatus(runID, filesScanned, eventsAdded, eventsUpdated, eventsSkipped, "completed", "")
}

func (d *Database) FinishImportRunWithStatus(runID string, filesScanned, eventsAdded, eventsUpdated, eventsSkipped int, status, errorText string) error {
	if status == "" {
		status = "completed"
	}
	_, err := d.conn.Exec(`
        UPDATE import_runs SET
            finished_at_ms = ?,
            status = ?,
            files_scanned = ?,
            events_added = ?,
            events_updated = ?,
            events_skipped = ?,
            error = ?
        WHERE id = ?
    `, time.Now().UnixMilli(), status, filesScanned, eventsAdded, eventsUpdated, eventsSkipped, nullIfEmpty(errorText), runID)
	return err
}

func (d *Database) UpsertEvent(ev *model.UsageEvent) (string, error) {
	// Raw usage envelopes are parsing inputs only. The v2 fact table persists
	// structured usage fields and must never retain the envelope itself. Work
	// on a copy so callers can still use their parsed record for fingerprinting.
	storedEvent := *ev
	ev = &storedEvent
	ev.RawUsageJSON = ""
	ev.ModelResolution = modelResolutionForStorage(ev)
	tx, err := d.conn.Begin()
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	existing, err := selectEventForComparison(tx, ev.EventID)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	exactMatch := err == nil
	if !exactMatch {
		existing = nil
	}
	incoming := ev
	var protectExactClassification bool
	ev, protectExactClassification = preserveStrongerExactCodexClassification(existing, ev)

	sourceMatches, err := selectEventsForComparisonBySourceIdentity(tx, ev)
	if err != nil {
		return "", err
	}
	if existing != nil {
		redactedMatches, err := selectRedactedEventsForComparisonBySourceIdentity(tx, ev)
		if err != nil {
			return "", err
		}
		for _, match := range redactedMatches {
			sourceMatches = appendUniqueEvent(sourceMatches, match)
		}
	}
	refreshSourceMetadata := existing != nil && strings.TrimSpace(existing.SourceFile) == "" && strings.TrimSpace(ev.SourceFile) != ""
	refreshClassification := codexClassificationNeedsReconciliation(existing, ev, sourceMatches)
	if hasDifferentEventID(sourceMatches, ev.EventID) || refreshSourceMetadata || refreshClassification || protectExactClassification {
		var status string
		status, err = reconcileSourceIdentityMatches(tx, incoming, existing, sourceMatches)
		if err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return status, nil
	}
	if shouldReplaceWorkBuddyAutoModel(existing, ev) {
		existing.Provider = ev.Provider
		existing.ModelRaw = ev.ModelRaw
		existing.ModelNormalized = ev.ModelNormalized
		existing.ModelResolution = ev.ModelResolution
		existing.ModelIsFallback = ev.ModelIsFallback
		existing.RawUsageJSON = ""
		existing.UpdatedAtMs = ev.UpdatedAtMs
		if err = updateEvent(tx, existing); err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return "updated", nil
	}
	if existing == nil {
		if err = insertEvent(tx, ev); err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return "inserted", nil
	}

	if !isMoreComplete(ev, existing) {
		if mergeMissingMetadata(existing, ev) {
			if err = updateEventMetadata(tx, existing); err != nil {
				return "", err
			}
			if _, err = clearRawUsage(tx, existing.EventID); err != nil {
				return "", err
			}
			if err = tx.Commit(); err != nil {
				return "", err
			}
			return "updated", nil
		}
		cleared, clearErr := clearRawUsage(tx, existing.EventID)
		if clearErr != nil {
			return "", clearErr
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		if cleared {
			return "updated", nil
		}
		return "skipped", nil
	}

	ev.ImportedAtMs = existing.ImportedAtMs
	preserveExistingSourceMetadata(ev, existing)
	// RequestCount is independent of token/timing completeness. An unknown
	// incoming value must never clear a previously known count.
	ev.RequestCount = selectRequestCount(ev, existing, nil)
	if err = updateEvent(tx, ev); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return "updated", nil
}

func shouldReplaceWorkBuddyAutoModel(existing, incoming *model.UsageEvent) bool {
	return existing != nil && incoming != nil &&
		incoming.Channel == "workbuddy" && existing.Channel == "workbuddy" &&
		strings.EqualFold(strings.TrimSpace(existing.ModelRaw), "auto") &&
		strings.EqualFold(strings.TrimSpace(incoming.ModelRaw), "auto") &&
		strings.EqualFold(strings.TrimSpace(existing.ModelNormalized), "auto") &&
		strings.EqualFold(strings.TrimSpace(incoming.ModelNormalized), "unknown") &&
		incoming.ModelIsFallback && incoming.ModelResolution == model.ModelResolutionUnknown
}

const eventComparisonColumns = `
    event_id, dedupe_key, dedupe_strategy,
    channel, COALESCE(provider, ''), COALESCE(model_raw, ''), COALESCE(model_normalized, ''), COALESCE(model_resolution, ''),
    COALESCE(source_agent, ''), COALESCE(source_product, ''), COALESCE(observability_level, ''), model_is_fallback,
    source_total_tokens, raw_input_tokens, COALESCE(token_accounting_method, ''), COALESCE(accounting_profile, ''),
    timestamp_ms, COALESCE(session_id, ''), COALESCE(session_path_id, ''), COALESCE(turn_id, ''), COALESCE(project_path, ''),
	COALESCE(message_id, ''), COALESCE(request_id, ''), COALESCE(source_file, ''), COALESCE(line_number, 0), COALESCE(raw_sha256, ''),
	input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
	request_count,
	request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
	recorded_cost_usd, COALESCE(raw_usage_json, ''), imported_at_ms, updated_at_ms
`

func selectEventForComparison(tx *sql.Tx, eventID string) (*model.UsageEvent, error) {
	row := tx.QueryRow(`SELECT `+eventComparisonColumns+` FROM usage_events WHERE event_id = ?`, eventID)
	return scanEventForComparison(row)
}

func selectEventsForComparisonBySourceIdentity(tx *sql.Tx, ev *model.UsageEvent) ([]*model.UsageEvent, error) {
	if ev.Channel != "codex" || strings.TrimSpace(ev.SourceFile) == "" || ev.LineNumber <= 0 || strings.TrimSpace(ev.RawSHA256) == "" {
		return nil, nil
	}
	rows, err := tx.Query(`SELECT `+eventComparisonColumns+`
		FROM usage_events
		WHERE source_file = ? AND line_number = ? AND raw_sha256 = ? AND channel = ?
		ORDER BY imported_at_ms ASC, event_id ASC
	`, ev.SourceFile, ev.LineNumber, ev.RawSHA256, ev.Channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []*model.UsageEvent
	for rows.Next() {
		match, err := scanEventForComparison(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

func selectRedactedEventsForComparisonBySourceIdentity(tx *sql.Tx, ev *model.UsageEvent) ([]*model.UsageEvent, error) {
	if ev.Channel != "codex" || ev.LineNumber <= 0 || strings.TrimSpace(ev.RawSHA256) == "" || strings.TrimSpace(ev.SessionID) == "" {
		return nil, nil
	}
	rows, err := tx.Query(`SELECT `+eventComparisonColumns+`
		FROM usage_events
		WHERE source_file IS NULL
			AND line_number = ? AND raw_sha256 = ? AND channel = ?
			AND session_id = ? AND timestamp_ms = ?
		ORDER BY imported_at_ms ASC, event_id ASC
	`, ev.LineNumber, ev.RawSHA256, ev.Channel, ev.SessionID, ev.TimestampMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []*model.UsageEvent
	for rows.Next() {
		match, err := scanEventForComparison(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

func hasDifferentEventID(events []*model.UsageEvent, eventID string) bool {
	for _, event := range events {
		if event.EventID != eventID {
			return true
		}
	}
	return false
}

func reconcileSourceIdentityMatches(tx *sql.Tx, incoming, exact *model.UsageEvent, sourceMatches []*model.UsageEvent) (string, error) {
	var stored []*model.UsageEvent
	for _, match := range sourceMatches {
		stored = appendUniqueEvent(stored, match)
	}
	stored = appendUniqueEvent(stored, exact)
	if hasConflictingExplicitCodexModels(incoming, stored) {
		cleared := false
		for _, match := range stored {
			changed, err := clearRawUsage(tx, match.EventID)
			if err != nil {
				return "", err
			}
			cleared = cleared || changed
		}
		if cleared {
			return "updated", nil
		}
		return "skipped", nil
	}

	var canonical *model.UsageEvent
	for {
		canonical = buildCanonicalReconciledEvent(incoming, exact, stored)
		canonicalMatch, err := selectEventForComparison(tx, canonical.EventID)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return "", err
		}
		if containsEventID(stored, canonicalMatch.EventID) {
			break
		}
		stored = append(stored, canonicalMatch)
		if hasConflictingExplicitCodexModels(incoming, stored) {
			return "skipped", nil
		}
	}

	if len(stored) == 1 && sameEventContentExceptRawUsage(canonical, stored[0]) {
		cleared, err := clearRawUsage(tx, stored[0].EventID)
		if err != nil {
			return "", err
		}
		if cleared {
			return "updated", nil
		}
		return "skipped", nil
	}

	for _, match := range stored {
		if err := deleteEventByID(tx, match.EventID); err != nil {
			return "", err
		}
	}
	if err := insertEvent(tx, canonical); err != nil {
		return "", err
	}
	return "updated", nil
}

func appendUniqueEvent(events []*model.UsageEvent, candidate *model.UsageEvent) []*model.UsageEvent {
	if candidate == nil || containsEventID(events, candidate.EventID) {
		return events
	}
	return append(events, candidate)
}

func containsEventID(events []*model.UsageEvent, eventID string) bool {
	for _, event := range events {
		if event.EventID == eventID {
			return true
		}
	}
	return false
}

func buildCanonicalReconciledEvent(incoming, exact *model.UsageEvent, stored []*model.UsageEvent) *model.UsageEvent {
	usageWinner := *incoming
	for _, candidate := range stored {
		if isMoreComplete(candidate, &usageWinner) {
			usageWinner = *candidate
		}
	}
	accountingCandidates := make([]*model.UsageEvent, 0, len(stored)+1)
	accountingCandidates = append(accountingCandidates, incoming)
	accountingCandidates = append(accountingCandidates, stored...)
	mergeMissingAccountingMetadataFromBestDonor(&usageWinner, accountingCandidates)

	canonical := *incoming
	preserveReconciledSourceMetadata(&canonical, stored)
	preserveStrongerCodexClassification(&canonical, incoming, exact, stored)
	applyUsageWinner(&canonical, &usageWinner)
	canonical.RequestCount = selectRequestCount(incoming, exact, stored)
	for _, candidate := range stored {
		if canonical.ImportedAtMs <= 0 || (candidate.ImportedAtMs > 0 && candidate.ImportedAtMs < canonical.ImportedAtMs) {
			canonical.ImportedAtMs = candidate.ImportedAtMs
		}
		if candidate.UpdatedAtMs > canonical.UpdatedAtMs {
			canonical.UpdatedAtMs = candidate.UpdatedAtMs
		}
	}

	canonical.RawUsageJSON = ""
	if protected := storedIdentityProtection(exact, stored); protected != nil {
		canonical.EventID = protected.EventID
		canonical.DedupeKey = protected.DedupeKey
		canonical.DedupeStrategy = protected.DedupeStrategy
	} else if rawEvidenceIdentityProtected(incoming.DedupeStrategy) {
		canonical.EventID = incoming.EventID
		canonical.DedupeKey = incoming.DedupeKey
		canonical.DedupeStrategy = incoming.DedupeStrategy
	} else {
		eventID, strategy := computeEventFingerprint(&canonical)
		canonical.EventID = eventID
		canonical.DedupeKey = eventID
		canonical.DedupeStrategy = string(strategy)
	}
	return &canonical
}

func preserveReconciledSourceMetadata(target *model.UsageEvent, stored []*model.UsageEvent) {
	if len(stored) == 0 {
		return
	}

	sourceMetadataBaseline := stored[0]
	for _, candidate := range stored[1:] {
		if candidate.ImportedAtMs < sourceMetadataBaseline.ImportedAtMs ||
			(candidate.ImportedAtMs == sourceMetadataBaseline.ImportedAtMs && candidate.EventID < sourceMetadataBaseline.EventID) {
			sourceMetadataBaseline = candidate
		}
	}
	preserveExistingSourceMetadata(target, sourceMetadataBaseline)

	for _, candidate := range stored {
		mergeMissingSourceMetadata(target, candidate)
	}
}

func mergeMissingSourceMetadata(target, candidate *model.UsageEvent) {
	if target.SourceAgent == "" && candidate.SourceAgent != "" {
		target.SourceAgent = candidate.SourceAgent
	}
	if target.SourceProduct == "" && candidate.SourceProduct != "" {
		target.SourceProduct = candidate.SourceProduct
	} else if shouldCorrectSourceProduct(target, candidate) {
		target.SourceProduct = candidate.SourceProduct
	}
	if (target.ObservabilityLevel == "" || target.ObservabilityLevel == "unknown") && candidate.ObservabilityLevel != "" {
		target.ObservabilityLevel = candidate.ObservabilityLevel
	}
	if target.SessionPathID == "" && candidate.SessionPathID != "" {
		target.SessionPathID = candidate.SessionPathID
	}
	if target.TurnID == "" && candidate.TurnID != "" {
		target.TurnID = candidate.TurnID
	}
	if target.ProjectPath == "" || shouldUpgradeProjectPath(target.ProjectPath, candidate.ProjectPath) {
		target.ProjectPath = candidate.ProjectPath
	}
}

func mergeMissingAccountingMetadataFromBestDonor(target *model.UsageEvent, candidates []*model.UsageEvent) {
	var donor *model.UsageEvent
	bestScore := -1
	for _, candidate := range candidates {
		if candidate == nil || !sameTokenUsage(target, candidate) || !accountingMetadataCompatible(target, candidate) {
			continue
		}
		score := accountingMetadataScore(candidate)
		if score > bestScore {
			donor = candidate
			bestScore = score
		}
	}
	if donor != nil {
		mergeMissingAccountingMetadata(target, donor)
	}
}

func accountingMetadataCompatible(target, candidate *model.UsageEvent) bool {
	if target.SourceTotalTokens != nil && (candidate.SourceTotalTokens == nil || *target.SourceTotalTokens != *candidate.SourceTotalTokens) {
		return false
	}
	if target.RawInputTokens != nil && (candidate.RawInputTokens == nil || *target.RawInputTokens != *candidate.RawInputTokens) {
		return false
	}
	if target.TokenAccountingMethod != "" && target.TokenAccountingMethod != candidate.TokenAccountingMethod {
		return false
	}
	return target.AccountingProfile == "" || target.AccountingProfile == candidate.AccountingProfile
}

func accountingMetadataScore(ev *model.UsageEvent) int {
	score := 0
	if ev.SourceTotalTokens != nil {
		score++
	}
	if ev.RawInputTokens != nil {
		score++
	}
	if ev.TokenAccountingMethod != "" {
		score += 2
	}
	if ev.AccountingProfile != "" {
		score += 2
	}
	return score
}

func mergeMissingAccountingMetadata(target, candidate *model.UsageEvent) bool {
	if !sameTokenUsage(target, candidate) {
		return false
	}
	changed := false
	if target.SourceTotalTokens == nil && candidate.SourceTotalTokens != nil {
		target.SourceTotalTokens = candidate.SourceTotalTokens
		changed = true
	}
	if target.RawInputTokens == nil && candidate.RawInputTokens != nil {
		target.RawInputTokens = candidate.RawInputTokens
		changed = true
	}
	if target.TokenAccountingMethod == "" && candidate.TokenAccountingMethod != "" {
		target.TokenAccountingMethod = candidate.TokenAccountingMethod
		changed = true
	}
	if target.AccountingProfile == "" && candidate.AccountingProfile != "" {
		target.AccountingProfile = candidate.AccountingProfile
		changed = true
	}
	return changed
}

func applyUsageWinner(target, winner *model.UsageEvent) {
	target.InputTokens = winner.InputTokens
	target.OutputTokens = winner.OutputTokens
	target.ReasoningTokens = winner.ReasoningTokens
	target.CacheCreationTokens = winner.CacheCreationTokens
	target.CacheReadTokens = winner.CacheReadTokens
	target.TotalTokens = winner.TotalTokens
	target.RequestStartedAtMs = winner.RequestStartedAtMs
	target.FirstTokenAtMs = winner.FirstTokenAtMs
	target.CompletedAtMs = winner.CompletedAtMs
	target.TotalDurationMs = winner.TotalDurationMs
	target.TTFTMs = winner.TTFTMs
	target.OutputDurationMs = winner.OutputDurationMs
	target.OutputTPS = winner.OutputTPS
	target.RecordedCostUSD = winner.RecordedCostUSD
	target.SourceTotalTokens = winner.SourceTotalTokens
	target.RawInputTokens = winner.RawInputTokens
	target.TokenAccountingMethod = winner.TokenAccountingMethod
	target.AccountingProfile = winner.AccountingProfile
}

func computeEventFingerprint(ev *model.UsageEvent) (string, fingerprint.Strategy) {
	agent := ev.SourceAgent
	if agent == "" {
		agent = ev.Channel
	}
	return fingerprint.Compute(&fingerprint.ParsedRecord{
		Agent:               agent,
		Provider:            ev.Provider,
		Model:               ev.ModelRaw,
		TimestampMs:         ev.TimestampMs,
		SessionID:           ev.SessionID,
		MessageID:           ev.MessageID,
		RequestID:           ev.RequestID,
		InputTokens:         ev.InputTokens,
		OutputTokens:        ev.OutputTokens,
		CacheCreationTokens: ev.CacheCreationTokens,
		CacheReadTokens:     ev.CacheReadTokens,
		ReasoningTokens:     ev.ReasoningTokens,
		TotalTokens:         ev.TotalTokens,
		SourceTotalTokens:   ev.SourceTotalTokens,
		SourceFile:          ev.SourceFile,
		LineNumber:          ev.LineNumber,
		RawSHA256:           ev.RawSHA256,
	})
}

func sameEventContentExceptRawUsage(left, right *model.UsageEvent) bool {
	leftCopy := *left
	rightCopy := *right
	leftCopy.ImportedAtMs = 0
	leftCopy.UpdatedAtMs = 0
	leftCopy.RawUsageJSON = ""
	rightCopy.ImportedAtMs = 0
	rightCopy.UpdatedAtMs = 0
	rightCopy.RawUsageJSON = ""
	return reflect.DeepEqual(leftCopy, rightCopy)
}

func codexClassificationNeedsReconciliation(existing, incoming *model.UsageEvent, sourceMatches []*model.UsageEvent) bool {
	if existing == nil || incoming.Channel != "codex" || !containsEventID(sourceMatches, existing.EventID) {
		return false
	}
	classificationChanged := existing.Provider != incoming.Provider ||
		existing.ModelRaw != incoming.ModelRaw ||
		existing.ModelNormalized != incoming.ModelNormalized ||
		existing.ModelResolution != incoming.ModelResolution ||
		existing.ModelIsFallback != incoming.ModelIsFallback
	if !classificationChanged {
		return false
	}
	if rawEvidenceIdentityProtected(existing.DedupeStrategy) {
		return true
	}
	recomputedEventID, strategy := computeEventFingerprint(incoming)
	return incoming.EventID == recomputedEventID &&
		incoming.DedupeKey == recomputedEventID &&
		incoming.DedupeStrategy == string(strategy)
}

// preserveStrongerCodexClassification lets current explicit parser output win,
// while retaining stored explicit values over fallback or unknown input.
func preserveStrongerCodexClassification(target, incoming, exact *model.UsageEvent, stored []*model.UsageEvent) {
	target.Provider = selectCodexProvider(incoming, exact, stored)
	modelSource := selectCodexModel(incoming, exact, stored)
	target.ModelRaw = modelSource.ModelRaw
	target.ModelNormalized = modelSource.ModelNormalized
	target.ModelResolution = modelSource.ModelResolution
	target.ModelIsFallback = modelSource.ModelIsFallback
}

func selectCodexProvider(incoming, exact *model.UsageEvent, stored []*model.UsageEvent) string {
	if incoming.Provider == "openai" {
		return incoming.Provider
	}
	if exact != nil && exact.Provider == "openai" {
		return exact.Provider
	}
	for _, candidate := range stored {
		if candidate.Provider == "openai" {
			return candidate.Provider
		}
	}
	if exact != nil && isKnownClassificationValue(exact.Provider) {
		return exact.Provider
	}
	if candidate := selectBestStoredCodexClassification(exact, stored, func(candidate *model.UsageEvent) bool {
		return isKnownClassificationValue(candidate.Provider)
	}); candidate != nil {
		return candidate.Provider
	}
	return incoming.Provider
}

func selectCodexModel(incoming, exact *model.UsageEvent, stored []*model.UsageEvent) *model.UsageEvent {
	if hasCompleteExplicitCodexModel(incoming) {
		return incoming
	}
	if candidate := selectBestStoredCodexClassification(exact, stored, hasExplicitCodexModel); candidate != nil {
		return candidate
	}
	if hasExplicitCodexModel(incoming) {
		return incoming
	}
	if shouldReplaceLegacyCodexDefaultFallback(incoming, exact, stored) {
		return incoming
	}
	if candidate := selectBestStoredCodexClassification(exact, stored, hasKnownCodexModel); candidate != nil {
		return candidate
	}
	return incoming
}

func shouldReplaceLegacyCodexDefaultFallback(incoming, exact *model.UsageEvent, stored []*model.UsageEvent) bool {
	if incoming == nil || !incoming.ModelIsFallback ||
		!strings.EqualFold(strings.TrimSpace(incoming.ModelRaw), "unknown") ||
		!strings.EqualFold(strings.TrimSpace(incoming.ModelNormalized), "unknown") {
		return false
	}
	foundLegacyDefault := false
	for _, candidate := range append([]*model.UsageEvent{exact}, stored...) {
		if candidate == nil || !candidate.ModelIsFallback {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(candidate.ModelRaw), "gpt-5") &&
			strings.EqualFold(strings.TrimSpace(candidate.ModelNormalized), "gpt-5") {
			foundLegacyDefault = true
			continue
		}
		if hasKnownCodexModel(candidate) {
			return false
		}
	}
	return foundLegacyDefault
}

func selectBestStoredCodexClassification(exact *model.UsageEvent, stored []*model.UsageEvent, eligible func(*model.UsageEvent) bool) *model.UsageEvent {
	if exact != nil && eligible(exact) {
		return exact
	}
	var best *model.UsageEvent
	for _, candidate := range stored {
		if !eligible(candidate) {
			continue
		}
		if best == nil || candidate.EventID < best.EventID {
			best = candidate
		}
	}
	return best
}

func hasConflictingExplicitCodexModels(incoming *model.UsageEvent, stored []*model.UsageEvent) bool {
	if hasCompleteExplicitCodexModel(incoming) {
		return false
	}
	var modelRaw, modelNormalized string
	found := false
	for _, candidate := range stored {
		if !hasExplicitCodexModel(candidate) {
			continue
		}
		candidateRaw := strings.TrimSpace(candidate.ModelRaw)
		candidateNormalized := strings.TrimSpace(candidate.ModelNormalized)
		if !found {
			modelRaw = candidateRaw
			modelNormalized = candidateNormalized
			found = true
			continue
		}
		if candidateRaw != modelRaw || candidateNormalized != modelNormalized {
			return true
		}
	}
	return false
}

func preserveStrongerExactCodexClassification(existing, incoming *model.UsageEvent) (*model.UsageEvent, bool) {
	if existing == nil || incoming.Channel != "codex" ||
		(incoming.Provider == "openai" && hasCompleteExplicitCodexModel(incoming)) {
		return incoming, false
	}
	candidate := *incoming
	preserveStrongerCodexClassification(&candidate, incoming, existing, []*model.UsageEvent{existing})
	if candidate.Provider == incoming.Provider &&
		candidate.ModelRaw == incoming.ModelRaw &&
		candidate.ModelNormalized == incoming.ModelNormalized &&
		candidate.ModelResolution == incoming.ModelResolution &&
		candidate.ModelIsFallback == incoming.ModelIsFallback {
		return incoming, false
	}
	recomputedEventID, strategy := computeEventFingerprint(&candidate)
	needsReconciliation := incoming.EventID != recomputedEventID ||
		incoming.DedupeKey != recomputedEventID ||
		incoming.DedupeStrategy != string(strategy)
	return &candidate, needsReconciliation
}

func hasCompleteExplicitCodexModel(ev *model.UsageEvent) bool {
	return ev != nil && !ev.ModelIsFallback &&
		isKnownClassificationValue(ev.ModelRaw) &&
		isKnownClassificationValue(ev.ModelNormalized)
}

func hasExplicitCodexModel(ev *model.UsageEvent) bool {
	return ev != nil && !ev.ModelIsFallback && hasKnownCodexModel(ev)
}

func hasKnownCodexModel(ev *model.UsageEvent) bool {
	return ev != nil && (isKnownClassificationValue(ev.ModelRaw) || isKnownClassificationValue(ev.ModelNormalized))
}

func isKnownClassificationValue(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.EqualFold(value, "unknown")
}

type eventComparisonScanner interface {
	Scan(dest ...any) error
}

func scanEventForComparison(row eventComparisonScanner, additionalDestinations ...any) (*model.UsageEvent, error) {
	var ev model.UsageEvent
	var requestStarted, firstToken, completed, totalDuration, ttft, outputDuration sql.NullInt64
	var sourceTotal, rawInput, requestCount sql.NullInt64
	var outputTPS, recordedCost sql.NullFloat64
	var lineNumber int64
	var modelIsFallback int
	destinations := []any{
		&ev.EventID,
		&ev.DedupeKey,
		&ev.DedupeStrategy,
		&ev.Channel,
		&ev.Provider,
		&ev.ModelRaw,
		&ev.ModelNormalized,
		&ev.ModelResolution,
		&ev.SourceAgent,
		&ev.SourceProduct,
		&ev.ObservabilityLevel,
		&modelIsFallback,
		&sourceTotal,
		&rawInput,
		&ev.TokenAccountingMethod,
		&ev.AccountingProfile,
		&ev.TimestampMs,
		&ev.SessionID,
		&ev.SessionPathID,
		&ev.TurnID,
		&ev.ProjectPath,
		&ev.MessageID,
		&ev.RequestID,
		&ev.SourceFile,
		&lineNumber,
		&ev.RawSHA256,
		&ev.InputTokens,
		&ev.OutputTokens,
		&ev.ReasoningTokens,
		&ev.CacheCreationTokens,
		&ev.CacheReadTokens,
		&ev.TotalTokens,
		&requestCount,
		&requestStarted,
		&firstToken,
		&completed,
		&totalDuration,
		&ttft,
		&outputDuration,
		&outputTPS,
		&recordedCost,
		&ev.RawUsageJSON,
		&ev.ImportedAtMs,
		&ev.UpdatedAtMs,
	}
	destinations = append(destinations, additionalDestinations...)
	if err := row.Scan(destinations...); err != nil {
		return nil, err
	}
	ev.RequestStartedAtMs = nullInt64Ptr(requestStarted)
	ev.FirstTokenAtMs = nullInt64Ptr(firstToken)
	ev.CompletedAtMs = nullInt64Ptr(completed)
	ev.TotalDurationMs = nullInt64Ptr(totalDuration)
	ev.TTFTMs = nullInt64Ptr(ttft)
	ev.OutputDurationMs = nullInt64Ptr(outputDuration)
	ev.OutputTPS = nullFloat64Ptr(outputTPS)
	ev.RecordedCostUSD = nullFloat64Ptr(recordedCost)
	ev.ModelIsFallback = modelIsFallback != 0
	ev.SourceTotalTokens = nullInt64Ptr(sourceTotal)
	ev.RawInputTokens = nullInt64Ptr(rawInput)
	ev.RequestCount = nullInt64Ptr(requestCount)
	ev.LineNumber = int(lineNumber)
	return &ev, nil
}

func isMoreComplete(candidate, existing *model.UsageEvent) bool {
	if isClaudeEvent(candidate) || isClaudeEvent(existing) {
		if candidate.TotalTokens != existing.TotalTokens {
			return candidate.TotalTokens > existing.TotalTokens
		}
	}
	return completenessScore(candidate) > completenessScore(existing)
}

func isClaudeEvent(ev *model.UsageEvent) bool {
	return ev.Channel == "claude" || ev.SourceAgent == "claude" || ev.SourceProduct == "claude-code"
}

func completenessScore(ev *model.UsageEvent) int64 {
	var score int64
	if ev.RequestStartedAtMs != nil || ev.FirstTokenAtMs != nil || ev.CompletedAtMs != nil ||
		ev.TotalDurationMs != nil || ev.TTFTMs != nil || ev.OutputDurationMs != nil || ev.OutputTPS != nil {
		score += 1_000_000_000_000
	}
	if ev.RecordedCostUSD != nil {
		score += 100_000_000_000
	}
	if ev.ModelRaw != "" || ev.ModelNormalized != "" {
		score += 10_000_000_000
	}
	score += ev.TotalTokens
	return score
}

func insertEvent(exec interface {
	Exec(string, ...any) (sql.Result, error)
}, ev *model.UsageEvent) error {
	_, err := exec.Exec(`
        INSERT INTO usage_events (
            event_id, dedupe_key, dedupe_strategy,
            channel, provider, model_raw, model_normalized, model_resolution,
            source_agent, source_product, observability_level, model_is_fallback, source_total_tokens, raw_input_tokens, token_accounting_method, accounting_profile,
            timestamp_ms, session_id, session_path_id, turn_id, project_path, message_id, request_id, source_file, line_number, raw_sha256,
	            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens, request_count,
            request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, raw_usage_json,
            imported_at_ms, updated_at_ms
        ) VALUES (
            ?, ?, ?,
            ?, ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
	            ?, ?, ?, ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?, ?,
            ?, ?,
            ?, ?
        )
    `, eventArgs(ev)...)
	return err
}

func updateEvent(tx *sql.Tx, ev *model.UsageEvent) error {
	return updateEventByID(tx, ev.EventID, ev)
}

func updateEventByID(tx *sql.Tx, existingEventID string, ev *model.UsageEvent) error {
	args := []any{
		ev.EventID, ev.DedupeKey, ev.DedupeStrategy,
		ev.Channel, ev.Provider, ev.ModelRaw, ev.ModelNormalized, modelResolutionForStorage(ev),
		nullIfEmpty(ev.SourceAgent), nullIfEmpty(ev.SourceProduct), nullIfEmpty(ev.ObservabilityLevel), boolToInt(ev.ModelIsFallback), nullableInt64(ev.SourceTotalTokens), nullableInt64(ev.RawInputTokens), nullIfEmpty(ev.TokenAccountingMethod), nullIfEmpty(ev.AccountingProfile),
		ev.TimestampMs, ev.SessionID, ev.SessionPathID, ev.TurnID, ev.ProjectPath, ev.MessageID, ev.RequestID, ev.SourceFile, ev.LineNumber, ev.RawSHA256,
		ev.InputTokens, ev.OutputTokens, ev.ReasoningTokens, ev.CacheCreationTokens, ev.CacheReadTokens, ev.TotalTokens, nullableInt64(ev.RequestCount),
		ev.RequestStartedAtMs, ev.FirstTokenAtMs, ev.CompletedAtMs, ev.TotalDurationMs, ev.TTFTMs, ev.OutputDurationMs, ev.OutputTPS,
		ev.RecordedCostUSD, nullIfEmpty(ev.RawUsageJSON),
		ev.UpdatedAtMs,
		existingEventID,
	}
	_, err := tx.Exec(`
        UPDATE usage_events SET
            event_id = ?, dedupe_key = ?, dedupe_strategy = ?,
            channel = ?, provider = ?, model_raw = ?, model_normalized = ?, model_resolution = ?,
            source_agent = ?, source_product = ?, observability_level = ?, model_is_fallback = ?, source_total_tokens = ?, raw_input_tokens = ?, token_accounting_method = ?, accounting_profile = ?,
            timestamp_ms = ?, session_id = ?, session_path_id = ?, turn_id = ?, project_path = ?, message_id = ?, request_id = ?, source_file = ?, line_number = ?, raw_sha256 = ?,
	            input_tokens = ?, output_tokens = ?, reasoning_tokens = ?, cache_creation_tokens = ?, cache_read_tokens = ?, total_tokens = ?, request_count = COALESCE(?, request_count),
            request_started_at_ms = ?, first_token_at_ms = ?, completed_at_ms = ?, total_duration_ms = ?, ttft_ms = ?, output_duration_ms = ?, output_tps = ?,
            recorded_cost_usd = ?, raw_usage_json = ?,
            updated_at_ms = ?
        WHERE event_id = ?
    `, args...)
	return err
}

func updateEventMetadata(tx *sql.Tx, ev *model.UsageEvent) error {
	_, err := tx.Exec(`
        UPDATE usage_events SET
            source_agent = ?,
            source_product = ?,
            observability_level = ?,
            model_is_fallback = ?,
            model_resolution = ?,
            source_total_tokens = ?,
            raw_input_tokens = ?,
            token_accounting_method = ?,
	            accounting_profile = ?,
	            request_count = ?,
	            session_path_id = ?,
            turn_id = ?,
            project_path = ?,
            updated_at_ms = ?
        WHERE event_id = ?
    `,
		nullIfEmpty(ev.SourceAgent),
		nullIfEmpty(ev.SourceProduct),
		nullIfEmpty(ev.ObservabilityLevel),
		boolToInt(ev.ModelIsFallback),
		modelResolutionForStorage(ev),
		nullableInt64(ev.SourceTotalTokens),
		nullableInt64(ev.RawInputTokens),
		nullIfEmpty(ev.TokenAccountingMethod),
		nullIfEmpty(ev.AccountingProfile),
		nullableInt64(ev.RequestCount),
		nullIfEmpty(ev.SessionPathID),
		nullIfEmpty(ev.TurnID),
		nullIfEmpty(ev.ProjectPath),
		ev.UpdatedAtMs,
		ev.EventID,
	)
	return err
}

func deleteEventByID(tx *sql.Tx, eventID string) error {
	_, err := tx.Exec(`DELETE FROM usage_events WHERE event_id = ?`, eventID)
	return err
}

// clearRawUsage makes a legacy row comply with the statistics-only storage
// boundary. It intentionally treats an empty string as stale raw data too.
func clearRawUsage(tx *sql.Tx, eventID string) (bool, error) {
	result, err := tx.Exec(`
		UPDATE usage_events
		SET raw_usage_json = NULL
		WHERE event_id = ? AND raw_usage_json IS NOT NULL
	`, eventID)
	if err != nil {
		return false, fmt.Errorf("clear raw usage evidence: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("verify raw usage evidence cleanup: %w", err)
	}
	return changed > 0, nil
}

func eventArgs(ev *model.UsageEvent) []any {
	return []any{
		ev.EventID, ev.DedupeKey, ev.DedupeStrategy,
		ev.Channel, ev.Provider, ev.ModelRaw, ev.ModelNormalized, modelResolutionForStorage(ev),
		nullIfEmpty(ev.SourceAgent), nullIfEmpty(ev.SourceProduct), nullIfEmpty(ev.ObservabilityLevel), boolToInt(ev.ModelIsFallback), nullableInt64(ev.SourceTotalTokens), nullableInt64(ev.RawInputTokens), nullIfEmpty(ev.TokenAccountingMethod), nullIfEmpty(ev.AccountingProfile),
		ev.TimestampMs, ev.SessionID, ev.SessionPathID, ev.TurnID, ev.ProjectPath, ev.MessageID, ev.RequestID, ev.SourceFile, ev.LineNumber, ev.RawSHA256,
		ev.InputTokens, ev.OutputTokens, ev.ReasoningTokens, ev.CacheCreationTokens, ev.CacheReadTokens, ev.TotalTokens, nullableInt64(ev.RequestCount),
		ev.RequestStartedAtMs, ev.FirstTokenAtMs, ev.CompletedAtMs, ev.TotalDurationMs, ev.TTFTMs, ev.OutputDurationMs, ev.OutputTPS,
		ev.RecordedCostUSD, nullIfEmpty(ev.RawUsageJSON),
		ev.ImportedAtMs, ev.UpdatedAtMs,
	}
}

func modelResolutionForStorage(ev *model.UsageEvent) string {
	if value := strings.TrimSpace(ev.ModelResolution); value != "" {
		return value
	}
	if ev.ModelIsFallback || !hasKnownCodexModel(ev) {
		return model.ModelResolutionUnknown
	}
	return model.ModelResolutionLegacyUnclassified
}

// MergeResult is an aggregate-only merge outcome. It never includes incoming
// event identifiers, source paths, or raw usage data.
type MergeResult struct {
	Inserted           int64
	Skipped            int64
	RawEvidenceOmitted int64
}

// MergeFrom attaches another v2 .aldb database and imports unseen events.
// All destination writes are atomic and incoming raw envelopes are discarded.
func (d *Database) MergeFrom(incomingPath string) (result MergeResult, err error) {
	absPath, err := filepath.Abs(incomingPath)
	if err != nil {
		return result, errors.New("invalid incoming database path")
	}
	if destinationPath, resolveErr := filepath.Abs(d.path); resolveErr == nil && destinationPath == absPath {
		return result, errors.New("incoming database must differ from destination")
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return result, errors.New("cannot access incoming database")
	}
	if info.IsDir() {
		return result, errors.New("incoming database path is a directory")
	}

	f, err := os.Open(absPath)
	if err != nil {
		return result, errors.New("cannot open incoming database")
	}
	header := make([]byte, 16)
	_, err = f.Read(header)
	_ = f.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		return result, errors.New("incoming database is not valid SQLite")
	}

	escapedPath := strings.ReplaceAll(absPath, "'", "''")
	if _, err = d.conn.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS incoming", escapedPath)); err != nil {
		return result, errors.New("failed to attach incoming database")
	}
	defer func() {
		if _, detachErr := d.conn.Exec("DETACH DATABASE incoming"); detachErr != nil && err == nil {
			err = errors.New("failed to detach incoming database")
		}
	}()

	var version string
	if err = d.conn.QueryRow(`SELECT value FROM incoming.meta WHERE key='schema_version'`).Scan(&version); err != nil {
		return result, errors.New("incoming database missing schema metadata")
	}
	if version != SchemaVersion {
		return result, errors.New("incoming database schema version is not compatible")
	}

	var totalIncoming int64
	if err = d.conn.QueryRow("SELECT COUNT(*) FROM incoming.usage_events").Scan(&totalIncoming); err != nil {
		return result, errors.New("failed to count incoming events")
	}

	selects, err := incomingCompatibilitySelects(d.conn)
	if err != nil {
		return result, err
	}
	tx, err := d.conn.Begin()
	if err != nil {
		return result, fmt.Errorf("start merge transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = tx.QueryRow(`
		SELECT COUNT(*)
		FROM incoming.usage_events AS candidate
		WHERE NOT EXISTS (SELECT 1 FROM usage_events WHERE event_id = candidate.event_id)
			AND candidate.raw_usage_json IS NOT NULL
	`).Scan(&result.RawEvidenceOmitted); err != nil {
		return result, errors.New("inspect incoming raw usage evidence")
	}
	query := fmt.Sprintf(`
        INSERT OR IGNORE INTO usage_events (
            event_id, dedupe_key, dedupe_strategy,
            channel, provider, model_raw, model_normalized, model_resolution,
            source_agent, source_product, observability_level, model_is_fallback, source_total_tokens, raw_input_tokens, token_accounting_method, accounting_profile,
            timestamp_ms, session_id, session_path_id, turn_id, project_path, message_id, request_id, source_file, line_number, raw_sha256,
	            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens, request_count,
            request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, raw_usage_json,
            imported_at_ms, updated_at_ms
        )
        SELECT
            event_id, dedupe_key, dedupe_strategy,
            channel, provider, model_raw, model_normalized, %s,
            %s, %s, %s, %s, %s, %s, %s, %s,
            timestamp_ms, session_id, %s, %s, project_path, message_id, request_id, source_file, line_number, raw_sha256,
	            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens, %s,
			request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
			recorded_cost_usd,
			NULL,
			imported_at_ms, updated_at_ms
        FROM incoming.usage_events
	    `, selects.modelResolution, selects.sourceAgent, selects.sourceProduct, selects.observabilityLevel, selects.modelIsFallback, selects.sourceTotalTokens, selects.rawInputTokens, selects.tokenAccountingMethod, selects.accountingProfile, selects.sessionPathID, selects.turnID, selects.requestCount)
	insertResult, err := tx.Exec(query)
	if err != nil {
		return result, fmt.Errorf("merge events: %w", err)
	}

	rowsAffected, err := insertResult.RowsAffected()
	if err != nil {
		return result, fmt.Errorf("inspect merge result: %w", err)
	}
	result.Inserted = rowsAffected
	result.Skipped = totalIncoming - rowsAffected
	if err = tx.Commit(); err != nil {
		return result, fmt.Errorf("commit merge transaction: %w", err)
	}
	return result, nil
}

type incomingSelects struct {
	modelResolution       string
	sourceAgent           string
	sourceProduct         string
	observabilityLevel    string
	modelIsFallback       string
	sourceTotalTokens     string
	rawInputTokens        string
	tokenAccountingMethod string
	accountingProfile     string
	requestCount          string
	sessionPathID         string
	turnID                string
}

func incomingCompatibilitySelects(conn sqlQueryer) (incomingSelects, error) {
	has := func(column string) (bool, error) {
		return attachedColumnExists(conn, "incoming", "usage_events", column)
	}
	selects := incomingSelects{
		modelResolution:       "'legacy_unclassified'",
		sourceAgent:           "channel",
		sourceProduct:         "NULL",
		observabilityLevel:    "'unknown'",
		modelIsFallback:       "0",
		sourceTotalTokens:     "NULL",
		rawInputTokens:        "NULL",
		tokenAccountingMethod: "NULL",
		accountingProfile:     "NULL",
		requestCount:          "NULL",
		sessionPathID:         "NULL",
		turnID:                "NULL",
	}
	checks := []struct {
		column string
		set    func()
	}{
		{"source_agent", func() { selects.sourceAgent = "COALESCE(NULLIF(source_agent, ''), channel)" }},
		{"model_resolution", func() { selects.modelResolution = "COALESCE(NULLIF(model_resolution, ''), 'legacy_unclassified')" }},
		{"source_product", func() { selects.sourceProduct = "source_product" }},
		{"observability_level", func() { selects.observabilityLevel = "COALESCE(NULLIF(observability_level, ''), 'unknown')" }},
		{"model_is_fallback", func() { selects.modelIsFallback = "model_is_fallback" }},
		{"source_total_tokens", func() { selects.sourceTotalTokens = "source_total_tokens" }},
		{"raw_input_tokens", func() { selects.rawInputTokens = "raw_input_tokens" }},
		{"token_accounting_method", func() { selects.tokenAccountingMethod = "token_accounting_method" }},
		{"accounting_profile", func() { selects.accountingProfile = "accounting_profile" }},
		{"request_count", func() { selects.requestCount = "request_count" }},
		{"session_path_id", func() { selects.sessionPathID = "session_path_id" }},
		{"turn_id", func() { selects.turnID = "turn_id" }},
	}
	for _, check := range checks {
		exists, err := has(check.column)
		if err != nil {
			return selects, err
		}
		if exists {
			check.set()
		}
	}
	return selects, nil
}

func attachedColumnExists(conn sqlQueryer, schema, table, column string) (bool, error) {
	rows, err := conn.Query(fmt.Sprintf("PRAGMA %s.table_info(%s)", schema, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (d *Database) GetStats() (map[string]interface{}, error) {
	stats := map[string]interface{}{"schema_version": SchemaVersion}

	var count int64
	if err := d.conn.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&count); err != nil {
		return nil, err
	}
	stats["total_events"] = count

	if err := d.conn.QueryRow("SELECT COUNT(*) FROM import_runs").Scan(&count); err != nil {
		return nil, err
	}
	stats["total_import_runs"] = count

	var totalTokens sql.NullInt64
	if err := d.conn.QueryRow("SELECT SUM(total_tokens) FROM usage_events").Scan(&totalTokens); err != nil {
		return nil, err
	}
	stats["total_tokens"] = int64(0)
	if totalTokens.Valid {
		stats["total_tokens"] = totalTokens.Int64
	}

	var totalCost sql.NullFloat64
	if err := d.conn.QueryRow("SELECT SUM(recorded_cost_usd) FROM usage_events").Scan(&totalCost); err != nil {
		return nil, err
	}
	stats["total_recorded_cost_usd"] = 0.0
	if totalCost.Valid {
		stats["total_recorded_cost_usd"] = totalCost.Float64
	}

	return stats, nil
}

// selectRequestCount chooses a source-backed request count independently of
// token/timing winners. Priority is fixed:
// 1) current incoming non-nil value
// 2) exact-row non-nil value
// 3) most recently updated non-nil stored candidate, with event_id tie-break
func selectRequestCount(incoming, exact *model.UsageEvent, stored []*model.UsageEvent) *int64 {
	if incoming != nil && incoming.RequestCount != nil {
		return incoming.RequestCount
	}
	if exact != nil && exact.RequestCount != nil {
		return exact.RequestCount
	}
	var best *model.UsageEvent
	for _, candidate := range stored {
		if candidate == nil || candidate.RequestCount == nil {
			continue
		}
		if best == nil {
			best = candidate
			continue
		}
		if candidate.UpdatedAtMs > best.UpdatedAtMs {
			best = candidate
			continue
		}
		if candidate.UpdatedAtMs == best.UpdatedAtMs && candidate.EventID < best.EventID {
			best = candidate
		}
	}
	if best != nil {
		return best.RequestCount
	}
	return nil
}

func mergeMissingMetadata(target, candidate *model.UsageEvent) bool {
	changed := false
	if target.SourceAgent == "" && candidate.SourceAgent != "" {
		target.SourceAgent = candidate.SourceAgent
		changed = true
	}
	if target.SourceProduct == "" && candidate.SourceProduct != "" {
		target.SourceProduct = candidate.SourceProduct
		changed = true
	} else if shouldCorrectSourceProduct(target, candidate) {
		target.SourceProduct = candidate.SourceProduct
		changed = true
	}
	if (target.ObservabilityLevel == "" || target.ObservabilityLevel == "unknown") && candidate.ObservabilityLevel != "" {
		target.ObservabilityLevel = candidate.ObservabilityLevel
		changed = true
	}
	if target.ModelRaw == candidate.ModelRaw &&
		target.ModelNormalized == candidate.ModelNormalized &&
		target.ModelIsFallback == candidate.ModelIsFallback &&
		modelResolutionRank(candidate.ModelResolution) > modelResolutionRank(target.ModelResolution) {
		target.ModelResolution = candidate.ModelResolution
		changed = true
	}
	if !target.ModelIsFallback && candidate.ModelIsFallback {
		target.ModelIsFallback = true
		changed = true
	}
	// RequestCount is independent of token-bucket equality. Known incoming
	// values may correct or fill the stored count even when token fields differ.
	if candidate.RequestCount != nil && (target.RequestCount == nil || *target.RequestCount != *candidate.RequestCount) {
		target.RequestCount = candidate.RequestCount
		changed = true
	}
	if sameTokenUsage(target, candidate) {
		if target.SourceTotalTokens == nil && candidate.SourceTotalTokens != nil {
			target.SourceTotalTokens = candidate.SourceTotalTokens
			changed = true
		}
		if target.RawInputTokens == nil && candidate.RawInputTokens != nil {
			target.RawInputTokens = candidate.RawInputTokens
			changed = true
		}
		if target.TokenAccountingMethod == "" && candidate.TokenAccountingMethod != "" {
			target.TokenAccountingMethod = candidate.TokenAccountingMethod
			changed = true
		}
		if target.AccountingProfile == "" && candidate.AccountingProfile != "" {
			target.AccountingProfile = candidate.AccountingProfile
			changed = true
		}
	}
	if target.SessionPathID == "" && candidate.SessionPathID != "" {
		target.SessionPathID = candidate.SessionPathID
		changed = true
	}
	if target.TurnID == "" && candidate.TurnID != "" {
		target.TurnID = candidate.TurnID
		changed = true
	}
	if target.ProjectPath == "" && candidate.ProjectPath != "" {
		target.ProjectPath = candidate.ProjectPath
		changed = true
	} else if shouldUpgradeProjectPath(target.ProjectPath, candidate.ProjectPath) {
		target.ProjectPath = candidate.ProjectPath
		changed = true
	}
	if changed && candidate.UpdatedAtMs > 0 {
		target.UpdatedAtMs = candidate.UpdatedAtMs
	}
	return changed
}

func modelResolutionRank(value string) int {
	switch strings.TrimSpace(value) {
	case model.ModelResolutionDirectEvent:
		return 4
	case model.ModelResolutionThreadSettings, model.ModelResolutionTurnContext:
		return 3
	case model.ModelResolutionUnknown:
		return 2
	case model.ModelResolutionLegacyUnclassified, "":
		return 1
	default:
		return 1
	}
}

func sameTokenUsage(left, right *model.UsageEvent) bool {
	return left.InputTokens == right.InputTokens &&
		left.OutputTokens == right.OutputTokens &&
		left.ReasoningTokens == right.ReasoningTokens &&
		left.CacheCreationTokens == right.CacheCreationTokens &&
		left.CacheReadTokens == right.CacheReadTokens &&
		left.TotalTokens == right.TotalTokens
}

func preserveExistingSourceMetadata(target, existing *model.UsageEvent) {
	if existing.SourceAgent != "" {
		target.SourceAgent = existing.SourceAgent
	}
	if existing.SourceProduct != "" && !shouldCorrectSourceProduct(existing, target) {
		target.SourceProduct = existing.SourceProduct
	}
	if existing.ObservabilityLevel != "" && existing.ObservabilityLevel != "unknown" {
		target.ObservabilityLevel = existing.ObservabilityLevel
	} else if target.ObservabilityLevel == "" {
		target.ObservabilityLevel = existing.ObservabilityLevel
	}
	if existing.SessionPathID != "" {
		target.SessionPathID = existing.SessionPathID
	}
	if existing.TurnID != "" {
		target.TurnID = existing.TurnID
	}
	if existing.ProjectPath != "" && !shouldUpgradeProjectPath(existing.ProjectPath, target.ProjectPath) {
		target.ProjectPath = existing.ProjectPath
	}
}

func shouldCorrectSourceProduct(existing, candidate *model.UsageEvent) bool {
	if existing == nil || candidate == nil {
		return false
	}
	if existing.SourceProduct != "open-cowork" || candidate.SourceProduct != "claude-code" {
		return false
	}
	return existing.Channel == "claude" || existing.SourceAgent == "claude" || candidate.Channel == "claude" || candidate.SourceAgent == "claude"
}

func shouldUpgradeProjectPath(existing, candidate string) bool {
	existing = strings.TrimSpace(existing)
	candidate = strings.TrimSpace(candidate)
	if existing == "" || candidate == "" || existing == candidate {
		return false
	}
	return projectPathSpecificity(candidate) > projectPathSpecificity(existing)
}

func projectPathSpecificity(value string) int {
	normalized := filepath.ToSlash(strings.TrimSpace(value))
	if normalized == "" {
		return 0
	}
	if filepath.IsAbs(value) || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, "/") {
		return 3
	}
	if strings.HasPrefix(normalized, "-") {
		return 1
	}
	return 2
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullFloat64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

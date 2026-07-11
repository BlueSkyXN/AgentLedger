package db

import (
	"database/sql"
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

	sourceMatches, err := selectEventsForComparisonBySourceIdentity(tx, ev)
	if err != nil {
		return "", err
	}
	if hasDifferentEventID(sourceMatches, ev.EventID) {
		var status string
		status, err = reconcileSourceIdentityMatches(tx, ev, existing, sourceMatches)
		if err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return status, nil
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
			if err = tx.Commit(); err != nil {
				return "", err
			}
			return "updated", nil
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return "skipped", nil
	}

	ev.ImportedAtMs = existing.ImportedAtMs
	preserveExistingSourceMetadata(ev, existing)
	if err = updateEvent(tx, ev); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return "updated", nil
}

const eventComparisonColumns = `
    event_id, dedupe_key, dedupe_strategy,
    channel, COALESCE(provider, ''), COALESCE(model_raw, ''), COALESCE(model_normalized, ''),
    COALESCE(source_agent, ''), COALESCE(source_product, ''), COALESCE(observability_level, ''), model_is_fallback,
    source_total_tokens, raw_input_tokens, COALESCE(token_accounting_method, ''), COALESCE(accounting_profile, ''),
    timestamp_ms, COALESCE(session_id, ''), COALESCE(session_path_id, ''), COALESCE(turn_id, ''), COALESCE(project_path, ''),
	COALESCE(message_id, ''), COALESCE(request_id, ''), COALESCE(source_file, ''), COALESCE(line_number, 0), COALESCE(raw_sha256, ''),
	input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
	request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
	recorded_cost_usd, imported_at_ms, updated_at_ms
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

	var canonical *model.UsageEvent
	for {
		canonical = buildCanonicalReconciledEvent(incoming, stored)
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
	}

	if len(stored) == 1 && sameEventContent(canonical, stored[0]) {
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

func buildCanonicalReconciledEvent(incoming *model.UsageEvent, stored []*model.UsageEvent) *model.UsageEvent {
	usageWinner := *incoming
	for _, candidate := range stored {
		if isMoreComplete(candidate, &usageWinner) {
			usageWinner = *candidate
		}
	}
	mergeMissingAccountingMetadata(&usageWinner, incoming)
	for _, candidate := range stored {
		mergeMissingAccountingMetadata(&usageWinner, candidate)
	}

	canonical := *incoming
	if len(stored) > 0 {
		sourceMetadataWinner := stored[0]
		for _, candidate := range stored[1:] {
			if isMoreComplete(candidate, sourceMetadataWinner) {
				sourceMetadataWinner = candidate
			}
		}
		preserveExistingSourceMetadata(&canonical, sourceMetadataWinner)
	}
	applyUsageWinner(&canonical, &usageWinner)
	for _, candidate := range stored {
		if canonical.ImportedAtMs <= 0 || (candidate.ImportedAtMs > 0 && candidate.ImportedAtMs < canonical.ImportedAtMs) {
			canonical.ImportedAtMs = candidate.ImportedAtMs
		}
		if candidate.UpdatedAtMs > canonical.UpdatedAtMs {
			canonical.UpdatedAtMs = candidate.UpdatedAtMs
		}
	}

	eventID, strategy := computeEventFingerprint(&canonical)
	canonical.EventID = eventID
	canonical.DedupeKey = eventID
	canonical.DedupeStrategy = string(strategy)
	return &canonical
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
		RawJSON:             ev.RawUsageJSON,
		SourceFile:          ev.SourceFile,
		LineNumber:          ev.LineNumber,
		RawSHA256:           ev.RawSHA256,
	})
}

func sameEventContent(left, right *model.UsageEvent) bool {
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

type eventComparisonScanner interface {
	Scan(dest ...any) error
}

func scanEventForComparison(row eventComparisonScanner) (*model.UsageEvent, error) {
	var ev model.UsageEvent
	var requestStarted, firstToken, completed, totalDuration, ttft, outputDuration sql.NullInt64
	var sourceTotal, rawInput sql.NullInt64
	var outputTPS, recordedCost sql.NullFloat64
	var lineNumber int64
	var modelIsFallback int
	if err := row.Scan(
		&ev.EventID,
		&ev.DedupeKey,
		&ev.DedupeStrategy,
		&ev.Channel,
		&ev.Provider,
		&ev.ModelRaw,
		&ev.ModelNormalized,
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
		&requestStarted,
		&firstToken,
		&completed,
		&totalDuration,
		&ttft,
		&outputDuration,
		&outputTPS,
		&recordedCost,
		&ev.ImportedAtMs,
		&ev.UpdatedAtMs,
	); err != nil {
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
            channel, provider, model_raw, model_normalized,
            source_agent, source_product, observability_level, model_is_fallback, source_total_tokens, raw_input_tokens, token_accounting_method, accounting_profile,
            timestamp_ms, session_id, session_path_id, turn_id, project_path, message_id, request_id, source_file, line_number, raw_sha256,
            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
            request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, raw_usage_json,
            imported_at_ms, updated_at_ms
        ) VALUES (
            ?, ?, ?,
            ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?,
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
		ev.Channel, ev.Provider, ev.ModelRaw, ev.ModelNormalized,
		nullIfEmpty(ev.SourceAgent), nullIfEmpty(ev.SourceProduct), nullIfEmpty(ev.ObservabilityLevel), boolToInt(ev.ModelIsFallback), nullableInt64(ev.SourceTotalTokens), nullableInt64(ev.RawInputTokens), nullIfEmpty(ev.TokenAccountingMethod), nullIfEmpty(ev.AccountingProfile),
		ev.TimestampMs, ev.SessionID, ev.SessionPathID, ev.TurnID, ev.ProjectPath, ev.MessageID, ev.RequestID, ev.SourceFile, ev.LineNumber, ev.RawSHA256,
		ev.InputTokens, ev.OutputTokens, ev.ReasoningTokens, ev.CacheCreationTokens, ev.CacheReadTokens, ev.TotalTokens,
		ev.RequestStartedAtMs, ev.FirstTokenAtMs, ev.CompletedAtMs, ev.TotalDurationMs, ev.TTFTMs, ev.OutputDurationMs, ev.OutputTPS,
		ev.RecordedCostUSD, ev.RawUsageJSON,
		ev.UpdatedAtMs,
		existingEventID,
	}
	_, err := tx.Exec(`
        UPDATE usage_events SET
            event_id = ?, dedupe_key = ?, dedupe_strategy = ?,
            channel = ?, provider = ?, model_raw = ?, model_normalized = ?,
            source_agent = ?, source_product = ?, observability_level = ?, model_is_fallback = ?, source_total_tokens = ?, raw_input_tokens = ?, token_accounting_method = ?, accounting_profile = ?,
            timestamp_ms = ?, session_id = ?, session_path_id = ?, turn_id = ?, project_path = ?, message_id = ?, request_id = ?, source_file = ?, line_number = ?, raw_sha256 = ?,
            input_tokens = ?, output_tokens = ?, reasoning_tokens = ?, cache_creation_tokens = ?, cache_read_tokens = ?, total_tokens = ?,
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
            source_total_tokens = ?,
            raw_input_tokens = ?,
            token_accounting_method = ?,
            accounting_profile = ?,
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
		nullableInt64(ev.SourceTotalTokens),
		nullableInt64(ev.RawInputTokens),
		nullIfEmpty(ev.TokenAccountingMethod),
		nullIfEmpty(ev.AccountingProfile),
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

func eventArgs(ev *model.UsageEvent) []any {
	return []any{
		ev.EventID, ev.DedupeKey, ev.DedupeStrategy,
		ev.Channel, ev.Provider, ev.ModelRaw, ev.ModelNormalized,
		nullIfEmpty(ev.SourceAgent), nullIfEmpty(ev.SourceProduct), nullIfEmpty(ev.ObservabilityLevel), boolToInt(ev.ModelIsFallback), nullableInt64(ev.SourceTotalTokens), nullableInt64(ev.RawInputTokens), nullIfEmpty(ev.TokenAccountingMethod), nullIfEmpty(ev.AccountingProfile),
		ev.TimestampMs, ev.SessionID, ev.SessionPathID, ev.TurnID, ev.ProjectPath, ev.MessageID, ev.RequestID, ev.SourceFile, ev.LineNumber, ev.RawSHA256,
		ev.InputTokens, ev.OutputTokens, ev.ReasoningTokens, ev.CacheCreationTokens, ev.CacheReadTokens, ev.TotalTokens,
		ev.RequestStartedAtMs, ev.FirstTokenAtMs, ev.CompletedAtMs, ev.TotalDurationMs, ev.TTFTMs, ev.OutputDurationMs, ev.OutputTPS,
		ev.RecordedCostUSD, ev.RawUsageJSON,
		ev.ImportedAtMs, ev.UpdatedAtMs,
	}
}

// MergeFrom attaches another v2 .aldb database and imports unseen events.
func (d *Database) MergeFrom(incomingPath string) (inserted int64, skipped int64, err error) {
	absPath, err := filepath.Abs(incomingPath)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot access file: %w", err)
	}
	if info.IsDir() {
		return 0, 0, fmt.Errorf("path is a directory, not a database file")
	}

	f, err := os.Open(absPath)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot open file: %w", err)
	}
	header := make([]byte, 16)
	_, err = f.Read(header)
	_ = f.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		return 0, 0, fmt.Errorf("file is not a valid SQLite database")
	}

	escapedPath := strings.ReplaceAll(absPath, "'", "''")
	if _, err = d.conn.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS incoming", escapedPath)); err != nil {
		return 0, 0, fmt.Errorf("failed to attach incoming database: %w", err)
	}
	defer func() {
		_, _ = d.conn.Exec("DETACH DATABASE incoming")
	}()

	var version string
	if err = d.conn.QueryRow(`SELECT value FROM incoming.meta WHERE key='schema_version'`).Scan(&version); err != nil {
		return 0, 0, fmt.Errorf("incoming database missing schema metadata: %w", err)
	}
	if version != SchemaVersion {
		return 0, 0, fmt.Errorf("incoming database schema version %s is not compatible with AgentLedger v2", version)
	}

	var totalIncoming int64
	if err = d.conn.QueryRow("SELECT COUNT(*) FROM incoming.usage_events").Scan(&totalIncoming); err != nil {
		return 0, 0, fmt.Errorf("failed to count incoming events: %w", err)
	}

	selects, err := incomingCompatibilitySelects(d.conn)
	if err != nil {
		return 0, 0, err
	}
	query := fmt.Sprintf(`
        INSERT OR IGNORE INTO usage_events (
            event_id, dedupe_key, dedupe_strategy,
            channel, provider, model_raw, model_normalized,
            source_agent, source_product, observability_level, model_is_fallback, source_total_tokens, raw_input_tokens, token_accounting_method, accounting_profile,
            timestamp_ms, session_id, session_path_id, turn_id, project_path, message_id, request_id, source_file, line_number, raw_sha256,
            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
            request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, raw_usage_json,
            imported_at_ms, updated_at_ms
        )
        SELECT
            event_id, dedupe_key, dedupe_strategy,
            channel, provider, model_raw, model_normalized,
            %s, %s, %s, %s, %s, %s, %s, %s,
            timestamp_ms, session_id, %s, %s, project_path, message_id, request_id, source_file, line_number, raw_sha256,
            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
            request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, raw_usage_json,
            imported_at_ms, updated_at_ms
        FROM incoming.usage_events
    `, selects.sourceAgent, selects.sourceProduct, selects.observabilityLevel, selects.modelIsFallback, selects.sourceTotalTokens, selects.rawInputTokens, selects.tokenAccountingMethod, selects.accountingProfile, selects.sessionPathID, selects.turnID)
	result, err := d.conn.Exec(query)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to merge events: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, totalIncoming - rowsAffected, nil
}

type incomingSelects struct {
	sourceAgent           string
	sourceProduct         string
	observabilityLevel    string
	modelIsFallback       string
	sourceTotalTokens     string
	rawInputTokens        string
	tokenAccountingMethod string
	accountingProfile     string
	sessionPathID         string
	turnID                string
}

func incomingCompatibilitySelects(conn *sql.DB) (incomingSelects, error) {
	has := func(column string) (bool, error) {
		return attachedColumnExists(conn, "incoming", "usage_events", column)
	}
	selects := incomingSelects{
		sourceAgent:           "channel",
		sourceProduct:         "NULL",
		observabilityLevel:    "'unknown'",
		modelIsFallback:       "0",
		sourceTotalTokens:     "NULL",
		rawInputTokens:        "NULL",
		tokenAccountingMethod: "NULL",
		accountingProfile:     "NULL",
		sessionPathID:         "NULL",
		turnID:                "NULL",
	}
	checks := []struct {
		column string
		set    func()
	}{
		{"source_agent", func() { selects.sourceAgent = "COALESCE(NULLIF(source_agent, ''), channel)" }},
		{"source_product", func() { selects.sourceProduct = "source_product" }},
		{"observability_level", func() { selects.observabilityLevel = "COALESCE(NULLIF(observability_level, ''), 'unknown')" }},
		{"model_is_fallback", func() { selects.modelIsFallback = "model_is_fallback" }},
		{"source_total_tokens", func() { selects.sourceTotalTokens = "source_total_tokens" }},
		{"raw_input_tokens", func() { selects.rawInputTokens = "raw_input_tokens" }},
		{"token_accounting_method", func() { selects.tokenAccountingMethod = "token_accounting_method" }},
		{"accounting_profile", func() { selects.accountingProfile = "accounting_profile" }},
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

func attachedColumnExists(conn *sql.DB, schema, table, column string) (bool, error) {
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
	if !target.ModelIsFallback && candidate.ModelIsFallback {
		target.ModelIsFallback = true
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

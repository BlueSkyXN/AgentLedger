package db

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const (
	ReconcileInserted = "inserted"
	ReconcileUpdated  = "updated"
	ReconcileSkipped  = "skipped"
	ReconcileRejected = "rejected"
)

type ImportRun struct {
	ID             string `json:"id"`
	StartedAtMs    int64  `json:"started_at_ms"`
	FinishedAtMs   int64  `json:"finished_at_ms"`
	Status         string `json:"status"`
	FilesScanned   int    `json:"files_scanned"`
	EventsAdded    int    `json:"events_added"`
	EventsUpdated  int    `json:"events_updated"`
	EventsSkipped  int    `json:"events_skipped"`
	EventsRejected int    `json:"events_rejected"`
	Error          string `json:"error,omitempty"`
}

// RejectError describes an event-level incompatibility. It contains a stable
// code and deliberately omits source paths, session IDs, token values, and raw
// source content so it is safe to include in import warnings.
type RejectError struct {
	Code string `json:"code"`
}

func (e *RejectError) Error() string {
	return "event rejected: " + e.Code
}

func IsRejectError(err error) bool {
	var rejected *RejectError
	return errors.As(err, &rejected)
}

func reject(code string) error {
	return &RejectError{Code: code}
}

func (d *Database) StartImportRun(runID string) error {
	_, err := d.conn.Exec(`
        INSERT INTO import_runs (id, started_at_ms, status)
        VALUES (?, ?, 'running')
    `, runID, time.Now().UnixMilli())
	return err
}

func (d *Database) FinishImportRun(runID string, filesScanned, eventsAdded, eventsUpdated, eventsSkipped, eventsRejected int) error {
	return d.FinishImportRunWithStatus(runID, filesScanned, eventsAdded, eventsUpdated, eventsSkipped, eventsRejected, "completed", "")
}

func (d *Database) FinishImportRunWithStatus(runID string, filesScanned, eventsAdded, eventsUpdated, eventsSkipped, eventsRejected int, status, warningText string) error {
	if status == "" {
		status = "completed"
	}
	_, err := d.conn.Exec(`
        UPDATE import_runs SET
            finished_at_ms = ?, status = ?, files_scanned = ?,
            events_added = ?, events_updated = ?, events_skipped = ?, events_rejected = ?, error = ?
        WHERE id = ?
    `, time.Now().UnixMilli(), status, filesScanned, eventsAdded, eventsUpdated, eventsSkipped, eventsRejected, nullIfEmpty(warningText), runID)
	return err
}

// UpsertEvent applies the same deterministic reconcile contract used by merge.
// A rejected event is never written and returns status "rejected" plus a
// *RejectError so import can count it and continue the rest of the run.
func (d *Database) UpsertEvent(event *model.UsageEvent) (status string, err error) {
	incoming := normalizedEvent(event)
	if err := ValidateEvent(incoming); err != nil {
		return ReconcileRejected, err
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	existing, err := selectEvent(tx, incoming.EventID)
	if errors.Is(err, sql.ErrNoRows) {
		existing = nil
		err = nil
	}
	if err != nil {
		return "", err
	}

	canonical, status, decisionErr := reconcile(existing, incoming)
	if decisionErr != nil {
		_ = tx.Rollback()
		return ReconcileRejected, decisionErr
	}
	switch status {
	case ReconcileInserted:
		err = insertEvent(tx, canonical)
	case ReconcileUpdated:
		err = updateEvent(tx, canonical)
	case ReconcileSkipped:
		// Exact duplicates are intentionally zero-write.
	default:
		err = fmt.Errorf("unexpected reconcile status %q", status)
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return status, nil
}

// ValidateEvent enforces source-backed facts before SQLite constraints are hit.
// Accounting relationships are profile-specific because cache and reasoning
// buckets overlap differently across agent products.
func ValidateEvent(event *model.UsageEvent) error {
	if event == nil {
		return reject("nil_event")
	}
	if strings.TrimSpace(event.EventID) == "" || event.IdentityVersion != model.IdentityVersion ||
		strings.TrimSpace(event.IdentityStrategy) == "" || strings.TrimSpace(event.IdentityScope) == "" ||
		strings.TrimSpace(event.ContentSHA256) == "" {
		return reject("invalid_identity")
	}
	if !validIdentityStrategy(event.IdentityStrategy) {
		return reject("unsupported_identity_strategy")
	}
	if event.TimestampMs <= 0 {
		return reject("invalid_timestamp")
	}
	if strings.TrimSpace(event.SessionKey) == "" {
		return reject("missing_session")
	}
	if strings.TrimSpace(event.Channel) == "" || strings.TrimSpace(event.SourceProduct) == "" {
		return reject("missing_source")
	}
	if strings.TrimSpace(event.EventGranularity) == "" {
		return reject("missing_granularity")
	}
	for _, value := range []int64{
		event.InputTokens, event.OutputTokens, event.ReasoningTokens,
		event.CacheCreationTokens, event.CacheReadTokens, event.TotalTokens,
	} {
		if value < 0 {
			return reject("negative_token")
		}
	}
	if event.SourceTotalTokens != nil && *event.SourceTotalTokens < 0 {
		return reject("negative_source_total")
	}
	if event.RawInputTokens != nil && *event.RawInputTokens < 0 {
		return reject("negative_raw_input")
	}
	if event.TotalTokens < maxTokenBucket(event) {
		return reject("accounting_total_below_bucket")
	}
	if err := validateAccountingProfile(event); err != nil {
		return err
	}
	return nil
}

func validIdentityStrategy(value string) bool {
	switch value {
	case "native_event", "native_message", "native_request", "session_turn", "session_record", "content_fallback":
		return true
	default:
		return false
	}
}

func maxTokenBucket(event *model.UsageEvent) int64 {
	maximum := int64(0)
	for _, value := range []int64{event.InputTokens, event.OutputTokens, event.ReasoningTokens, event.CacheCreationTokens, event.CacheReadTokens} {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func validateAccountingProfile(event *model.UsageEvent) error {
	var expected int64
	strict := true
	switch event.TokenAccountingMethod {
	case model.AccClaudeUsageSum:
		expected = event.InputTokens + event.OutputTokens + event.ReasoningTokens + event.CacheCreationTokens + event.CacheReadTokens
	case model.AccCodexLastTokenUsage, model.AccCodexTotalDelta, model.AccCodexHeadlessUsage:
		expected = event.InputTokens + event.CacheCreationTokens + event.CacheReadTokens + max64(event.OutputTokens, event.ReasoningTokens)
		if expected > event.TotalTokens {
			return reject("accounting_total_mismatch")
		}
		if expected < event.TotalTokens && event.ObservabilityLevel != "partial" {
			return reject("accounting_total_mismatch")
		}
		strict = false
	case model.AccCopilotOtelParts, model.AccCopilotSessionMetrics:
		expected = event.InputTokens + event.OutputTokens + event.ReasoningTokens + event.CacheCreationTokens + event.CacheReadTokens
	case model.AccCopilotOtelTotalFallback:
		strict = false
	case model.AccGeminiUsage:
		expected = event.InputTokens + event.OutputTokens + event.ReasoningTokens + event.CacheCreationTokens + event.CacheReadTokens
	case model.AccWorkBuddyRawUsage:
		if event.ReasoningTokens > event.OutputTokens {
			return reject("accounting_reasoning_exceeds_output")
		}
		expected = event.InputTokens + event.OutputTokens + event.CacheCreationTokens + event.CacheReadTokens
	default:
		strict = false
	}
	if strict && event.TotalTokens != expected {
		return reject("accounting_total_mismatch")
	}
	return nil
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func normalizedEvent(event *model.UsageEvent) *model.UsageEvent {
	if event == nil {
		return nil
	}
	copy := *event
	copy.EventID = strings.TrimSpace(copy.EventID)
	copy.IdentityStrategy = strings.TrimSpace(copy.IdentityStrategy)
	copy.IdentityScope = strings.TrimSpace(copy.IdentityScope)
	copy.ContentSHA256 = strings.TrimSpace(copy.ContentSHA256)
	copy.Channel = strings.TrimSpace(copy.Channel)
	copy.SourceProduct = strings.TrimSpace(copy.SourceProduct)
	copy.Provider = strings.TrimSpace(copy.Provider)
	copy.ModelRaw = strings.TrimSpace(copy.ModelRaw)
	copy.ModelNormalized = strings.TrimSpace(copy.ModelNormalized)
	copy.ModelResolution = strings.TrimSpace(copy.ModelResolution)
	copy.SessionKey = strings.TrimSpace(copy.SessionKey)
	if copy.IdentityVersion == 0 {
		copy.IdentityVersion = model.IdentityVersion
	}
	if copy.ModelNormalized == "" {
		copy.ModelNormalized = "unknown"
	}
	if copy.ModelResolution == "" && strings.EqualFold(copy.ModelNormalized, "unknown") {
		copy.ModelResolution = model.ModelResolutionUnknown
		copy.ModelIsFallback = true
	}
	now := time.Now().UnixMilli()
	if copy.ImportedAtMs <= 0 {
		copy.ImportedAtMs = now
	}
	if copy.UpdatedAtMs <= 0 {
		copy.UpdatedAtMs = now
	}
	return &copy
}

func reconcile(existing, incoming *model.UsageEvent) (*model.UsageEvent, string, error) {
	if existing == nil {
		return incoming, ReconcileInserted, nil
	}
	if existing.EventID != incoming.EventID || existing.IdentityVersion != incoming.IdentityVersion ||
		existing.IdentityStrategy != incoming.IdentityStrategy || existing.IdentityScope != incoming.IdentityScope {
		return nil, ReconcileRejected, reject("identity_conflict")
	}
	if existing.TimestampMs != incoming.TimestampMs {
		return nil, ReconcileRejected, reject("timestamp_conflict")
	}
	if existing.SessionKey != incoming.SessionKey || knownConflict(existing.SessionID, incoming.SessionID) || knownConflict(existing.SessionPathID, incoming.SessionPathID) {
		return nil, ReconcileRejected, reject("session_conflict")
	}
	if existing.Channel != incoming.Channel || existing.SourceProduct != incoming.SourceProduct {
		return nil, ReconcileRejected, reject("source_conflict")
	}
	if existing.EventGranularity != incoming.EventGranularity {
		return nil, ReconcileRejected, reject("granularity_conflict")
	}
	if !sameTokenUsage(existing, incoming) || optionalIntConflict(existing.SourceTotalTokens, incoming.SourceTotalTokens) || optionalIntConflict(existing.RawInputTokens, incoming.RawInputTokens) {
		return nil, ReconcileRejected, reject("token_conflict")
	}
	if knownConflict(existing.TokenAccountingMethod, incoming.TokenAccountingMethod) || knownConflict(existing.AccountingProfile, incoming.AccountingProfile) {
		return nil, ReconcileRejected, reject("accounting_conflict")
	}
	if knownConflict(existing.Provider, incoming.Provider) {
		return nil, ReconcileRejected, reject("provider_conflict")
	}

	existingModelRank := modelEvidenceRank(existing)
	incomingModelRank := modelEvidenceRank(incoming)
	if existingModelRank == 2 && incomingModelRank == 2 && !strings.EqualFold(existing.ModelNormalized, incoming.ModelNormalized) {
		return nil, ReconcileRejected, reject("direct_model_conflict")
	}
	if existing.ContentSHA256 == incoming.ContentSHA256 {
		return existing, ReconcileSkipped, nil
	}

	canonical := *existing
	contentChanged := false
	metadataChanged := false
	if incomingModelRank > existingModelRank {
		canonical.ModelRaw = incoming.ModelRaw
		canonical.ModelNormalized = incoming.ModelNormalized
		canonical.ModelResolution = incoming.ModelResolution
		canonical.ModelIsFallback = incoming.ModelIsFallback
		contentChanged = true
	} else if incomingModelRank == existingModelRank && strings.EqualFold(canonical.ModelNormalized, incoming.ModelNormalized) {
		contentChanged = fillString(&canonical.ModelRaw, incoming.ModelRaw) || contentChanged
		contentChanged = fillString(&canonical.ModelResolution, incoming.ModelResolution) || contentChanged
	}
	contentChanged = fillKnownString(&canonical.Provider, incoming.Provider) || contentChanged
	contentChanged = fillString(&canonical.TokenAccountingMethod, incoming.TokenAccountingMethod) || contentChanged
	contentChanged = fillString(&canonical.AccountingProfile, incoming.AccountingProfile) || contentChanged
	contentChanged = fillOptionalInt(&canonical.SourceTotalTokens, incoming.SourceTotalTokens) || contentChanged
	contentChanged = fillOptionalInt(&canonical.RawInputTokens, incoming.RawInputTokens) || contentChanged
	contentChanged = fillString(&canonical.ObservabilityLevel, incoming.ObservabilityLevel) || contentChanged

	metadataChanged = fillString(&canonical.ParserVersion, incoming.ParserVersion) || metadataChanged
	metadataChanged = fillString(&canonical.SessionID, incoming.SessionID) || metadataChanged
	metadataChanged = fillString(&canonical.SessionPathID, incoming.SessionPathID) || metadataChanged
	metadataChanged = fillString(&canonical.TurnID, incoming.TurnID) || metadataChanged
	metadataChanged = fillString(&canonical.ProjectPath, incoming.ProjectPath) || metadataChanged
	metadataChanged = fillString(&canonical.MessageID, incoming.MessageID) || metadataChanged
	metadataChanged = fillString(&canonical.RequestID, incoming.RequestID) || metadataChanged
	metadataChanged = fillString(&canonical.SourceFile, incoming.SourceFile) || metadataChanged
	metadataChanged = fillString(&canonical.RawSHA256, incoming.RawSHA256) || metadataChanged
	if canonical.LineNumber == 0 && incoming.LineNumber > 0 {
		canonical.LineNumber = incoming.LineNumber
		metadataChanged = true
	}

	if !contentChanged && !metadataChanged {
		// A weaker model observation cannot downgrade stronger direct evidence.
		if incomingModelRank < existingModelRank {
			return existing, ReconcileSkipped, nil
		}
		return nil, ReconcileRejected, reject("content_conflict")
	}
	canonical.ImportedAtMs = existing.ImportedAtMs
	canonical.UpdatedAtMs = max64(incoming.UpdatedAtMs, time.Now().UnixMilli())
	if contentChanged {
		canonical.ContentSHA256 = incoming.ContentSHA256
	}
	return &canonical, ReconcileUpdated, nil
}

func modelEvidenceRank(event *model.UsageEvent) int {
	modelID := strings.TrimSpace(event.ModelNormalized)
	if modelID == "" || strings.EqualFold(modelID, "unknown") || event.ModelIsFallback || strings.EqualFold(event.ModelResolution, model.ModelResolutionUnknown) {
		return 0
	}
	if event.ModelResolution == model.ModelResolutionDirectEvent {
		return 2
	}
	return 1
}

func knownConflict(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" || strings.EqualFold(left, "unknown") || strings.EqualFold(right, "unknown") {
		return false
	}
	return !strings.EqualFold(left, right)
}

func optionalIntConflict(left, right *int64) bool {
	return left != nil && right != nil && *left != *right
}

func sameTokenUsage(left, right *model.UsageEvent) bool {
	return left.InputTokens == right.InputTokens &&
		left.OutputTokens == right.OutputTokens &&
		left.ReasoningTokens == right.ReasoningTokens &&
		left.CacheCreationTokens == right.CacheCreationTokens &&
		left.CacheReadTokens == right.CacheReadTokens &&
		left.TotalTokens == right.TotalTokens
}

func fillString(target *string, candidate string) bool {
	if strings.TrimSpace(*target) != "" || strings.TrimSpace(candidate) == "" {
		return false
	}
	*target = candidate
	return true
}

func fillKnownString(target *string, candidate string) bool {
	if !strings.EqualFold(strings.TrimSpace(*target), "unknown") && strings.TrimSpace(*target) != "" {
		return false
	}
	if strings.TrimSpace(candidate) == "" || strings.EqualFold(strings.TrimSpace(candidate), "unknown") {
		return false
	}
	*target = candidate
	return true
}

func fillOptionalInt(target **int64, candidate *int64) bool {
	if *target != nil || candidate == nil {
		return false
	}
	value := *candidate
	*target = &value
	return true
}

const eventColumns = `
    event_id, identity_version, identity_strategy, identity_scope, content_sha256,
    COALESCE(parser_version, ''), event_granularity,
    channel, source_product, COALESCE(provider, ''),
    COALESCE(model_raw, ''), model_normalized, COALESCE(model_resolution, ''), model_is_fallback,
    timestamp_ms, session_key, COALESCE(session_id, ''), COALESCE(session_path_id, ''),
    COALESCE(turn_id, ''), COALESCE(project_path, ''), COALESCE(message_id, ''), COALESCE(request_id, ''),
    COALESCE(source_file, ''), COALESCE(line_number, 0), COALESCE(raw_sha256, ''),
    input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
    source_total_tokens, raw_input_tokens, COALESCE(token_accounting_method, ''),
    COALESCE(accounting_profile, ''), COALESCE(observability_level, ''), imported_at_ms, updated_at_ms
`

type eventScanner interface {
	Scan(dest ...any) error
}

func scanEvent(scanner eventScanner) (*model.UsageEvent, error) {
	event := &model.UsageEvent{}
	var fallback int
	var sourceTotal, rawInput sql.NullInt64
	err := scanner.Scan(
		&event.EventID, &event.IdentityVersion, &event.IdentityStrategy, &event.IdentityScope, &event.ContentSHA256,
		&event.ParserVersion, &event.EventGranularity,
		&event.Channel, &event.SourceProduct, &event.Provider,
		&event.ModelRaw, &event.ModelNormalized, &event.ModelResolution, &fallback,
		&event.TimestampMs, &event.SessionKey, &event.SessionID, &event.SessionPathID,
		&event.TurnID, &event.ProjectPath, &event.MessageID, &event.RequestID,
		&event.SourceFile, &event.LineNumber, &event.RawSHA256,
		&event.InputTokens, &event.OutputTokens, &event.ReasoningTokens, &event.CacheCreationTokens, &event.CacheReadTokens, &event.TotalTokens,
		&sourceTotal, &rawInput, &event.TokenAccountingMethod,
		&event.AccountingProfile, &event.ObservabilityLevel, &event.ImportedAtMs, &event.UpdatedAtMs,
	)
	if err != nil {
		return nil, err
	}
	event.ModelIsFallback = fallback != 0
	event.SourceTotalTokens = nullInt64Ptr(sourceTotal)
	event.RawInputTokens = nullInt64Ptr(rawInput)
	return event, nil
}

func selectEvent(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, eventID string) (*model.UsageEvent, error) {
	return scanEvent(queryer.QueryRow(`SELECT `+eventColumns+` FROM usage_events WHERE event_id = ?`, eventID))
}

func listEvents(queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}) ([]*model.UsageEvent, error) {
	rows, err := queryer.Query(`SELECT ` + eventColumns + ` FROM usage_events ORDER BY event_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]*model.UsageEvent, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func insertEvent(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, event *model.UsageEvent) error {
	_, err := exec.Exec(`
        INSERT INTO usage_events (
            event_id, identity_version, identity_strategy, identity_scope, content_sha256,
            parser_version, event_granularity, channel, source_product, provider,
            model_raw, model_normalized, model_resolution, model_is_fallback,
            timestamp_ms, session_key, session_id, session_path_id, turn_id, project_path,
            message_id, request_id, source_file, line_number, raw_sha256,
            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
            source_total_tokens, raw_input_tokens, token_accounting_method, accounting_profile, observability_level,
            imported_at_ms, updated_at_ms
        ) VALUES (`+placeholders(38)+`)
    `, eventArgs(event)...)
	return err
}

func updateEvent(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, event *model.UsageEvent) error {
	args := eventArgs(event)
	args = append(args[1:], event.EventID)
	_, err := exec.Exec(`
        UPDATE usage_events SET
            identity_version=?, identity_strategy=?, identity_scope=?, content_sha256=?,
            parser_version=?, event_granularity=?, channel=?, source_product=?, provider=?,
            model_raw=?, model_normalized=?, model_resolution=?, model_is_fallback=?,
            timestamp_ms=?, session_key=?, session_id=?, session_path_id=?, turn_id=?, project_path=?,
            message_id=?, request_id=?, source_file=?, line_number=?, raw_sha256=?,
            input_tokens=?, output_tokens=?, reasoning_tokens=?, cache_creation_tokens=?, cache_read_tokens=?, total_tokens=?,
            source_total_tokens=?, raw_input_tokens=?, token_accounting_method=?, accounting_profile=?, observability_level=?,
            imported_at_ms=?, updated_at_ms=?
        WHERE event_id=?
    `, args...)
	return err
}

func eventArgs(event *model.UsageEvent) []any {
	return []any{
		event.EventID, event.IdentityVersion, event.IdentityStrategy, event.IdentityScope, event.ContentSHA256,
		nullIfEmpty(event.ParserVersion), event.EventGranularity, event.Channel, event.SourceProduct, nullIfEmpty(event.Provider),
		nullIfEmpty(event.ModelRaw), event.ModelNormalized, nullIfEmpty(event.ModelResolution), boolToInt(event.ModelIsFallback),
		event.TimestampMs, event.SessionKey, nullIfEmpty(event.SessionID), nullIfEmpty(event.SessionPathID), nullIfEmpty(event.TurnID), nullIfEmpty(event.ProjectPath),
		nullIfEmpty(event.MessageID), nullIfEmpty(event.RequestID), nullIfEmpty(event.SourceFile), nullableLine(event.LineNumber), nullIfEmpty(event.RawSHA256),
		event.InputTokens, event.OutputTokens, event.ReasoningTokens, event.CacheCreationTokens, event.CacheReadTokens, event.TotalTokens,
		nullableInt64(event.SourceTotalTokens), nullableInt64(event.RawInputTokens), nullIfEmpty(event.TokenAccountingMethod), nullIfEmpty(event.AccountingProfile), nullIfEmpty(event.ObservabilityLevel),
		event.ImportedAtMs, event.UpdatedAtMs,
	}
}

func placeholders(count int) string {
	items := make([]string, count)
	for index := range items {
		items[index] = "?"
	}
	return strings.Join(items, ",")
}

type MergeConflict struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type MergeResult struct {
	Added     int             `json:"inserted"`
	Updated   int             `json:"updated"`
	Skipped   int             `json:"skipped"`
	Rejected  int             `json:"rejected"`
	Conflicts []MergeConflict `json:"conflicts,omitempty"`
}

type MergeConflictError struct {
	Conflicts []MergeConflict
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("merge preflight rejected %d incompatible events", conflictTotal(e.Conflicts))
}

func conflictTotal(conflicts []MergeConflict) int {
	total := 0
	for _, conflict := range conflicts {
		total += conflict.Count
	}
	return total
}

type mergeAction struct {
	status string
	event  *model.UsageEvent
}

// MergeFrom accepts only v3/identity-v2 databases. It performs the complete
// reconcile preflight before its first destination write; any conflict rolls
// back the transaction and leaves destination events unchanged.
func (d *Database) MergeFrom(incomingPath string) (result MergeResult, err error) {
	destinationAbs, err := filepath.Abs(d.path)
	if err != nil {
		return result, err
	}
	incomingAbs, err := filepath.Abs(incomingPath)
	if err != nil {
		return result, err
	}
	if destinationAbs == incomingAbs {
		return result, fmt.Errorf("cannot merge database into itself")
	}

	incomingDB, err := OpenReadOnlyV3(incomingPath)
	if err != nil {
		return result, fmt.Errorf("incoming database is not AgentLedger v3 identity v2: %w", err)
	}
	defer incomingDB.Close()
	incomingEvents, err := listEvents(incomingDB.conn)
	if err != nil {
		return result, err
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	existingEvents, err := listEvents(tx)
	if err != nil {
		return result, err
	}
	virtual := make(map[string]*model.UsageEvent, len(existingEvents)+len(incomingEvents))
	for _, event := range existingEvents {
		virtual[event.EventID] = event
	}
	actions := make([]mergeAction, 0, len(incomingEvents))
	conflictCounts := make(map[string]int)
	for _, sourceEvent := range incomingEvents {
		incoming := normalizedEvent(sourceEvent)
		if validationErr := ValidateEvent(incoming); validationErr != nil {
			var rejected *RejectError
			if errors.As(validationErr, &rejected) {
				conflictCounts[rejected.Code]++
			} else {
				conflictCounts["invalid_event"]++
			}
			continue
		}
		canonical, status, decisionErr := reconcile(virtual[incoming.EventID], incoming)
		if decisionErr != nil {
			var rejected *RejectError
			if errors.As(decisionErr, &rejected) {
				conflictCounts[rejected.Code]++
			} else {
				conflictCounts["reconcile_error"]++
			}
			continue
		}
		actions = append(actions, mergeAction{status: status, event: canonical})
		virtual[incoming.EventID] = canonical
	}
	if len(conflictCounts) > 0 {
		result.Conflicts = sortedConflicts(conflictCounts)
		result.Rejected = conflictTotal(result.Conflicts)
		_ = tx.Rollback()
		return result, &MergeConflictError{Conflicts: result.Conflicts}
	}

	for _, action := range actions {
		switch action.status {
		case ReconcileInserted:
			if err = insertEvent(tx, action.event); err != nil {
				return result, err
			}
			result.Added++
		case ReconcileUpdated:
			if err = updateEvent(tx, action.event); err != nil {
				return result, err
			}
			result.Updated++
		case ReconcileSkipped:
			result.Skipped++
		}
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func sortedConflicts(counts map[string]int) []MergeConflict {
	keys := make([]string, 0, len(counts))
	for code := range counts {
		keys = append(keys, code)
	}
	slicesSort(keys)
	conflicts := make([]MergeConflict, 0, len(keys))
	for _, code := range keys {
		conflicts = append(conflicts, MergeConflict{Code: code, Count: counts[code]})
	}
	return conflicts
}

func slicesSort(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func (d *Database) GetStats() (map[string]interface{}, error) {
	stats := map[string]interface{}{
		"schema_version":   SchemaVersion,
		"identity_version": IdentityVersion,
	}
	queries := []struct {
		key   string
		query string
	}{
		{"total_events", `SELECT COUNT(*) FROM usage_events`},
		{"total_sessions", `SELECT COUNT(DISTINCT session_key) FROM usage_events`},
		{"total_import_runs", `SELECT COUNT(*) FROM import_runs`},
		{"total_tokens", `SELECT COALESCE(SUM(total_tokens), 0) FROM usage_events`},
	}
	for _, item := range queries {
		var value int64
		if err := d.conn.QueryRow(item.query).Scan(&value); err != nil {
			return nil, err
		}
		stats[item.key] = value
	}
	return stats, nil
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

func nullableLine(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

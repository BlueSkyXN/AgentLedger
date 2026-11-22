package adapters

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"

	// The read-only source connection uses the stock sqlite3 driver registered
	// by the mattn package; AgentLedger's own database driver stays in internal/db.
	_ "github.com/mattn/go-sqlite3"
)

const (
	antigravityAdapterName   = "antigravity"
	antigravityParserVersion = "antigravity-v1"
	antigravityAccounting    = "antigravity_model_usage_v1"
	antigravitySourceProduct = "antigravity"
	antigravityConversations = "conversations"
	antigravityTimestampRank = 3
	antigravityFallbackRank  = 1
)

// antigravityDefaultPaths mirrors the conversation roots the Antigravity
// product family writes (IDE, CLI, backup, ACP and the XDG variant).
var antigravityDefaultPaths = []string{
	"~/.gemini/antigravity/conversations",
	"~/.gemini/antigravity-cli/conversations",
	"~/.gemini/antigravity-ide/conversations",
	"~/.gemini/antigravity-backup/conversations",
	"~/.gemini/antigravity-acp/conversations",
	"~/.config/antigravity/conversations",
}

// AntigravityAdapter imports per-request usage from local Antigravity (AGY)
// conversation SQLite databases. Only the structured usage facts inside
// gen_metadata/steps protobuf blobs are read; context payloads that may embed
// private paths or conversation bodies are never decoded or persisted.
type AntigravityAdapter struct {
	skipped map[string]int64
}

func NewAntigravityAdapter() *AntigravityAdapter {
	return &AntigravityAdapter{skipped: make(map[string]int64)}
}

func (a *AntigravityAdapter) Name() string { return antigravityAdapterName }

// Discover collects conversation databases below the configured roots. A root
// that contains a conversations/ child scans only that child, matching the
// source layout; a root without one is treated as the conversation directory
// itself.
func (a *AntigravityAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = antigravityDefaultPaths
	}
	files := make([]string, 0)
	seen := make(map[string]struct{})
	for _, configuredPath := range paths {
		base := expandHome(configuredPath)
		info, err := os.Stat(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat Antigravity source %s: %w", base, err)
		}
		if !info.IsDir() {
			// An explicitly configured file is accepted regardless of its name.
			files = appendAntigravityPath(files, seen, base)
			continue
		}
		if nested := filepath.Join(base, antigravityConversations); dirExists(nested) {
			base = nested
		}
		err = filepath.Walk(base, func(path string, walkInfo os.FileInfo, err error) error {
			if err != nil || walkInfo.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Ext(path), ".db") {
				files = appendAntigravityPath(files, seen, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk Antigravity source %s: %w", base, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// appendAntigravityPath deduplicates by canonical path (symlinks resolved) so
// overlapping roots cannot import one database twice.
func appendAntigravityPath(files []string, seen map[string]struct{}, path string) []string {
	canonical := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		canonical = resolved
	}
	canonical = filepath.Clean(canonical)
	if _, ok := seen[canonical]; ok {
		return files
	}
	seen[canonical] = struct{}{}
	return append(files, filepath.Clean(path))
}

func (a *AntigravityAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	records, _, err := a.ParseFileWithWarnings(path)
	return records, err
}

func (a *AntigravityAdapter) ParseFileWithWarnings(path string) ([]*fingerprint.ParsedRecord, []string, error) {
	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil, fmt.Errorf("unnamed Antigravity conversation database: %s", path)
	}

	conn, err := openAntigravityReadOnly(path)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	conversationDB, err := validateAntigravitySchema(conn)
	if err != nil {
		return nil, nil, err
	}
	if !conversationDB {
		// Databases such as conversation_summaries.db hold no conversation
		// usage; skipping them is not a warning.
		return nil, nil, nil
	}

	trajectoryTimestamp, err := readAntigravityTrajectoryTimestamp(conn)
	if err != nil {
		return nil, nil, err
	}
	generationRows, err := readAntigravityGenerationRows(conn)
	if err != nil {
		return nil, nil, err
	}
	stepRows, err := readAntigravityStepRows(conn)
	if err != nil {
		return nil, nil, err
	}

	events := a.collectAntigravityEvents(sessionID, trajectoryTimestamp, generationRows, stepRows)
	events = deduplicateAntigravityEvents(events)

	records := make([]*fingerprint.ParsedRecord, 0, len(events))
	for _, event := range events {
		if record := antigravityRecordFromEvent(sessionID, path, event); record != nil {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].TimestampMs != records[j].TimestampMs {
			return records[i].TimestampMs < records[j].TimestampMs
		}
		return records[i].NativeEventID+records[i].IdentitySubkey < records[j].NativeEventID+records[j].IdentitySubkey
	})
	return records, nil, nil
}

type antigravityGenerationRow struct {
	idx      int64
	metadata antigravityGeneratorMetadata
}

type antigravityStepRow struct {
	idx      int64
	metadata antigravityStepMetadata
}

// antigravityUsageEvent is one token-bearing usage observation before the
// in-file mirror merge.
type antigravityUsageEvent struct {
	timestampMs     int64
	timestampRank   int
	sessionID       string
	sourceTable     string
	sourceIdx       int64
	model           string
	modelDisplay    string
	provider        uint64
	hasProvider     bool
	inputTokens     uint64
	outputTokens    uint64
	cacheCreation   uint64
	cacheRead       uint64
	reasoningTokens uint64
	totalOutput     uint64
	messageID       string
	messageIDRank   int
	identities      []string
}

func (a *AntigravityAdapter) collectAntigravityEvents(
	sessionID string,
	trajectoryTimestamp int64,
	generationRows []antigravityGenerationRow,
	stepRows []antigravityStepRow,
) []antigravityUsageEvent {
	// identityTimestamps associates every identity of a dated row (fallback
	// labels and all usage ids, retries included) with the earliest timestamp
	// observed for it, so undated mirrors and retries resolve to the request
	// time instead of the session start.
	identityTimestamps := make(map[string]int64)
	seed := func(timestampMs int64, identities []string, usages []*antigravityModelUsage) {
		if timestampMs <= 0 {
			return
		}
		keys := append([]string(nil), identities...)
		for _, usage := range usages {
			if usage != nil {
				keys = append(keys, usage.identityKeys()...)
			}
		}
		for _, key := range keys {
			if known, ok := identityTimestamps[key]; !ok || timestampMs < known {
				identityTimestamps[key] = timestampMs
			}
		}
	}
	for _, row := range stepRows {
		var usages []*antigravityModelUsage
		if row.metadata.usage != nil {
			usages = append(usages, row.metadata.usage)
		}
		for index := range row.metadata.retryUsages {
			usages = append(usages, &row.metadata.retryUsages[index])
		}
		seed(row.metadata.timestampMs, []string{antigravityStepLabel(sessionID, row.idx)}, usages)
	}
	for _, row := range generationRows {
		var usages []*antigravityModelUsage
		if row.metadata.usage != nil {
			usages = append(usages, row.metadata.usage)
		}
		for index := range row.metadata.retryUsages {
			usages = append(usages, &row.metadata.retryUsages[index])
		}
		seed(row.metadata.timestampMs, antigravityGenerationIdentities(sessionID, row.idx, row.metadata.stepIndices), usages)
	}

	// The last generation row with model evidence provides the fallback for
	// step rows without their own model information.
	generationModel := ""
	for index := len(generationRows) - 1; index >= 0 && generationModel == ""; index-- {
		generationModel = antigravityRowModel(generationRows[index].metadata.model, generationRows[index].metadata.modelID, generationRows[index].metadata.hasModelID)
	}

	events := make([]antigravityUsageEvent, 0)
	for _, row := range stepRows {
		model := antigravityRowModel(row.metadata.model, row.metadata.modelID, row.metadata.hasModelID)
		if model == "" {
			model = generationModel
		}
		if row.metadata.usage != nil {
			a.appendAntigravityUsageEvent(&events, identityTimestamps, *row.metadata.usage, antigravityEventContext{
				sessionID:          sessionID,
				sourceTable:        "steps",
				sourceIdx:          row.idx,
				model:              model,
				modelID:            row.metadata.modelID,
				hasModelID:         row.metadata.hasModelID,
				provider:           row.metadata.provider,
				hasProvider:        row.metadata.hasProvider,
				timestampMs:        row.metadata.timestampMs,
				timestampRank:      antigravityTimestampRank,
				fallbackIdentities: []string{antigravityStepLabel(sessionID, row.idx)},
				trajectoryMs:       trajectoryTimestamp,
			})
		}
		for _, retry := range row.metadata.retryUsages {
			a.appendAntigravityUsageEvent(&events, identityTimestamps, retry, antigravityEventContext{
				sessionID:          sessionID,
				sourceTable:        "steps",
				sourceIdx:          row.idx,
				model:              model,
				modelID:            row.metadata.modelID,
				hasModelID:         row.metadata.hasModelID,
				provider:           row.metadata.provider,
				hasProvider:        row.metadata.hasProvider,
				timestampMs:        row.metadata.timestampMs,
				timestampRank:      antigravityTimestampRank,
				fallbackIdentities: nil,
				trajectoryMs:       trajectoryTimestamp,
			})
		}
	}

	currentModel := ""
	for _, row := range generationRows {
		resolved := antigravityRowModel(row.metadata.model, row.metadata.modelID, row.metadata.hasModelID)
		if resolved == "" && row.metadata.usage != nil && row.metadata.usage.hasModelID {
			if fromID, ok := antigravityModelFromID(row.metadata.usage.modelID); ok {
				resolved = fromID
			}
		}
		if resolved != "" {
			currentModel = resolved
		}
		fallbackIdentities := antigravityGenerationIdentities(sessionID, row.idx, row.metadata.stepIndices)
		if row.metadata.usage != nil {
			a.appendAntigravityUsageEvent(&events, identityTimestamps, *row.metadata.usage, antigravityEventContext{
				sessionID:          sessionID,
				sourceTable:        "gen_metadata",
				sourceIdx:          row.idx,
				model:              currentModel,
				modelID:            row.metadata.modelID,
				hasModelID:         row.metadata.hasModelID,
				timestampMs:        row.metadata.timestampMs,
				timestampRank:      antigravityTimestampRank,
				fallbackIdentities: fallbackIdentities,
				trajectoryMs:       trajectoryTimestamp,
			})
		}
		for _, retry := range row.metadata.retryUsages {
			a.appendAntigravityUsageEvent(&events, identityTimestamps, retry, antigravityEventContext{
				sessionID:          sessionID,
				sourceTable:        "gen_metadata",
				sourceIdx:          row.idx,
				model:              currentModel,
				modelID:            row.metadata.modelID,
				hasModelID:         row.metadata.hasModelID,
				timestampMs:        row.metadata.timestampMs,
				timestampRank:      antigravityTimestampRank,
				fallbackIdentities: nil,
				trajectoryMs:       trajectoryTimestamp,
			})
		}
	}
	return events
}

type antigravityEventContext struct {
	sessionID          string
	sourceTable        string
	sourceIdx          int64
	model              string
	modelID            uint64
	hasModelID         bool
	provider           uint64
	hasProvider        bool
	timestampMs        int64
	timestampRank      int
	fallbackIdentities []string
	trajectoryMs       int64
}

func (a *AntigravityAdapter) appendAntigravityUsageEvent(
	events *[]antigravityUsageEvent,
	identityTimestamps map[string]int64,
	usage antigravityModelUsage,
	context antigravityEventContext,
) {
	if !usage.isTokenBearing() {
		a.skipped["zero_usage"]++
		return
	}
	identities := usage.identityKeys()
	if len(identities) == 0 {
		identities = append([]string(nil), context.fallbackIdentities...)
	}
	timestampMs, timestampRank := int64(0), 0
	if context.timestampMs > 0 {
		timestampMs, timestampRank = context.timestampMs, context.timestampRank
	} else {
		for _, identity := range identities {
			if known, ok := identityTimestamps[identity]; ok && (timestampMs == 0 || known < timestampMs) {
				timestampMs, timestampRank = known, antigravityTimestampRank
			}
		}
		if timestampMs == 0 && context.trajectoryMs > 0 {
			timestampMs, timestampRank = context.trajectoryMs, antigravityFallbackRank
		}
	}
	if timestampMs <= 0 {
		a.skipped["missing_timestamp"]++
		return
	}

	// Output adjustment: total output is authoritative; visible output and
	// reasoning are its components and are reconciled when the source reports
	// an inconsistent split.
	totalOutput := maxUint64(usage.totalOutputTokens, addUint64(usage.visibleOutputTokens, usage.reasoningTokens))
	output := maxUint64(usage.visibleOutputTokens, subUint64(totalOutput, usage.reasoningTokens))
	reasoning := maxUint64(usage.reasoningTokens, subUint64(totalOutput, output))
	for _, value := range []uint64{usage.inputTokens, output, usage.cacheCreationTokens, usage.cacheReadTokens, reasoning, totalOutput} {
		if value > math.MaxInt64 {
			a.skipped["invalid_token_magnitude"]++
			return
		}
	}

	// Model evidence: a resolved model id from the usage wins, then the row's
	// display name when it belongs to the same model id, then any id-derived
	// name, then any display name, then the internal default.
	model := antigravityDefaultModelName
	modelDisplay := strings.TrimSpace(context.model)
	if usage.hasModelID {
		if fromID, ok := antigravityModelFromID(usage.modelID); ok && isResolvedAntigravityModel(fromID) {
			model = fromID
		}
	}
	if model == antigravityDefaultModelName && modelDisplay != "" {
		sameModelID := context.hasModelID == usage.hasModelID && context.modelID == usage.modelID
		if normalized, err := normalizeAntigravityModel(modelDisplay); err == nil && sameModelID && isResolvedAntigravityModel(normalized) {
			model = normalized
		}
	}
	if model == antigravityDefaultModelName && usage.hasModelID {
		if fromID, ok := antigravityModelFromID(usage.modelID); ok {
			model = fromID
		}
	}
	if model == antigravityDefaultModelName && modelDisplay != "" {
		if normalized, err := normalizeAntigravityModel(modelDisplay); err == nil {
			model = normalized
		}
	}

	messageID, messageIDRank := usage.preferredMessageID()
	provider, hasProvider := usage.provider, usage.hasProvider
	if !hasProvider && context.hasProvider {
		provider, hasProvider = context.provider, true
	}
	*events = append(*events, antigravityUsageEvent{
		timestampMs:     timestampMs,
		timestampRank:   timestampRank,
		sessionID:       context.sessionID,
		sourceTable:     context.sourceTable,
		sourceIdx:       context.sourceIdx,
		model:           model,
		modelDisplay:    modelDisplay,
		provider:        provider,
		hasProvider:     hasProvider,
		inputTokens:     usage.inputTokens,
		outputTokens:    output,
		cacheCreation:   usage.cacheCreationTokens,
		cacheRead:       usage.cacheReadTokens,
		reasoningTokens: reasoning,
		totalOutput:     totalOutput,
		messageID:       messageID,
		messageIDRank:   messageIDRank,
		identities:      identities,
	})
}

func antigravityStepLabel(sessionID string, idx int64) string {
	return fmt.Sprintf("step:%s:%d", sessionID, idx)
}

func antigravityGenerationIdentities(sessionID string, idx int64, stepIndices []int64) []string {
	if len(stepIndices) == 0 {
		return []string{fmt.Sprintf("gen:%s:%d", sessionID, idx)}
	}
	labels := make([]string, 0, len(stepIndices))
	for _, stepIdx := range stepIndices {
		labels = append(labels, antigravityStepLabel(sessionID, stepIdx))
	}
	return labels
}

// deduplicateAntigravityEvents merges events that share any identity (the
// same request is mirrored across steps and gen_metadata) with max-token
// semantics, keeping the strongest model evidence and best timestamp.
func deduplicateAntigravityEvents(events []antigravityUsageEvent) []antigravityUsageEvent {
	slots := make([]*antigravityUsageEvent, 0, len(events))
	identitySlots := make(map[string]int)
	for index := range events {
		event := events[index]
		matching := make(map[int]struct{})
		for _, identity := range event.identities {
			if slot, ok := identitySlots[identity]; ok {
				matching[slot] = struct{}{}
			}
		}
		ordered := make([]int, 0, len(matching))
		for slot := range matching {
			ordered = append(ordered, slot)
		}
		sort.Ints(ordered)
		if len(ordered) == 0 {
			slot := len(slots)
			for _, identity := range event.identities {
				identitySlots[identity] = slot
			}
			eventCopy := event
			slots = append(slots, &eventCopy)
			continue
		}
		target := ordered[0]
		for _, duplicate := range ordered[1:] {
			mergeAntigravityEvents(slots[target], *slots[duplicate])
			slots[duplicate] = nil
		}
		mergeAntigravityEvents(slots[target], event)
		for _, identity := range slots[target].identities {
			identitySlots[identity] = target
		}
	}
	merged := make([]antigravityUsageEvent, 0, len(slots))
	for _, slot := range slots {
		if slot != nil {
			merged = append(merged, *slot)
		}
	}
	return merged
}

func mergeAntigravityEvents(target *antigravityUsageEvent, duplicate antigravityUsageEvent) {
	target.inputTokens = maxUint64(target.inputTokens, duplicate.inputTokens)
	target.outputTokens = maxUint64(target.outputTokens, duplicate.outputTokens)
	target.cacheCreation = maxUint64(target.cacheCreation, duplicate.cacheCreation)
	target.cacheRead = maxUint64(target.cacheRead, duplicate.cacheRead)
	target.reasoningTokens = maxUint64(target.reasoningTokens, duplicate.reasoningTokens)
	target.totalOutput = maxUint64(
		maxUint64(target.totalOutput, duplicate.totalOutput),
		addUint64(target.outputTokens, target.reasoningTokens),
	)
	if antigravityModelResolution(duplicate.model) > antigravityModelResolution(target.model) {
		target.model = duplicate.model
		target.modelDisplay = duplicate.modelDisplay
	}
	if !target.hasProvider && duplicate.hasProvider {
		target.provider, target.hasProvider = duplicate.provider, true
	}
	useDuplicateTimestamp := duplicate.timestampRank > target.timestampRank ||
		(duplicate.timestampRank == target.timestampRank && duplicate.timestampMs < target.timestampMs)
	if useDuplicateTimestamp {
		target.timestampMs, target.timestampRank = duplicate.timestampMs, duplicate.timestampRank
	}
	if duplicate.messageIDRank > target.messageIDRank {
		target.messageID, target.messageIDRank = duplicate.messageID, duplicate.messageIDRank
	}
	for _, identity := range duplicate.identities {
		found := false
		for _, existing := range target.identities {
			if existing == identity {
				found = true
				break
			}
		}
		if !found {
			target.identities = append(target.identities, identity)
		}
	}
}

func antigravityRecordFromEvent(sessionID, path string, event antigravityUsageEvent) *fingerprint.ParsedRecord {
	totalTokens := addUint64(
		addUint64(event.inputTokens, event.cacheCreation),
		addUint64(event.cacheRead, event.totalOutput),
	)
	if totalTokens > math.MaxInt64 {
		return nil
	}

	modelRaw := event.modelDisplay
	if modelRaw == "" {
		modelRaw = event.model
	}
	modelResolution := model.ModelResolutionDirectEvent
	modelIsFallback := false
	if event.model == antigravityDefaultModelName {
		modelResolution = model.ModelResolutionUnknown
		modelIsFallback = true
	}
	// Provider stays the model-vendor label used across AgentLedger; the
	// source's serving-API enum (Google/Vertex) is not persisted. Without
	// model evidence no vendor is guessed.
	provider := ""
	if !modelIsFallback {
		_, provider, _ = NormalizeModelName(event.model)
	}

	nativeEventID, identityKind, identityScope, identitySubkey := "", "", "", ""
	if event.messageID != "" {
		nativeEventID, identityKind, identityScope = event.messageID, "message", "global"
	} else {
		labels := sortAntigravityIdentities(event.identities)
		subkey := "record"
		if len(labels) > 0 {
			subkey = strings.Replace(labels[0], sessionID+":", "", 1)
		}
		identityKind, identityScope, identitySubkey = "record", "session", subkey
	}

	envelope := map[string]interface{}{
		"session_id":            sessionID,
		"source_table":          event.sourceTable,
		"source_idx":            event.sourceIdx,
		"input_tokens":          event.inputTokens,
		"output_tokens":         event.outputTokens,
		"cache_creation_tokens": event.cacheCreation,
		"cache_read_tokens":     event.cacheRead,
		"reasoning_tokens":      event.reasoningTokens,
		"total_output_tokens":   event.totalOutput,
		"message_id":            event.messageID,
	}
	if event.hasProvider {
		envelope["provider_id"] = event.provider
	}
	rawJSON, err := json.Marshal(envelope)
	if err != nil {
		return nil
	}

	return &fingerprint.ParsedRecord{
		Agent:                 antigravityAdapterName,
		Provider:              provider,
		Model:                 modelRaw,
		ModelNormalized:       event.model,
		ModelResolution:       modelResolution,
		ModelIsFallback:       modelIsFallback,
		NativeSessionID:       sessionID,
		SessionID:             sessionID,
		NativeEventID:         nativeEventID,
		IdentityKind:          identityKind,
		IdentityScope:         identityScope,
		IdentitySubkey:        identitySubkey,
		ParserVersion:         antigravityParserVersion,
		Granularity:           "request",
		TimestampMs:           event.timestampMs,
		MessageID:             event.messageID,
		InputTokens:           int64(event.inputTokens),
		OutputTokens:          int64(event.outputTokens),
		CacheCreationTokens:   int64(event.cacheCreation),
		CacheReadTokens:       int64(event.cacheRead),
		ReasoningTokens:       int64(event.reasoningTokens),
		TotalTokens:           int64(totalTokens),
		SourceProduct:         antigravitySourceProduct,
		ObservabilityLevel:    "full",
		TokenAccountingMethod: model.AccAntigravityModelUsage,
		AccountingProfile:     antigravityAccounting,
		FingerprintJSON:       string(rawJSON),
		SourceFile:            path,
		LineNumber:            int(event.sourceIdx) + 1,
		RawSHA256:             sha256Hex(rawJSON),
	}
}

func openAntigravityReadOnly(path string) (*sql.DB, error) {
	uri := (&url.URL{
		Scheme:   "file",
		Path:     filepath.Clean(path),
		RawQuery: "mode=ro&_busy_timeout=5000",
	}).String()
	conn, err := sql.Open("sqlite3", uri)
	if err != nil {
		return nil, fmt.Errorf("open Antigravity database %s: %w", path, err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("open Antigravity database %s: %w", path, err)
	}
	return conn, nil
}

// validateAntigravitySchema applies the fail-closed contract: a database with
// gen_metadata must expose the expected columns; a database with steps or
// trajectory metadata but no gen_metadata is an unsupported schema; a database
// without any of the three tables (for example conversation_summaries.db) is
// reported as a benign skip.
func validateAntigravitySchema(conn *sql.DB) (bool, error) {
	genExists, err := antigravityTableExists(conn, "gen_metadata")
	if err != nil {
		return false, err
	}
	stepsExists, err := antigravityTableExists(conn, "steps")
	if err != nil {
		return false, err
	}
	trajectoryExists, err := antigravityTableExists(conn, "trajectory_metadata_blob")
	if err != nil {
		return false, err
	}
	if !genExists {
		if stepsExists || trajectoryExists {
			return false, fmt.Errorf("unsupported Antigravity schema: steps/trajectory metadata without gen_metadata")
		}
		return false, nil
	}
	for table, required := range map[string]map[string]bool{
		"gen_metadata":             {"idx": true, "data": true},
		"steps":                    {"idx": true, "metadata": true},
		"trajectory_metadata_blob": {"data": true},
	} {
		if table == "gen_metadata" || (table == "steps" && stepsExists) || (table == "trajectory_metadata_blob" && trajectoryExists) {
			if err := antigravityRequireColumns(conn, table, required); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func antigravityTableExists(conn *sql.DB, table string) (bool, error) {
	rows, err := conn.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table)
	if err != nil {
		return false, fmt.Errorf("inspect Antigravity schema: %w", err)
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

func antigravityRequireColumns(conn *sql.DB, table string, required map[string]bool) error {
	rows, err := conn.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("inspect Antigravity schema table %s: %w", table, err)
	}
	defer rows.Close()
	present := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("inspect Antigravity schema table %s: %w", table, err)
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect Antigravity schema table %s: %w", table, err)
	}
	for column := range required {
		if !present[column] {
			return fmt.Errorf("unsupported Antigravity schema: table %s is missing column %s", table, column)
		}
	}
	return nil
}

func readAntigravityTrajectoryTimestamp(conn *sql.DB) (int64, error) {
	exists, err := antigravityTableExists(conn, "trajectory_metadata_blob")
	if err != nil || !exists {
		return 0, err
	}
	rows, err := conn.Query("SELECT data FROM trajectory_metadata_blob WHERE data IS NOT NULL ORDER BY rowid ASC")
	if err != nil {
		return 0, fmt.Errorf("query Antigravity trajectory metadata: %w", err)
	}
	defer rows.Close()
	timestamp := int64(0)
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return 0, fmt.Errorf("scan Antigravity trajectory metadata: %w", err)
		}
		if candidate, ok := parseAntigravityTrajectoryTimestamp(blob); ok && timestamp == 0 {
			timestamp = candidate
		}
	}
	return timestamp, rows.Err()
}

func readAntigravityGenerationRows(conn *sql.DB) ([]antigravityGenerationRow, error) {
	rows, err := conn.Query("SELECT idx, data FROM gen_metadata WHERE data IS NOT NULL ORDER BY idx ASC")
	if err != nil {
		return nil, fmt.Errorf("query Antigravity gen_metadata: %w", err)
	}
	defer rows.Close()
	var parsed []antigravityGenerationRow
	for rows.Next() {
		var idx int64
		var blob []byte
		if err := rows.Scan(&idx, &blob); err != nil {
			return nil, fmt.Errorf("scan Antigravity gen_metadata row: %w", err)
		}
		metadata, err := parseAntigravityGeneratorMetadata(blob)
		if err != nil {
			return nil, fmt.Errorf("decode Antigravity gen_metadata idx %d: %w", idx, err)
		}
		parsed = append(parsed, antigravityGenerationRow{idx: idx, metadata: metadata})
	}
	return parsed, rows.Err()
}

func readAntigravityStepRows(conn *sql.DB) ([]antigravityStepRow, error) {
	exists, err := antigravityTableExists(conn, "steps")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	rows, err := conn.Query("SELECT idx, metadata FROM steps WHERE metadata IS NOT NULL ORDER BY idx ASC")
	if err != nil {
		return nil, fmt.Errorf("query Antigravity steps: %w", err)
	}
	defer rows.Close()
	var parsed []antigravityStepRow
	for rows.Next() {
		var idx int64
		var blob []byte
		if err := rows.Scan(&idx, &blob); err != nil {
			return nil, fmt.Errorf("scan Antigravity steps row: %w", err)
		}
		metadata, err := parseAntigravityStepMetadata(blob)
		if err != nil {
			return nil, fmt.Errorf("decode Antigravity steps idx %d: %w", idx, err)
		}
		parsed = append(parsed, antigravityStepRow{idx: idx, metadata: metadata})
	}
	return parsed, rows.Err()
}

func (a *AntigravityAdapter) ImportDiagnostics() []ImportDiagnostic {
	diagnostics := make([]ImportDiagnostic, 0, len(a.skipped))
	for reason, count := range a.skipped {
		if count > 0 {
			diagnostics = append(diagnostics, ImportDiagnostic{
				Code:  "antigravity_skipped_" + reason,
				Unit:  ImportDiagnosticUnitCount,
				Count: count,
			})
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].Code < diagnostics[j].Code })
	return diagnostics
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func addUint64(left, right uint64) uint64 {
	sum := left + right
	if sum < left {
		return math.MaxUint64
	}
	return sum
}

func subUint64(left, right uint64) uint64 {
	if left < right {
		return 0
	}
	return left - right
}

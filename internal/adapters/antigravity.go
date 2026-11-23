package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"

	// The read-only source connection uses the stock sqlite3 driver registered
	// by the mattn package; AgentLedger's own database driver stays in internal/db.
	_ "github.com/mattn/go-sqlite3"
)

const (
	antigravityAdapterName   = model.AntigravitySource
	antigravityConversations = "conversations"
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
	skipped     map[string]int64
	batchEvents map[*fingerprint.ParsedRecord]antigravityUsageEvent
}

func NewAntigravityAdapter() *AntigravityAdapter {
	return &AntigravityAdapter{skipped: make(map[string]int64)}
}

func (a *AntigravityAdapter) Name() string { return antigravityAdapterName }

// Keep the observations from valid files until all import sources have been
// parsed. An additional copy may link IDs from two older copies.
func (a *AntigravityAdapter) PrepareFileSet([]string) error {
	a.batchEvents = make(map[*fingerprint.ParsedRecord]antigravityUsageEvent)
	return nil
}

func (a *AntigravityAdapter) PostProcessRecords(records []*fingerprint.ParsedRecord) []*fingerprint.ParsedRecord {
	if a.batchEvents == nil {
		return records
	}
	events := make([]antigravityUsageEvent, 0, len(records))
	for _, record := range records {
		events = append(events, a.batchEvents[record])
	}
	a.batchEvents = nil
	return a.antigravityRecords(a.deduplicateAntigravityEvents(events))
}

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

	// Each SQLite statement would otherwise see its own snapshot, including
	// writes that land in WAL between the generation and steps queries.
	tx, err := conn.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin Antigravity snapshot: %w", err)
	}
	defer tx.Rollback()
	conversationDB, err := validateAntigravitySchema(tx)
	if err != nil {
		return nil, nil, err
	}
	if !conversationDB {
		// Databases such as conversation_summaries.db hold no conversation
		// usage; skipping them is not a warning.
		return nil, nil, nil
	}

	trajectoryTimestamp, err := readAntigravityTrajectoryTimestamp(tx)
	if err != nil {
		return nil, nil, err
	}
	generationRows, err := readAntigravityGenerationRows(tx)
	if err != nil {
		return nil, nil, err
	}
	stepRows, err := readAntigravityStepRows(tx)
	if err != nil {
		return nil, nil, err
	}

	events := a.collectAntigravityEvents(sessionID, path, trajectoryTimestamp, generationRows, stepRows)
	return a.antigravityRecords(a.deduplicateAntigravityEvents(events)), nil, nil
}

func (a *AntigravityAdapter) antigravityRecords(events []antigravityUsageEvent) []*fingerprint.ParsedRecord {
	records := make([]*fingerprint.ParsedRecord, 0, len(events))
	for _, event := range events {
		if event.timestamp.ms == 0 {
			a.skipped["missing_timestamp"]++
			continue
		}
		record := antigravityRecordFromEvent(event)
		if record == nil {
			a.skipped["invalid_token_magnitude"]++
			continue
		}
		if a.batchEvents != nil {
			a.batchEvents[record] = event
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].TimestampMs != records[j].TimestampMs {
			return records[i].TimestampMs < records[j].TimestampMs
		}
		return records[i].NativeEventID+records[i].IdentitySubkey < records[j].NativeEventID+records[j].IdentitySubkey
	})
	return records
}

type antigravityGenerationRow struct {
	idx      int64
	metadata antigravityGeneratorMetadata
}

type antigravityStepRow struct {
	idx      int64
	metadata antigravityStepMetadata
}

type antigravityTime struct {
	ms   int64
	rank model.AntigravityTime
}

func (t antigravityTime) betterThan(other antigravityTime) bool {
	return t.ms > 0 && (other.ms == 0 || t.rank > other.rank || (t.rank == other.rank && t.ms < other.ms))
}

type antigravityRowKey struct {
	session, key string
}

type antigravityUsageEvent struct {
	sessionID     string
	sourceFile    string
	timestamp     antigravityTime
	sourceTable   string
	sourceIdx     int64
	model         string
	modelDisplay  string
	modelRank     int
	inputTokens   int64
	cacheCreation int64
	cacheRead     int64
	output        model.AntigravityOutput
	identities    []string
	rowKeys       []antigravityRowKey
}

type antigravityEventContext struct {
	table      string
	idx        int64
	model      string
	modelID    uint64
	hasModelID bool
	inherited  bool
	timestamp  antigravityTime
}

func (a *AntigravityAdapter) collectAntigravityEvents(
	sessionID, path string,
	trajectoryTimestamp int64,
	generationRows []antigravityGenerationRow,
	stepRows []antigravityStepRow,
) []antigravityUsageEvent {
	stepTimes := make(map[int64]antigravityTime, len(stepRows))
	for _, row := range stepRows {
		stepTimes[row.idx] = antigravityTime{row.metadata.timestampMs, model.AntigravityStepTime}
	}
	generationTimes := make(map[int64]antigravityTime)
	for _, row := range generationRows {
		timestamp := antigravityTime{row.metadata.timestampMs, model.AntigravityGenerationTime}
		for _, step := range row.metadata.stepIndices {
			if timestamp.betterThan(generationTimes[step]) {
				generationTimes[step] = timestamp
			}
		}
	}

	var events []antigravityUsageEvent
	appendRow := func(ctx antigravityEventContext, primary *antigravityModelUsage, retries []antigravityRetryUsage, keys []string) {
		if ctx.timestamp.ms == 0 {
			ctx.timestamp = antigravityTime{trajectoryTimestamp, model.AntigravityTrajectoryTime}
		}
		appendUsage := func(usage antigravityModelUsage, rowKeys []string) {
			event, ok := a.antigravityUsageEvent(usage, ctx)
			if !ok {
				return
			}
			event.sessionID, event.sourceFile = sessionID, path
			for _, key := range rowKeys {
				event.rowKeys = append(event.rowKeys, antigravityRowKey{sessionID, key})
			}
			events = append(events, event)
		}
		if primary != nil {
			appendUsage(*primary, keys)
		}
		for _, retry := range retries {
			key := fmt.Sprintf("%s:%d:retry:%d", ctx.table, ctx.idx, retry.ordinal)
			appendUsage(retry.usage, []string{key})
		}
	}

	for _, row := range stepRows {
		metadata := row.metadata
		timestamp := stepTimes[row.idx]
		if timestamp.ms == 0 {
			timestamp = generationTimes[row.idx]
		}
		appendRow(antigravityEventContext{
			table: "step", idx: row.idx, timestamp: timestamp,
			model:   antigravityRowModel(metadata.model, metadata.modelID, metadata.hasModelID),
			modelID: metadata.modelID, hasModelID: metadata.hasModelID,
		}, metadata.usage, metadata.retryUsages, []string{antigravityStepLabel(row.idx)})
	}
	currentModel := ""
	for _, row := range generationRows {
		metadata := row.metadata
		timestamp := antigravityTime{metadata.timestampMs, model.AntigravityGenerationTime}
		keys := make([]string, 0, len(metadata.stepIndices)+1)
		for _, step := range metadata.stepIndices {
			keys = append(keys, antigravityStepLabel(step))
			if candidate := stepTimes[step]; candidate.betterThan(timestamp) {
				timestamp = candidate
			}
		}
		keys = append(keys, fmt.Sprintf("gen:%d", row.idx))
		rowModel := antigravityRowModel(metadata.model, metadata.modelID, metadata.hasModelID)
		inherited := rowModel == ""
		if rowModel != "" {
			currentModel = rowModel
		}
		appendRow(antigravityEventContext{
			table: "gen", idx: row.idx, timestamp: timestamp,
			model: currentModel, modelID: metadata.modelID, hasModelID: metadata.hasModelID, inherited: inherited,
		}, metadata.usage, metadata.retryUsages, keys)
	}
	return events
}

func (a *AntigravityAdapter) antigravityUsageEvent(usage antigravityModelUsage, ctx antigravityEventContext) (antigravityUsageEvent, bool) {
	if !usage.isTokenBearing() {
		a.skipped["zero_usage"]++
		return antigravityUsageEvent{}, false
	}
	for _, value := range []uint64{usage.inputTokens, usage.cacheCreationTokens, usage.cacheReadTokens,
		usage.totalOutputTokens, usage.visibleOutputTokens, usage.reasoningTokens} {
		if value > math.MaxInt64 {
			a.skipped["invalid_token_magnitude"]++
			return antigravityUsageEvent{}, false
		}
	}
	output, ok := antigravityOutput(usage)
	if !ok {
		a.skipped["invalid_token_magnitude"]++
		return antigravityUsageEvent{}, false
	}
	event := antigravityUsageEvent{
		timestamp: ctx.timestamp, sourceTable: ctx.table, sourceIdx: ctx.idx,
		inputTokens: int64(usage.inputTokens), cacheCreation: int64(usage.cacheCreationTokens),
		cacheRead: int64(usage.cacheReadTokens), output: output,
		identities: usage.identityKeys(),
		model:      antigravityDefaultModelName, modelDisplay: strings.TrimSpace(ctx.model),
	}
	if name, ok := antigravityModelFromID(usage.modelID); usage.hasModelID && ok {
		event.model = name
		if isResolvedAntigravityModel(name) {
			event.modelRank = 3
			if ctx.inherited || (ctx.hasModelID && ctx.modelID != usage.modelID) {
				event.modelDisplay = ""
			}
			return event, true
		}
	}
	if event.modelDisplay != "" && (!usage.hasModelID || (ctx.hasModelID && ctx.modelID == usage.modelID)) {
		if name, err := normalizeAntigravityModel(event.modelDisplay); err == nil {
			event.model = name
			if isResolvedAntigravityModel(name) {
				event.modelRank = 2
				if ctx.inherited {
					event.modelRank = 1
				}
			}
		}
	}
	return event, true
}

func antigravityOutput(usage antigravityModelUsage) (model.AntigravityOutput, bool) {
	visible, reasoning := int64(usage.visibleOutputTokens), int64(usage.reasoningTokens)
	if !usage.hasTotalOutput {
		_, valid := model.AntigravityTotal(visible, reasoning)
		return model.AntigravityOutput{Visible: visible, Reasoning: reasoning, Evidence: model.AntigravityDerivedOutput}, valid
	}
	total := int64(usage.totalOutputTokens)
	evidence := model.AntigravityReportedOutput
	if visible > 0 && visible <= total && reasoning <= total-visible && visible+reasoning == total {
		evidence = model.AntigravityReportedOutputWithSplit
	} else if reasoning > 0 && reasoning <= total {
		evidence = model.AntigravityReportedOutputWithPart
	} else if visible > 0 && visible <= total {
		evidence = model.AntigravityReportedOutputWithPart
	}
	switch {
	case reasoning > 0 && reasoning <= total:
		// The reported reasoning fits the authoritative output total.
	case visible > 0 && visible <= total:
		reasoning = total - visible
	default:
		reasoning = 0
	}
	return model.AntigravityOutput{Visible: total - reasoning, Reasoning: reasoning, Evidence: evidence}, true
}

func antigravityStepLabel(idx int64) string {
	return fmt.Sprintf("step:%d", idx)
}

func antigravityCompatibleRows(left, right antigravityUsageEvent) bool {
	return (len(left.identities) == 0 || len(right.identities) == 0) &&
		left.inputTokens == right.inputTokens && left.cacheCreation == right.cacheCreation &&
		left.cacheRead == right.cacheRead && left.output.Total() == right.output.Total()
}

// Native links form the primary identity. A row link is evidence for an
// otherwise unkeyed mirror only when the observed usage agrees.
func (a *AntigravityAdapter) deduplicateAntigravityEvents(events []antigravityUsageEvent) []antigravityUsageEvent {
	slots := make([]*antigravityUsageEvent, 0, len(events))
	nativeSlots := make(map[string]int)
	rowSlots := make(map[antigravityRowKey][]int)
	for _, event := range events {
		matches := make(map[int]struct{})
		for _, id := range event.identities {
			if slot, ok := nativeSlots[id]; ok {
				matches[slot] = struct{}{}
			}
		}
		locations := make(map[int]struct{})
		for _, key := range event.rowKeys {
			for _, slot := range rowSlots[key] {
				if existing := slots[slot]; existing != nil && antigravityCompatibleRows(event, *existing) {
					locations[slot] = struct{}{}
				}
			}
		}
		if len(event.identities) == 0 {
			nativeLocations := 0
			for slot := range locations {
				if len(slots[slot].identities) > 0 {
					nativeLocations++
				}
			}
			if nativeLocations > 1 {
				// More than one native attempt fits these row links. Keep
				// the proven attempts and skip this ambiguous mirror.
				a.skipped["ambiguous_row_link"]++
				continue
			}
		}
		for slot := range locations {
			matches[slot] = struct{}{}
		}
		ordered := make([]int, 0, len(matches))
		for slot := range matches {
			ordered = append(ordered, slot)
		}
		sort.Ints(ordered)
		target := len(slots)
		if len(ordered) == 0 {
			slots = append(slots, &event)
		} else {
			target = ordered[0]
			for _, slot := range ordered[1:] {
				mergeAntigravityEvents(slots[target], *slots[slot])
				slots[slot] = nil
			}
			mergeAntigravityEvents(slots[target], event)
		}
		for _, id := range slots[target].identities {
			nativeSlots[id] = target
		}
		for _, key := range slots[target].rowKeys {
			if !slices.Contains(rowSlots[key], target) {
				rowSlots[key] = append(rowSlots[key], target)
			}
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
	target.inputTokens = max(target.inputTokens, duplicate.inputTokens)
	target.cacheCreation = max(target.cacheCreation, duplicate.cacheCreation)
	target.cacheRead = max(target.cacheRead, duplicate.cacheRead)
	if duplicate.output.BetterThan(target.output) {
		target.output = duplicate.output
	}
	if duplicate.modelRank > target.modelRank ||
		(duplicate.modelRank == target.modelRank && antigravityModelResolution(duplicate.model) > antigravityModelResolution(target.model)) {
		target.model, target.modelDisplay, target.modelRank = duplicate.model, duplicate.modelDisplay, duplicate.modelRank
	}
	if duplicate.timestamp.betterThan(target.timestamp) {
		target.timestamp = duplicate.timestamp
	}
	target.identities = uniqueAntigravityKeys(target.identities, duplicate.identities)
	target.rowKeys = uniqueAntigravityKeys(target.rowKeys, duplicate.rowKeys)
}

func uniqueAntigravityKeys[T comparable](existing, incoming []T) []T {
	for _, key := range incoming {
		if !slices.Contains(existing, key) {
			existing = append(existing, key)
		}
	}
	return existing
}

func antigravityRecordFromEvent(event antigravityUsageEvent) *fingerprint.ParsedRecord {
	total, ok := model.AntigravityTotal(event.inputTokens, event.cacheCreation, event.cacheRead, event.output.Total())
	if !ok || total == 0 {
		return nil
	}
	modelRaw := firstNonEmpty(event.modelDisplay, event.model)
	modelResolution := model.ModelResolutionUnknown
	provider := ""
	if event.modelRank > 0 {
		modelResolution = model.ModelResolutionDirectEvent
		if event.modelRank == 1 {
			modelResolution = model.ModelResolutionTurnContext
		}
		_, provider, _ = NormalizeModelName(event.model)
	}
	usageProfile := model.AntigravityAccountingEvidence{
		Time: event.timestamp.rank, Output: event.output.Evidence,
	}.Profile()
	record := &fingerprint.ParsedRecord{
		Agent: antigravityAdapterName, SourceProduct: model.AntigravitySource,
		Provider: provider, Model: modelRaw, ModelNormalized: event.model,
		ModelResolution: modelResolution, ModelIsFallback: event.modelRank == 0,
		NativeSessionID: event.sessionID, SessionID: event.sessionID,
		ParserVersion: model.AntigravityParser, Granularity: "request",
		TimestampMs: event.timestamp.ms, InputTokens: event.inputTokens,
		OutputTokens: event.output.Visible, ReasoningTokens: event.output.Reasoning,
		CacheCreationTokens: event.cacheCreation, CacheReadTokens: event.cacheRead,
		TotalTokens: total, TokenAccountingMethod: model.AccAntigravityModelUsage,
		ObservabilityLevel: "full", AccountingProfile: usageProfile,
		SourceFile: event.sourceFile, LineNumber: int(event.sourceIdx) + 1,
	}
	record.RequestID = setAntigravityRecordIdentity(record, event)
	rawJSON, err := json.Marshal(struct {
		Table         string `json:"source_table"`
		Index         int64  `json:"source_idx"`
		Input         int64  `json:"input_tokens"`
		Output        int64  `json:"total_output_tokens"`
		Reasoning     int64  `json:"reasoning_tokens"`
		CacheCreation int64  `json:"cache_creation_tokens"`
		CacheRead     int64  `json:"cache_read_tokens"`
	}{event.sourceTable, event.sourceIdx, event.inputTokens, event.output.Total(), event.output.Reasoning, event.cacheCreation, event.cacheRead})
	if err != nil {
		return nil
	}
	record.FingerprintJSON = string(rawJSON)
	record.RawSHA256 = sha256Hex(rawJSON)
	return record
}

func setAntigravityRecordIdentity(record *fingerprint.ParsedRecord, event antigravityUsageEvent) string {
	var evidence model.AntigravityRequestIdentity
	rowKeys := slices.Clone(event.rowKeys)
	sort.Slice(rowKeys, func(i, j int) bool { return rowKeys[i].key < rowKeys[j].key })
	record.IdentityKind, record.IdentityScope = "record", "session"
	record.IdentitySubkey = rowKeys[0].key
	for _, key := range rowKeys {
		if strings.HasPrefix(key.key, "step:") {
			record.IdentitySubkey = key.key
			break
		}
	}
	for _, key := range rowKeys {
		evidence.Rows = append(evidence.Rows, fingerprint.AntigravityRecordEventID(key.session, key.key))
		if strings.Contains(key.key, ":retry:") {
			evidence.Legacy = append(evidence.Legacy, fingerprint.AntigravityRecordEventID(key.session, "record"))
		}
	}
	for _, namespace := range []string{"response", "provider", "message"} {
		for _, identity := range event.identities {
			kind, value, _ := strings.Cut(identity, ":")
			if kind != namespace {
				continue
			}
			if record.NativeEventID == "" {
				record.NativeEventID, record.MessageID = value, value
				record.IdentityKind, record.IdentityScope, record.IdentitySubkey = "message", "global", namespace
			}
			evidence.Native = append(evidence.Native, fingerprint.AntigravityNativeEventID(namespace, value))
			evidence.Legacy = append(evidence.Legacy, fingerprint.AntigravityNativeEventID("", value))
		}
	}
	slices.Sort(evidence.Native)
	slices.Sort(evidence.Rows)
	slices.Sort(evidence.Legacy)
	evidence.Legacy = slices.Compact(evidence.Legacy)
	encoded, _ := json.Marshal(evidence)
	return string(encoded)
}

func openAntigravityReadOnly(path string) (*sql.DB, error) {
	uri, err := db.SQLiteFileURI(path, url.Values{
		"mode": {"ro"}, "_busy_timeout": {"5000"}, "_query_only": {"1"},
	})
	if err != nil {
		return nil, fmt.Errorf("open Antigravity database %s: %w", path, err)
	}
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
func validateAntigravitySchema(conn *sql.Tx) (bool, error) {
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

func antigravityTableExists(conn *sql.Tx, table string) (bool, error) {
	rows, err := conn.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table)
	if err != nil {
		return false, fmt.Errorf("inspect Antigravity schema: %w", err)
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

func antigravityRequireColumns(conn *sql.Tx, table string, required map[string]bool) error {
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

func readAntigravityTrajectoryTimestamp(conn *sql.Tx) (int64, error) {
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
		candidate, err := parseAntigravityTrajectoryTimestamp(blob)
		if err != nil {
			return 0, fmt.Errorf("decode Antigravity trajectory metadata: %w", err)
		}
		if timestamp == 0 {
			timestamp = candidate
		}
	}
	return timestamp, rows.Err()
}

func readAntigravityGenerationRows(conn *sql.Tx) ([]antigravityGenerationRow, error) {
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

func readAntigravityStepRows(conn *sql.Tx) ([]antigravityStepRow, error) {
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

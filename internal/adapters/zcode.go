package adapters

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	zcodeDefaultSourceDir    = "~/.zcode/cli/db"
	zcodeDatabaseFileName    = "db.sqlite"
	zcodeAccountingProfile   = "zcode_model_usage_v1"
	zcodeParserVersion       = "zcode-model-usage-v1"
	zcodeSourceProductSuffix = "zcode-cli-db"
)

// zcodeRequiredModelUsageColumns is the fail-closed schema contract. ZCode
// versions that rename or drop any of these columns are rejected instead of
// being imported with guessed semantics.
var zcodeRequiredModelUsageColumns = map[string]bool{
	"id": true, "logical_request_id": true, "session_id": true, "started_at": true,
	"model_id": true, "provider_id": true, "query_source": true, "status": true,
	"input_tokens": true, "output_tokens": true, "reasoning_tokens": true,
	"cache_creation_input_tokens": true, "cache_read_input_tokens": true,
	"provider_total_tokens": true, "computed_total_tokens": true,
}

var zcodeRequiredSessionColumns = map[string]bool{"id": true, "path": true, "directory": true}

// ZCodeAdapter imports per-request model usage from the local ZCode CLI
// SQLite database. It reads only the structured model_usage columns (plus the
// session project path); message bodies, raw_usage_json,
// provider_metadata_json, and model-io rollout logs are never touched.
type ZCodeAdapter struct {
	skippedRows map[string]int64
}

func NewZCodeAdapter() *ZCodeAdapter {
	return &ZCodeAdapter{skippedRows: make(map[string]int64)}
}

func (a *ZCodeAdapter) Name() string { return "zcode" }

func (a *ZCodeAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{zcodeDefaultSourceDir}
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
			return nil, fmt.Errorf("stat ZCode source %s: %w", base, err)
		}
		if !info.IsDir() {
			// An explicitly configured file is accepted regardless of its name.
			cleaned := filepath.Clean(base)
			if _, ok := seen[cleaned]; !ok {
				seen[cleaned] = struct{}{}
				files = append(files, cleaned)
			}
			continue
		}
		err = filepath.Walk(base, func(path string, walkInfo os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if walkInfo.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Base(path), zcodeDatabaseFileName) {
				cleaned := filepath.Clean(path)
				if _, ok := seen[cleaned]; !ok {
					seen[cleaned] = struct{}{}
					files = append(files, cleaned)
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk ZCode source %s: %w", base, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (a *ZCodeAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	records, _, err := a.ParseFileWithWarnings(path)
	return records, err
}

func (a *ZCodeAdapter) ParseFileWithWarnings(path string) ([]*fingerprint.ParsedRecord, []string, error) {
	conn, err := openZCodeReadOnly(path)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	if err := validateZCodeSchema(conn); err != nil {
		return nil, nil, err
	}

	rows, err := conn.Query(`
		SELECT m.id, m.logical_request_id, m.attempt_index, m.session_id,
		       COALESCE(s.path, s.directory) AS project_path,
		       COALESCE(m.turn_id, '') AS turn_id,
		       COALESCE(m.assistant_message_id, '') AS assistant_message_id,
		       m.query_source, m.model_id, m.status, m.started_at,
		       m.input_tokens, m.output_tokens, m.reasoning_tokens,
		       m.cache_creation_input_tokens, m.cache_read_input_tokens,
		       m.provider_total_tokens, m.computed_total_tokens
		FROM model_usage m
		LEFT JOIN session s ON s.id = m.session_id
		ORDER BY m.started_at, m.id`)
	if err != nil {
		return nil, nil, fmt.Errorf("query ZCode model_usage in %s: %w", path, err)
	}
	defer rows.Close()

	records := make([]*fingerprint.ParsedRecord, 0)
	diagnostics := newZCodeParseDiagnostics()
	for rows.Next() {
		var (
			providerTotal    sql.NullInt64
			attemptIndex     sql.NullInt64
			row              zcodeUsageRow
			providerTotalPtr *int64
		)
		if err := rows.Scan(
			&row.id, &row.logicalRequestID, &attemptIndex, &row.sessionID,
			&row.projectPath, &row.turnID, &row.assistantMessageID,
			&row.querySource, &row.modelID, &row.status, &row.startedAt,
			&row.inputTokens, &row.outputTokens, &row.reasoningTokens,
			&row.cacheCreationTokens, &row.cacheReadTokens,
			&providerTotal, &row.computedTotalTokens,
		); err != nil {
			return nil, diagnostics.warnings(), fmt.Errorf("scan ZCode model_usage row in %s: %w", path, err)
		}
		if providerTotal.Valid {
			value := providerTotal.Int64
			providerTotalPtr = &value
		}
		record, reason := zcodeRecordFromRow(row, attemptIndex, providerTotalPtr, path)
		if record != nil {
			records = append(records, record)
		} else if reason != "" {
			diagnostics.add(reason)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, diagnostics.warnings(), fmt.Errorf("iterate ZCode model_usage rows in %s: %w", path, err)
	}
	for reason, count := range diagnostics.counts {
		a.skippedRows[reason] += count
	}
	return records, diagnostics.warnings(), nil
}

type zcodeUsageRow struct {
	id                  string
	logicalRequestID    string
	sessionID           string
	projectPath         string
	turnID              string
	assistantMessageID  string
	querySource         string
	modelID             string
	status              string
	startedAt           int64
	inputTokens         int64
	outputTokens        int64
	reasoningTokens     int64
	cacheCreationTokens int64
	cacheReadTokens     int64
	computedTotalTokens int64
}

func zcodeRecordFromRow(row zcodeUsageRow, attemptIndex sql.NullInt64, providerTotal *int64, path string) (*fingerprint.ParsedRecord, string) {
	if strings.TrimSpace(row.id) == "" || strings.TrimSpace(row.sessionID) == "" ||
		strings.TrimSpace(row.modelID) == "" || row.startedAt <= 0 {
		return nil, "missing_required_fields"
	}

	input, output := row.inputTokens, row.outputTokens
	cacheCreation, cacheRead, reasoning := row.cacheCreationTokens, row.cacheReadTokens, row.reasoningTokens
	total := row.computedTotalTokens
	if providerTotal != nil {
		total = *providerTotal
	}
	if input < 0 || output < 0 || reasoning < 0 || cacheCreation < 0 || cacheRead < 0 || total < 0 {
		return nil, "invalid_token_totals"
	}
	if cacheCreation+cacheRead > input || reasoning > output {
		return nil, "invalid_token_details"
	}
	// Source invariants: cached tokens are part of input_tokens, reasoning is
	// part of output_tokens, and the authoritative totals must agree with the
	// component columns. Rows that violate this are skipped, never repaired.
	if row.computedTotalTokens != input+output {
		return nil, "invalid_token_totals"
	}
	if providerTotal != nil && *providerTotal != row.computedTotalTokens {
		return nil, "invalid_source_total"
	}
	if total == 0 {
		return nil, "zero_usage"
	}

	attempt := int64(0)
	if attemptIndex.Valid {
		attempt = attemptIndex.Int64
	}
	envelope := zcodeUsageEnvelope(row, attempt, providerTotal)
	rawJSON, err := json.Marshal(envelope)
	if err != nil {
		return nil, "raw_envelope_encoding_failed"
	}

	return &fingerprint.ParsedRecord{
		Agent:                 "zcode",
		Provider:              zcodeProviderForModel(row.modelID),
		Model:                 row.modelID,
		NativeSessionID:       row.sessionID,
		NativeEventID:         row.id,
		IdentityKind:          "request",
		IdentityScope:         "session",
		ParserVersion:         zcodeParserVersion,
		Granularity:           "request",
		ModelResolution:       model.ModelResolutionDirectEvent,
		TimestampMs:           normalizeEpoch(row.startedAt),
		SessionID:             row.sessionID,
		ProjectPath:           row.projectPath,
		MessageID:             row.assistantMessageID,
		RequestID:             row.logicalRequestID,
		TurnID:                row.turnID,
		InputTokens:           input - cacheCreation - cacheRead,
		OutputTokens:          output,
		CacheCreationTokens:   cacheCreation,
		CacheReadTokens:       cacheRead,
		ReasoningTokens:       reasoning,
		TotalTokens:           total,
		SourceTotalTokens:     int64Ptr(total),
		RawInputTokens:        int64Ptr(input),
		SourceProduct:         zcodeSourceProductSuffix,
		ObservabilityLevel:    "full",
		TokenAccountingMethod: model.AccZCodeModelUsage,
		AccountingProfile:     zcodeAccountingProfile,
		FingerprintJSON:       string(rawJSON),
		SourceFile:            path,
		RawSHA256:             sha256Hex(rawJSON),
	}, ""
}

// zcodeProviderForModel maps the model family onto the provider labels used by
// the pricing table. ZCode's provider_id column names routing plans/accounts,
// not model vendors, so it is deliberately not used as the event provider.
func zcodeProviderForModel(modelRaw string) string {
	normalized, provider, _ := NormalizeModelName(modelRaw)
	if provider != "unknown" {
		return provider
	}
	lower := strings.ToLower(normalized)
	switch {
	case strings.Contains(lower, "glm"):
		return "zai"
	case strings.Contains(lower, "deepseek"):
		return "deepseek"
	case strings.Contains(lower, "grok"):
		return "xai"
	case strings.Contains(lower, "kimi"):
		return "kimi"
	default:
		return ""
	}
}

// zcodeUsageEnvelope keeps only structured, non-private facts: identifiers,
// classification columns, and token counters. Error text, trace ids, raw
// usage, and provider metadata are intentionally excluded.
func zcodeUsageEnvelope(row zcodeUsageRow, attempt int64, providerTotal *int64) map[string]interface{} {
	envelope := map[string]interface{}{
		"id":                    row.id,
		"logical_request_id":    row.logicalRequestID,
		"attempt_index":         attempt,
		"session_id":            row.sessionID,
		"turn_id":               row.turnID,
		"query_source":          row.querySource,
		"model_id":              row.modelID,
		"status":                row.status,
		"started_at":            row.startedAt,
		"input_tokens":          row.inputTokens,
		"output_tokens":         row.outputTokens,
		"reasoning_tokens":      row.reasoningTokens,
		"cache_creation_tokens": row.cacheCreationTokens,
		"cache_read_tokens":     row.cacheReadTokens,
		"computed_total_tokens": row.computedTotalTokens,
	}
	if providerTotal != nil {
		envelope["provider_total_tokens"] = *providerTotal
	}
	return envelope
}

func openZCodeReadOnly(path string) (*sql.DB, error) {
	uri := (&url.URL{
		Scheme:   "file",
		Path:     filepath.Clean(path),
		RawQuery: "mode=ro&_busy_timeout=5000",
	}).String()
	conn, err := sql.Open("sqlite3", uri)
	if err != nil {
		return nil, fmt.Errorf("open ZCode database %s: %w", path, err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("open ZCode database %s: %w", path, err)
	}
	return conn, nil
}

func validateZCodeSchema(conn *sql.DB) error {
	for table, required := range map[string]map[string]bool{
		"model_usage": zcodeRequiredModelUsageColumns,
		"session":     zcodeRequiredSessionColumns,
	} {
		rows, err := conn.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			return fmt.Errorf("inspect ZCode schema table %s: %w", table, err)
		}
		present := make(map[string]bool)
		for rows.Next() {
			var cid int
			var name, columnType string
			var notNull, pk int
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
				rows.Close()
				return fmt.Errorf("inspect ZCode schema table %s: %w", table, err)
			}
			present[name] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("inspect ZCode schema table %s: %w", table, err)
		}
		rows.Close()
		if len(present) == 0 {
			return fmt.Errorf("unsupported ZCode schema: table %s not found", table)
		}
		for column := range required {
			if !present[column] {
				return fmt.Errorf("unsupported ZCode schema: table %s is missing column %s", table, column)
			}
		}
	}
	return nil
}

type zcodeParseDiagnostics struct {
	total  int
	counts map[string]int64
	order  []string
}

func newZCodeParseDiagnostics() *zcodeParseDiagnostics {
	return &zcodeParseDiagnostics{counts: make(map[string]int64)}
}

func (d *zcodeParseDiagnostics) add(reason string) {
	d.total++
	if _, seen := d.counts[reason]; !seen {
		d.order = append(d.order, reason)
	}
	d.counts[reason]++
}

func (d *zcodeParseDiagnostics) warnings() []string {
	if d.total == 0 {
		return nil
	}
	reasons := append([]string(nil), d.order...)
	sort.Strings(reasons)
	counts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		counts = append(counts, fmt.Sprintf("%s=%d", reason, d.counts[reason]))
	}
	return []string{fmt.Sprintf("skipped %d unusable ZCode model_usage row(s) (%s)", d.total, strings.Join(counts, ", "))}
}

func (a *ZCodeAdapter) ImportDiagnostics() []ImportDiagnostic {
	diagnostics := make([]ImportDiagnostic, 0, len(a.skippedRows))
	for reason, count := range a.skippedRows {
		if count > 0 {
			diagnostics = append(diagnostics, ImportDiagnostic{
				Code:  "zcode_skipped_" + reason,
				Unit:  ImportDiagnosticUnitCount,
				Count: count,
			})
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].Code < diagnostics[j].Code })
	return diagnostics
}

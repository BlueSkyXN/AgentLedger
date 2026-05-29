package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestUpsertEventKeepsMoreCompleteDuplicate(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID:        "event-1",
		DedupeKey:      "event-1",
		DedupeStrategy: "message_id",
		Channel:        "codex",
		SourceAgent:    "codex",
		Provider:       "openai",
		TimestampMs:    1,
		InputTokens:    10,
		OutputTokens:   5,
		TotalTokens:    15,
		ImportedAtMs:   1,
		UpdatedAtMs:    1,
	}
	status, err := database.UpsertEvent(base)
	if err != nil || status != "inserted" {
		t.Fatalf("initial upsert status=%s err=%v", status, err)
	}

	ttft := int64(300)
	tps := 25.0
	candidate := *base
	candidate.ModelRaw = "gpt-5"
	candidate.ModelNormalized = "gpt-5"
	candidate.TotalTokens = 20
	candidate.TTFTMs = &ttft
	candidate.OutputTPS = &tps
	candidate.UpdatedAtMs = 2
	status, err = database.UpsertEvent(&candidate)
	if err != nil || status != "updated" {
		t.Fatalf("complete upsert status=%s err=%v", status, err)
	}

	var modelName string
	var total int64
	var storedTTFT int64
	if err := database.Conn().QueryRow(`SELECT model_normalized, total_tokens, ttft_ms FROM usage_events WHERE event_id='event-1'`).Scan(&modelName, &total, &storedTTFT); err != nil {
		t.Fatalf("select updated event: %v", err)
	}
	if modelName != "gpt-5" || total != 20 || storedTTFT != ttft {
		t.Fatalf("unexpected updated event model=%s total=%d ttft=%d", modelName, total, storedTTFT)
	}

	status, err = database.UpsertEvent(base)
	if err != nil || status != "skipped" {
		t.Fatalf("less complete upsert status=%s err=%v", status, err)
	}
}

func TestUpsertClaudeDuplicateKeepsLargestTokenTotal(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID:        "claude-event",
		DedupeKey:      "claude-event",
		DedupeStrategy: "message_id",
		Channel:        "claude",
		SourceAgent:    "claude",
		SourceProduct:  "claude-code",
		Provider:       "anthropic",
		TimestampMs:    1,
		InputTokens:    100,
		OutputTokens:   50,
		TotalTokens:    150,
		ImportedAtMs:   1,
		UpdatedAtMs:    1,
	}
	if status, err := database.UpsertEvent(base); err != nil || status != "inserted" {
		t.Fatalf("insert base status=%s err=%v", status, err)
	}

	ttft := int64(100)
	lowerButTimed := *base
	lowerButTimed.InputTokens = 10
	lowerButTimed.OutputTokens = 5
	lowerButTimed.TotalTokens = 15
	lowerButTimed.TTFTMs = &ttft
	lowerButTimed.UpdatedAtMs = 2
	if status, err := database.UpsertEvent(&lowerButTimed); err != nil || status != "skipped" {
		t.Fatalf("lower Claude duplicate should be skipped, status=%s err=%v", status, err)
	}

	higher := *base
	higher.InputTokens = 120
	higher.OutputTokens = 60
	higher.TotalTokens = 180
	higher.UpdatedAtMs = 3
	if status, err := database.UpsertEvent(&higher); err != nil || status != "updated" {
		t.Fatalf("higher Claude duplicate should update, status=%s err=%v", status, err)
	}

	var total int64
	var storedTTFT sql.NullInt64
	if err := database.Conn().QueryRow(`SELECT total_tokens, ttft_ms FROM usage_events WHERE event_id='claude-event'`).Scan(&total, &storedTTFT); err != nil {
		t.Fatalf("select: %v", err)
	}
	if total != 180 || storedTTFT.Valid {
		t.Fatalf("expected highest token record without lower timing overwrite, total=%d ttft_valid=%v", total, storedTTFT.Valid)
	}
}

func TestFreshDBCreatesSchemaV2WithSourceColumns(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	version, exists, err := database.schemaVersion()
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if !exists || version != "2" {
		t.Fatalf("expected schema version 2, exists=%v version=%q", exists, version)
	}
	for _, column := range []string{"source_agent", "source_product", "observability_level", "model_is_fallback", "source_total_tokens", "raw_input_tokens", "token_accounting_method", "accounting_profile", "session_path_id", "turn_id"} {
		if ok, err := dbColumnExists(database.Conn(), "usage_events", column); err != nil || !ok {
			t.Fatalf("expected %s column ok=%v err=%v", column, ok, err)
		}
	}
}

func TestV2CompatibilityColumnsBackfillAndIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	createOriginalV2TestDB(t, path)

	database, err := Open(path)
	if err != nil {
		t.Fatalf("open/migrate: %v", err)
	}
	version, _, err := database.schemaVersion()
	if err != nil {
		t.Fatalf("schema version after open: %v", err)
	}
	if version != "2" {
		t.Fatalf("expected version 2, got %s", version)
	}

	var sourceAgent, observability string
	var sourceProduct sql.NullString
	var modelFallback int
	if err := database.Conn().QueryRow(`SELECT source_agent, source_product, observability_level, model_is_fallback FROM usage_events WHERE event_id='legacy-event'`).Scan(&sourceAgent, &sourceProduct, &observability, &modelFallback); err != nil {
		t.Fatalf("select migrated event: %v", err)
	}
	if sourceAgent != "claude" || sourceProduct.Valid || observability != "unknown" || modelFallback != 0 {
		t.Fatalf("unexpected backfill source_agent=%q source_product_valid=%v observability=%q fallback=%d", sourceAgent, sourceProduct.Valid, observability, modelFallback)
	}

	var groupedTotal int64
	if err := database.Conn().QueryRow(`SELECT SUM(total_tokens) FROM usage_events WHERE channel='claude' AND model_normalized='claude-sonnet' AND session_id='legacy-session'`).Scan(&groupedTotal); err != nil {
		t.Fatalf("legacy report-style query failed: %v", err)
	}
	if groupedTotal != 15 {
		t.Fatalf("legacy report-style query total=%d", groupedTotal)
	}
	_ = database.Close()

	database, err = Open(path)
	if err != nil {
		t.Fatalf("second open should be idempotent: %v", err)
	}
	_ = database.Close()
}

func TestUnsupportedSchemaVersionFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO meta (key, value) VALUES ('schema_version', '99')`); err != nil {
		t.Fatalf("create incompatible db: %v", err)
	}
	_ = conn.Close()

	database, err := Open(path)
	if database != nil {
		_ = database.Close()
	}
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("expected incompatible schema error, got %v", err)
	}
}

func TestFinishImportRunWithStatusStoresError(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	if err := database.StartImportRun("run-warning"); err != nil {
		t.Fatalf("start import run: %v", err)
	}
	if err := database.FinishImportRunWithStatus("run-warning", 2, 1, 0, 1, "completed_with_warnings", "parse warning"); err != nil {
		t.Fatalf("finish import run: %v", err)
	}

	var status, errorText string
	var files, added, skipped int
	if err := database.Conn().QueryRow(`SELECT status, files_scanned, events_added, events_skipped, error FROM import_runs WHERE id='run-warning'`).Scan(&status, &files, &added, &skipped, &errorText); err != nil {
		t.Fatalf("select import run: %v", err)
	}
	if status != "completed_with_warnings" || files != 2 || added != 1 || skipped != 1 || errorText != "parse warning" {
		t.Fatalf("unexpected import run status=%s files=%d added=%d skipped=%d error=%q", status, files, added, skipped, errorText)
	}
}

func TestUpsertFillsSourceMetadataOnce(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID:        "event-meta",
		DedupeKey:      "event-meta",
		DedupeStrategy: "message_id",
		Channel:        "codex",
		Provider:       "openai",
		TimestampMs:    1,
		InputTokens:    10,
		OutputTokens:   5,
		TotalTokens:    15,
		ImportedAtMs:   1,
		UpdatedAtMs:    1,
	}
	status, err := database.UpsertEvent(base)
	if err != nil || status != "inserted" {
		t.Fatalf("insert base status=%s err=%v", status, err)
	}

	sourceTotal := int64(15)
	rawInput := int64(20)
	withMetadata := *base
	withMetadata.SourceAgent = "codex"
	withMetadata.SourceProduct = "codex-cli"
	withMetadata.ObservabilityLevel = "full"
	withMetadata.ModelIsFallback = true
	withMetadata.SourceTotalTokens = &sourceTotal
	withMetadata.RawInputTokens = &rawInput
	withMetadata.TokenAccountingMethod = model.AccCodexLastTokenUsage
	withMetadata.AccountingProfile = "ledger"
	withMetadata.SessionPathID = "2026/05/27/rollout-a"
	withMetadata.TurnID = "turn-a"
	withMetadata.UpdatedAtMs = 2
	status, err = database.UpsertEvent(&withMetadata)
	if err != nil || status != "updated" {
		t.Fatalf("metadata fill status=%s err=%v", status, err)
	}

	changed := withMetadata
	changed.SourceProduct = "other-product"
	changed.ObservabilityLevel = "inferred"
	changed.SourceTotalTokens = int64PtrForTest(99)
	changed.RawInputTokens = int64PtrForTest(100)
	changed.TokenAccountingMethod = model.AccCodexTotalDelta
	changed.AccountingProfile = "ccusage_compatible"
	changed.SessionPathID = "2026/05/27/rollout-b"
	changed.TurnID = "turn-b"
	changed.ModelIsFallback = false
	changed.UpdatedAtMs = 3
	status, err = database.UpsertEvent(&changed)
	if err != nil || status != "skipped" {
		t.Fatalf("metadata churn status=%s err=%v", status, err)
	}

	var sourceAgent, sourceProduct, observability, accounting, accountingProfile, sessionPathID, turnID string
	var fallback int
	var storedSourceTotal, storedRawInput int64
	if err := database.Conn().QueryRow(`SELECT source_agent, source_product, observability_level, model_is_fallback, source_total_tokens, raw_input_tokens, token_accounting_method, accounting_profile, session_path_id, turn_id FROM usage_events WHERE event_id='event-meta'`).Scan(&sourceAgent, &sourceProduct, &observability, &fallback, &storedSourceTotal, &storedRawInput, &accounting, &accountingProfile, &sessionPathID, &turnID); err != nil {
		t.Fatalf("select metadata: %v", err)
	}
	if sourceAgent != "codex" || sourceProduct != "codex-cli" || observability != "full" || fallback != 1 || storedSourceTotal != 15 || storedRawInput != 20 || accounting != model.AccCodexLastTokenUsage || accountingProfile != "ledger" || sessionPathID != "2026/05/27/rollout-a" || turnID != "turn-a" {
		t.Fatalf("metadata was not stable: source=%s product=%s obs=%s fallback=%d source_total=%d raw_input=%d accounting=%s profile=%s session_path=%s turn=%s", sourceAgent, sourceProduct, observability, fallback, storedSourceTotal, storedRawInput, accounting, accountingProfile, sessionPathID, turnID)
	}
}

func TestMoreCompleteReplacementPreservesExistingSourceMetadata(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	sourceTotal := int64(15)
	base := &model.UsageEvent{
		EventID:               "event-replace-meta",
		DedupeKey:             "event-replace-meta",
		DedupeStrategy:        "message_id",
		Channel:               "copilot",
		SourceAgent:           "copilot",
		SourceProduct:         "copilot-otel",
		ObservabilityLevel:    "full",
		SourceTotalTokens:     &sourceTotal,
		TokenAccountingMethod: model.AccCopilotOtelParts,
		Provider:              "github",
		TimestampMs:           1,
		InputTokens:           10,
		OutputTokens:          5,
		TotalTokens:           15,
		ImportedAtMs:          1,
		UpdatedAtMs:           1,
	}
	if status, err := database.UpsertEvent(base); err != nil || status != "inserted" {
		t.Fatalf("insert base status=%s err=%v", status, err)
	}

	ttft := int64(100)
	candidate := *base
	candidate.SourceProduct = "wrong"
	candidate.ObservabilityLevel = "inferred"
	candidate.SourceTotalTokens = int64PtrForTest(99)
	candidate.TokenAccountingMethod = model.AccCopilotOtelTotalFallback
	candidate.TotalTokens = 20
	candidate.TTFTMs = &ttft
	candidate.UpdatedAtMs = 2
	if status, err := database.UpsertEvent(&candidate); err != nil || status != "updated" {
		t.Fatalf("replace candidate status=%s err=%v", status, err)
	}

	var sourceProduct, observability, accounting string
	var storedSourceTotal, total int64
	if err := database.Conn().QueryRow(`SELECT source_product, observability_level, source_total_tokens, token_accounting_method, total_tokens FROM usage_events WHERE event_id='event-replace-meta'`).Scan(&sourceProduct, &observability, &storedSourceTotal, &accounting, &total); err != nil {
		t.Fatalf("select replacement: %v", err)
	}
	if sourceProduct != "copilot-otel" || observability != "full" || storedSourceTotal != 15 || accounting != model.AccCopilotOtelParts || total != 20 {
		t.Fatalf("replacement metadata/total mismatch product=%s obs=%s source_total=%d accounting=%s total=%d", sourceProduct, observability, storedSourceTotal, accounting, total)
	}
}

func TestMergeFromPreservesSourceMetadata(t *testing.T) {
	sourceTotal := int64(20)
	event := &model.UsageEvent{
		EventID:               "event-merge",
		DedupeKey:             "event-merge",
		DedupeStrategy:        "message_id",
		Channel:               "copilot",
		SourceAgent:           "copilot",
		SourceProduct:         "copilot-otel",
		ObservabilityLevel:    "inferred",
		SourceTotalTokens:     &sourceTotal,
		TokenAccountingMethod: model.AccCopilotOtelTotalFallback,
		Provider:              "github",
		ModelRaw:              "gpt-4.1",
		ModelNormalized:       "gpt-4.1",
		TimestampMs:           1,
		SessionID:             "session",
		TotalTokens:           20,
		ImportedAtMs:          1,
		UpdatedAtMs:           1,
	}

	incoming, err := Open(filepath.Join(t.TempDir(), "incoming.db"))
	if err != nil {
		t.Fatalf("open incoming: %v", err)
	}
	if status, err := incoming.UpsertEvent(event); err != nil || status != "inserted" {
		t.Fatalf("insert incoming status=%s err=%v", status, err)
	}
	incomingPath := incoming.Path()
	_ = incoming.Close()

	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open destination: %v", err)
	}
	defer database.Close()
	inserted, skipped, err := database.MergeFrom(incomingPath)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if inserted != 1 || skipped != 0 {
		t.Fatalf("unexpected merge inserted=%d skipped=%d", inserted, skipped)
	}

	var sourceProduct, observability, accounting string
	var storedSourceTotal int64
	if err := database.Conn().QueryRow(`SELECT source_product, observability_level, source_total_tokens, token_accounting_method FROM usage_events WHERE event_id='event-merge'`).Scan(&sourceProduct, &observability, &storedSourceTotal, &accounting); err != nil {
		t.Fatalf("select merged event: %v", err)
	}
	if sourceProduct != "copilot-otel" || observability != "inferred" || storedSourceTotal != sourceTotal || accounting != model.AccCopilotOtelTotalFallback {
		t.Fatalf("metadata not preserved product=%s obs=%s source_total=%d accounting=%s", sourceProduct, observability, storedSourceTotal, accounting)
	}
}

func createOriginalV2TestDB(t *testing.T, path string) {
	t.Helper()
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw v2 db: %v", err)
	}
	defer conn.Close()
	_, err = conn.Exec(`
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO meta (key, value) VALUES ('schema_version', '2');
CREATE TABLE import_runs (
    id TEXT PRIMARY KEY,
    started_at_ms INTEGER NOT NULL,
    finished_at_ms INTEGER,
    status TEXT NOT NULL DEFAULT 'running',
    files_scanned INTEGER NOT NULL DEFAULT 0,
    events_added INTEGER NOT NULL DEFAULT 0,
    events_updated INTEGER NOT NULL DEFAULT 0,
    events_skipped INTEGER NOT NULL DEFAULT 0,
    error TEXT
);
CREATE TABLE usage_events (
    event_id TEXT PRIMARY KEY,
    dedupe_key TEXT NOT NULL,
    dedupe_strategy TEXT NOT NULL,
    channel TEXT NOT NULL,
    provider TEXT,
    model_raw TEXT,
    model_normalized TEXT,
    timestamp_ms INTEGER NOT NULL,
    session_id TEXT,
    project_path TEXT,
    message_id TEXT,
    request_id TEXT,
    source_file TEXT,
    line_number INTEGER,
    raw_sha256 TEXT,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    request_started_at_ms INTEGER,
    first_token_at_ms INTEGER,
    completed_at_ms INTEGER,
    total_duration_ms INTEGER,
    ttft_ms INTEGER,
    output_duration_ms INTEGER,
    output_tps REAL,
    recorded_cost_usd REAL,
    raw_usage_json TEXT,
    imported_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);
INSERT INTO usage_events (
    event_id, dedupe_key, dedupe_strategy, channel, provider, model_raw, model_normalized,
    timestamp_ms, session_id, input_tokens, output_tokens, total_tokens, imported_at_ms, updated_at_ms
) VALUES ('legacy-event', 'legacy-event', 'message_id', 'claude', 'anthropic', 'claude-sonnet', 'claude-sonnet', 1, 'legacy-session', 10, 5, 15, 1, 1);
`)
	if err != nil {
		t.Fatalf("create v2 db: %v", err)
	}
}

func dbColumnExists(conn *sql.DB, table, column string) (bool, error) {
	rows, err := conn.Query("PRAGMA table_info(" + table + ")")
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

func int64PtrForTest(value int64) *int64 {
	return &value
}

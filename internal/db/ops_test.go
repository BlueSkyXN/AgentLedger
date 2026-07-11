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

func TestUpsertEventMigratesChangedFingerprintBySourceIdentity(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	legacy := &model.UsageEvent{
		EventID:         "legacy-openai-event",
		DedupeKey:       "legacy-openai-event",
		DedupeStrategy:  "session_token",
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "openai",
		ModelRaw:        "gpt-5",
		ModelNormalized: "gpt-5",
		TimestampMs:     1,
		SessionID:       "session-a",
		SourceFile:      "/Users/test/.codex/sessions/session-a.jsonl",
		LineNumber:      7,
		RawSHA256:       "raw-hash-a",
		InputTokens:     10,
		OutputTokens:    5,
		TotalTokens:     15,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	if status, err := database.UpsertEvent(legacy); err != nil || status != "inserted" {
		t.Fatalf("legacy insert status=%s err=%v", status, err)
	}

	corrected := *legacy
	corrected.EventID = "corrected-event"
	corrected.DedupeKey = corrected.EventID
	corrected.ModelRaw = "gpt-5-codex"
	corrected.ModelNormalized = "gpt-5-codex"
	corrected.UpdatedAtMs = 2
	status, err := database.UpsertEvent(&corrected)
	if err != nil || status != "updated" {
		t.Fatalf("corrected upsert status=%s err=%v", status, err)
	}

	var count int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected source-identity migration to keep one event, got %d", count)
	}

	var eventID, provider string
	var importedAt, updatedAt int64
	if err := database.Conn().QueryRow(`SELECT event_id, provider, imported_at_ms, updated_at_ms FROM usage_events`).Scan(&eventID, &provider, &importedAt, &updatedAt); err != nil {
		t.Fatalf("select migrated event: %v", err)
	}
	if eventID != corrected.EventID || provider != "openai" || importedAt != 1 || updatedAt != 2 {
		t.Fatalf("unexpected migrated event id=%s provider=%s imported=%d updated=%d", eventID, provider, importedAt, updatedAt)
	}
}

func TestUpsertEventSourceIdentityMigrationPreservesMoreCompleteUsage(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ttft := int64(300)
	legacy := &model.UsageEvent{
		EventID:         "legacy-provider-event",
		DedupeKey:       "legacy-provider-event",
		DedupeStrategy:  "session_token",
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "cpa-hfs",
		ModelRaw:        "gpt-5",
		ModelNormalized: "gpt-5",
		TimestampMs:     1,
		SessionID:       "session-a",
		SourceFile:      "/Users/test/.codex/sessions/session-a.jsonl",
		LineNumber:      8,
		RawSHA256:       "raw-hash-b",
		InputTokens:     40,
		OutputTokens:    10,
		TotalTokens:     50,
		TTFTMs:          &ttft,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	if status, err := database.UpsertEvent(legacy); err != nil || status != "inserted" {
		t.Fatalf("legacy insert status=%s err=%v", status, err)
	}

	corrected := *legacy
	corrected.EventID = "corrected-provider-event"
	corrected.DedupeKey = corrected.EventID
	corrected.Provider = "openai"
	corrected.ModelRaw = "gpt-5-codex"
	corrected.ModelNormalized = "gpt-5-codex"
	corrected.InputTokens = 15
	corrected.OutputTokens = 5
	corrected.TotalTokens = 20
	corrected.SourceTotalTokens = int64PtrForTest(20)
	corrected.RawInputTokens = int64PtrForTest(15)
	corrected.TokenAccountingMethod = model.AccCodexTotalDelta
	corrected.AccountingProfile = "ccusage_compatible"
	corrected.TTFTMs = nil
	corrected.UpdatedAtMs = 2
	status, err := database.UpsertEvent(&corrected)
	if err != nil || status != "updated" {
		t.Fatalf("corrected upsert status=%s err=%v", status, err)
	}

	var eventID, provider, modelName string
	var total, storedTTFT int64
	var storedSourceTotal, storedRawInput sql.NullInt64
	var accounting, profile sql.NullString
	if err := database.Conn().QueryRow(`
        SELECT event_id, provider, model_normalized, total_tokens, ttft_ms,
            source_total_tokens, raw_input_tokens, token_accounting_method,
            accounting_profile
        FROM usage_events
    `).Scan(&eventID, &provider, &modelName, &total, &storedTTFT, &storedSourceTotal, &storedRawInput, &accounting, &profile); err != nil {
		t.Fatalf("select migrated event: %v", err)
	}
	if eventID != corrected.EventID || provider != "openai" || modelName != "gpt-5-codex" || total != 50 || storedTTFT != ttft || storedSourceTotal.Valid || storedRawInput.Valid || accounting.Valid || profile.Valid {
		t.Fatalf("unexpected migrated event id=%s provider=%s model=%s total=%d ttft=%d source_total=%v raw_input=%v accounting=%v profile=%v", eventID, provider, modelName, total, storedTTFT, storedSourceTotal, storedRawInput, accounting, profile)
	}
}

func TestUpsertEventReconcilesPreexistingSourceIdentityDuplicates(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ttft := int64(300)
	sourceTotal := int64(50)
	rawInput := int64(40)
	legacy := &model.UsageEvent{
		EventID:               "legacy-provider-event",
		DedupeKey:             "legacy-provider-event",
		DedupeStrategy:        "session_token",
		Channel:               "codex",
		SourceAgent:           "codex",
		SourceProduct:         "codex-cli",
		Provider:              "legacy-provider",
		ModelRaw:              "gpt-5",
		ModelNormalized:       "gpt-5",
		ModelIsFallback:       true,
		SourceTotalTokens:     &sourceTotal,
		RawInputTokens:        &rawInput,
		TokenAccountingMethod: model.AccCodexLastTokenUsage,
		AccountingProfile:     "ledger",
		TimestampMs:           1,
		SessionID:             "session-a",
		SourceFile:            "/synthetic/session-a.jsonl",
		LineNumber:            9,
		RawSHA256:             "raw-hash-collision",
		InputTokens:           40,
		OutputTokens:          10,
		TotalTokens:           50,
		TTFTMs:                &ttft,
		ImportedAtMs:          1,
		UpdatedAtMs:           1,
	}
	if err := insertEvent(database.Conn(), legacy); err != nil {
		t.Fatalf("insert legacy duplicate: %v", err)
	}

	shadowSourceTotal := int64(30)
	shadowRawInput := int64(25)
	shadow := *legacy
	shadow.EventID = "shadow-provider-event"
	shadow.DedupeKey = shadow.EventID
	shadow.SourceTotalTokens = &shadowSourceTotal
	shadow.RawInputTokens = &shadowRawInput
	shadow.InputTokens = 25
	shadow.OutputTokens = 5
	shadow.TotalTokens = 30
	shadow.TTFTMs = nil
	shadow.ImportedAtMs = 2
	shadow.UpdatedAtMs = 5
	if err := insertEvent(database.Conn(), &shadow); err != nil {
		t.Fatalf("insert shadow duplicate: %v", err)
	}

	correctedSourceTotal := int64(20)
	correctedRawInput := int64(15)
	corrected := *legacy
	corrected.EventID = "corrected-provider-event"
	corrected.DedupeKey = corrected.EventID
	corrected.Provider = "openai"
	corrected.ModelRaw = "gpt-5-codex"
	corrected.ModelNormalized = "gpt-5-codex"
	corrected.ModelIsFallback = false
	corrected.SourceTotalTokens = &correctedSourceTotal
	corrected.RawInputTokens = &correctedRawInput
	corrected.TokenAccountingMethod = model.AccCodexTotalDelta
	corrected.InputTokens = 15
	corrected.OutputTokens = 5
	corrected.TotalTokens = 20
	corrected.TTFTMs = nil
	corrected.ImportedAtMs = 3
	corrected.UpdatedAtMs = 3
	if err := insertEvent(database.Conn(), &corrected); err != nil {
		t.Fatalf("insert corrected duplicate: %v", err)
	}

	candidate := corrected
	candidate.UpdatedAtMs = 4
	status, err := database.UpsertEvent(&candidate)
	if err != nil {
		t.Fatalf("reconcile duplicate: %v", err)
	}

	var count int
	if err := database.Conn().QueryRow(`
        SELECT COUNT(*)
        FROM usage_events
        WHERE source_file = ? AND line_number = ? AND raw_sha256 = ?
    `, candidate.SourceFile, candidate.LineNumber, candidate.RawSHA256).Scan(&count); err != nil {
		t.Fatalf("count source duplicates: %v", err)
	}
	if status != "updated" || count != 1 {
		t.Fatalf("expected source duplicate convergence, status=%s rows=%d", status, count)
	}

	var eventID, provider, modelName, accounting, profile string
	var fallback int
	var total, storedTTFT, importedAt, updatedAt, storedSourceTotal, storedRawInput int64
	if err := database.Conn().QueryRow(`
        SELECT event_id, provider, model_normalized, model_is_fallback,
            total_tokens, ttft_ms, imported_at_ms, updated_at_ms,
            source_total_tokens, raw_input_tokens, token_accounting_method,
            accounting_profile
        FROM usage_events
        WHERE source_file = ? AND line_number = ? AND raw_sha256 = ?
    `, candidate.SourceFile, candidate.LineNumber, candidate.RawSHA256).Scan(
		&eventID,
		&provider,
		&modelName,
		&fallback,
		&total,
		&storedTTFT,
		&importedAt,
		&updatedAt,
		&storedSourceTotal,
		&storedRawInput,
		&accounting,
		&profile,
	); err != nil {
		t.Fatalf("select reconciled event: %v", err)
	}
	if eventID != corrected.EventID || provider != "openai" || modelName != "gpt-5-codex" || fallback != 0 || total != 50 || storedTTFT != ttft || importedAt != 1 || updatedAt != 5 || storedSourceTotal != sourceTotal || storedRawInput != rawInput || accounting != model.AccCodexLastTokenUsage || profile != "ledger" {
		t.Fatalf("unexpected reconciled event id=%s provider=%s model=%s fallback=%d total=%d ttft=%d imported=%d updated=%d source_total=%d raw_input=%d accounting=%s profile=%s", eventID, provider, modelName, fallback, total, storedTTFT, importedAt, updatedAt, storedSourceTotal, storedRawInput, accounting, profile)
	}

	status, err = database.UpsertEvent(&candidate)
	if err != nil || status != "skipped" {
		t.Fatalf("second reconciliation should be idempotent, status=%s err=%v", status, err)
	}
}

func TestUpsertEventDoesNotUseAmbiguousSourceIdentityOutsideCodex(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID:         "copilot-row-a",
		DedupeKey:       "copilot-row-a",
		DedupeStrategy:  "message_id",
		Channel:         "copilot",
		SourceAgent:     "copilot",
		SourceProduct:   "copilot-otel",
		Provider:        "github",
		ModelRaw:        "gpt-4.1",
		ModelNormalized: "gpt-4.1",
		TimestampMs:     1,
		SourceFile:      "/synthetic/copilot.jsonl",
		LineNumber:      5,
		RawSHA256:       "shared-outer-envelope",
		InputTokens:     8,
		OutputTokens:    2,
		TotalTokens:     10,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	if err := insertEvent(database.Conn(), base); err != nil {
		t.Fatalf("insert first Copilot event: %v", err)
	}

	second := *base
	second.EventID = "copilot-row-b"
	second.DedupeKey = second.EventID
	second.InputTokens = 16
	second.OutputTokens = 4
	second.TotalTokens = 20
	second.ImportedAtMs = 2
	second.UpdatedAtMs = 2
	if err := insertEvent(database.Conn(), &second); err != nil {
		t.Fatalf("insert second Copilot event: %v", err)
	}

	candidate := *base
	candidate.EventID = "copilot-row-c"
	candidate.DedupeKey = candidate.EventID
	candidate.InputTokens = 24
	candidate.OutputTokens = 6
	candidate.TotalTokens = 30
	candidate.ImportedAtMs = 3
	candidate.UpdatedAtMs = 3
	status, err := database.UpsertEvent(&candidate)
	if err != nil {
		t.Fatalf("upsert Copilot event: %v", err)
	}

	var count int
	if err := database.Conn().QueryRow(`
        SELECT COUNT(*)
        FROM usage_events
        WHERE source_file = ? AND line_number = ? AND raw_sha256 = ?
    `, candidate.SourceFile, candidate.LineNumber, candidate.RawSHA256).Scan(&count); err != nil {
		t.Fatalf("count Copilot events: %v", err)
	}
	if status != "inserted" || count != 3 {
		t.Fatalf("non-Codex source identity must remain ambiguous, status=%s rows=%d", status, count)
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

func TestLessCompleteReplacementDoesNotFillAccountingFromDifferentUsage(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ttft := int64(100)
	base := &model.UsageEvent{
		EventID:         "event-accounting-winner",
		DedupeKey:       "event-accounting-winner",
		DedupeStrategy:  "session_token",
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "openai",
		ModelRaw:        "gpt-5-codex",
		ModelNormalized: "gpt-5-codex",
		TimestampMs:     1,
		InputTokens:     40,
		OutputTokens:    10,
		TotalTokens:     50,
		TTFTMs:          &ttft,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	if status, err := database.UpsertEvent(base); err != nil || status != "inserted" {
		t.Fatalf("insert base status=%s err=%v", status, err)
	}

	candidate := *base
	candidate.InputTokens = 15
	candidate.OutputTokens = 5
	candidate.TotalTokens = 20
	candidate.TTFTMs = nil
	candidate.SourceTotalTokens = int64PtrForTest(20)
	candidate.RawInputTokens = int64PtrForTest(15)
	candidate.TokenAccountingMethod = model.AccCodexTotalDelta
	candidate.AccountingProfile = "ccusage_compatible"
	candidate.UpdatedAtMs = 2
	status, err := database.UpsertEvent(&candidate)
	if err != nil || status != "skipped" {
		t.Fatalf("less complete candidate status=%s err=%v", status, err)
	}

	var sourceTotal, rawInput sql.NullInt64
	var accounting, profile sql.NullString
	if err := database.Conn().QueryRow(`
        SELECT source_total_tokens, raw_input_tokens,
            token_accounting_method, accounting_profile
        FROM usage_events
        WHERE event_id = ?
    `, base.EventID).Scan(&sourceTotal, &rawInput, &accounting, &profile); err != nil {
		t.Fatalf("select accounting metadata: %v", err)
	}
	if sourceTotal.Valid || rawInput.Valid || accounting.Valid || profile.Valid {
		t.Fatalf("different usage leaked candidate accounting metadata source_total=%v raw_input=%v accounting=%v profile=%v", sourceTotal, rawInput, accounting, profile)
	}
}

func TestUpsertCorrectsOpenCoworkSourceProductToClaudeCode(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID:        "claude-cowork-event",
		DedupeKey:      "claude-cowork-event",
		DedupeStrategy: "message_id",
		Channel:        "claude",
		SourceAgent:    "claude",
		SourceProduct:  "open-cowork",
		Provider:       "anthropic",
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

	candidate := *base
	candidate.SourceProduct = "claude-code"
	candidate.UpdatedAtMs = 2
	status, err = database.UpsertEvent(&candidate)
	if err != nil || status != "updated" {
		t.Fatalf("source product correction status=%s err=%v", status, err)
	}

	var sourceProduct string
	if err := database.Conn().QueryRow(`SELECT source_product FROM usage_events WHERE event_id='claude-cowork-event'`).Scan(&sourceProduct); err != nil {
		t.Fatalf("select source product: %v", err)
	}
	if sourceProduct != "claude-code" {
		t.Fatalf("expected claude-code source product, got %q", sourceProduct)
	}
}

func TestUpsertUpgradesProjectPathToMoreSpecificPath(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID:        "claude-project-event",
		DedupeKey:      "claude-project-event",
		DedupeStrategy: "message_id",
		Channel:        "claude",
		SourceAgent:    "claude",
		SourceProduct:  "claude-code",
		Provider:       "anthropic",
		ProjectPath:    "-Users-test-Github-open-cowork",
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

	candidate := *base
	candidate.ProjectPath = "/Users/test/Github/open-cowork"
	candidate.UpdatedAtMs = 2
	status, err = database.UpsertEvent(&candidate)
	if err != nil || status != "updated" {
		t.Fatalf("project path upgrade status=%s err=%v", status, err)
	}

	var projectPath string
	if err := database.Conn().QueryRow(`SELECT project_path FROM usage_events WHERE event_id='claude-project-event'`).Scan(&projectPath); err != nil {
		t.Fatalf("select project path: %v", err)
	}
	if projectPath != "/Users/test/Github/open-cowork" {
		t.Fatalf("expected cwd project path, got %q", projectPath)
	}
}

func TestMoreCompleteReplacementKeepsCandidateUsageMetadata(t *testing.T) {
	for _, tc := range []struct {
		name          string
		changeEventID bool
	}{
		{name: "same event id"},
		{name: "changed event id", changeEventID: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer database.Close()

			sourceTotal := int64(15)
			rawInput := int64(10)
			base := &model.UsageEvent{
				EventID:               "event-replace-meta",
				DedupeKey:             "event-replace-meta",
				DedupeStrategy:        "session_token",
				Channel:               "codex",
				SourceAgent:           "codex",
				SourceProduct:         "codex-cli",
				ObservabilityLevel:    "full",
				ModelRaw:              "fallback-model",
				ModelNormalized:       "fallback-model",
				ModelIsFallback:       true,
				SourceTotalTokens:     &sourceTotal,
				RawInputTokens:        &rawInput,
				TokenAccountingMethod: model.AccCodexLastTokenUsage,
				AccountingProfile:     "ledger",
				Provider:              "openai",
				TimestampMs:           1,
				SourceFile:            "/synthetic/codex.jsonl",
				LineNumber:            4,
				RawSHA256:             "raw-hash-metadata",
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
			candidateSourceTotal := int64(20)
			candidateRawInput := int64(15)
			candidate := *base
			if tc.changeEventID {
				candidate.EventID = "corrected-event-replace-meta"
				candidate.DedupeKey = candidate.EventID
			}
			candidate.SourceProduct = "candidate-product"
			candidate.ObservabilityLevel = "inferred"
			candidate.ModelRaw = "gpt-4.1"
			candidate.ModelNormalized = "gpt-4.1"
			candidate.ModelIsFallback = false
			candidate.SourceTotalTokens = &candidateSourceTotal
			candidate.RawInputTokens = &candidateRawInput
			candidate.TokenAccountingMethod = model.AccCodexTotalDelta
			candidate.AccountingProfile = "ccusage_compatible"
			candidate.InputTokens = 15
			candidate.TotalTokens = 20
			candidate.TTFTMs = &ttft
			candidate.UpdatedAtMs = 2
			if status, err := database.UpsertEvent(&candidate); err != nil || status != "updated" {
				t.Fatalf("replace candidate status=%s err=%v", status, err)
			}

			var sourceProduct, observability, accounting, profile string
			var fallback int
			var storedSourceTotal, storedRawInput, total int64
			if err := database.Conn().QueryRow(`
                    SELECT source_product, observability_level, model_is_fallback,
                        source_total_tokens, raw_input_tokens,
                        token_accounting_method, accounting_profile, total_tokens
                    FROM usage_events
                    WHERE event_id = ?
                `, candidate.EventID).Scan(
				&sourceProduct,
				&observability,
				&fallback,
				&storedSourceTotal,
				&storedRawInput,
				&accounting,
				&profile,
				&total,
			); err != nil {
				t.Fatalf("select replacement: %v", err)
			}
			if sourceProduct != "codex-cli" || observability != "full" || fallback != 0 || storedSourceTotal != candidateSourceTotal || storedRawInput != candidateRawInput || accounting != model.AccCodexTotalDelta || profile != "ccusage_compatible" || total != 20 {
				t.Fatalf("replacement metadata/total mismatch product=%s obs=%s fallback=%d source_total=%d raw_input=%d accounting=%s profile=%s total=%d", sourceProduct, observability, fallback, storedSourceTotal, storedRawInput, accounting, profile, total)
			}
		})
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

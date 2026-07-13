package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
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
	corrected.ModelRaw = "gpt-5-codex"
	corrected.ModelNormalized = "gpt-5-codex"
	corrected.UpdatedAtMs = 2
	setUsageEventFingerprintForTest(&corrected)
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
	setUsageEventFingerprintForTest(&corrected)
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
	expected := corrected
	expected.InputTokens = legacy.InputTokens
	expected.OutputTokens = legacy.OutputTokens
	expected.TotalTokens = legacy.TotalTokens
	expected.TTFTMs = legacy.TTFTMs
	expected.SourceTotalTokens = legacy.SourceTotalTokens
	expected.RawInputTokens = legacy.RawInputTokens
	expected.TokenAccountingMethod = legacy.TokenAccountingMethod
	expected.AccountingProfile = legacy.AccountingProfile
	setUsageEventFingerprintForTest(&expected)
	if eventID != expected.EventID || provider != "openai" || modelName != "gpt-5-codex" || total != 50 || storedTTFT != ttft || storedSourceTotal.Valid || storedRawInput.Valid || accounting.Valid || profile.Valid {
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
	setUsageEventFingerprintForTest(&corrected)
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
	expected := corrected
	expected.InputTokens = legacy.InputTokens
	expected.OutputTokens = legacy.OutputTokens
	expected.TotalTokens = legacy.TotalTokens
	expected.TTFTMs = legacy.TTFTMs
	expected.SourceTotalTokens = legacy.SourceTotalTokens
	expected.RawInputTokens = legacy.RawInputTokens
	expected.TokenAccountingMethod = legacy.TokenAccountingMethod
	expected.AccountingProfile = legacy.AccountingProfile
	setUsageEventFingerprintForTest(&expected)
	if eventID != expected.EventID || provider != "openai" || modelName != "gpt-5-codex" || fallback != 0 || total != 50 || storedTTFT != ttft || importedAt != 1 || updatedAt != 5 || storedSourceTotal != sourceTotal || storedRawInput != rawInput || accounting != model.AccCodexLastTokenUsage || profile != "ledger" {
		t.Fatalf("unexpected reconciled event id=%s provider=%s model=%s fallback=%d total=%d ttft=%d imported=%d updated=%d source_total=%d raw_input=%d accounting=%s profile=%s", eventID, provider, modelName, fallback, total, storedTTFT, importedAt, updatedAt, storedSourceTotal, storedRawInput, accounting, profile)
	}

	status, err = database.UpsertEvent(&candidate)
	if err != nil || status != "skipped" {
		t.Fatalf("second reconciliation should be idempotent, status=%s err=%v", status, err)
	}
}

func TestUpsertEventReconcilesExactMatchOutsideCurrentSourceIdentity(t *testing.T) {
	for _, tc := range []struct {
		name                string
		correctedSourceFile string
		legacyTotal         int64
		correctedTotal      int64
		expectedTotal       int64
	}{
		{
			name:                "redacted corrected row with legacy usage winner",
			correctedSourceFile: "",
			legacyTotal:         50,
			correctedTotal:      20,
			expectedTotal:       50,
		},
		{
			name:                "different corrected path with corrected usage winner",
			correctedSourceFile: "/synthetic/export/session.jsonl",
			legacyTotal:         20,
			correctedTotal:      50,
			expectedTotal:       50,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer database.Close()

			ttft := int64(300)
			legacy := &model.UsageEvent{
				EventID:         "legacy-source-event",
				DedupeKey:       "legacy-source-event",
				DedupeStrategy:  "session_token",
				Channel:         "codex",
				SourceAgent:     "codex",
				SourceProduct:   "codex-cli",
				Provider:        "legacy-provider",
				ModelRaw:        "gpt-5",
				ModelNormalized: "gpt-5",
				TimestampMs:     1,
				SessionID:       "session-source-union",
				SourceFile:      "/synthetic/local/session.jsonl",
				LineNumber:      11,
				RawSHA256:       "source-union-hash",
				RawUsageJSON:    `{"type":"synthetic-token-count"}`,
				InputTokens:     tc.legacyTotal - 10,
				OutputTokens:    10,
				TotalTokens:     tc.legacyTotal,
				ImportedAtMs:    1,
				UpdatedAtMs:     1,
			}
			if tc.legacyTotal == tc.expectedTotal {
				legacy.TTFTMs = &ttft
			}
			if err := insertEvent(database.Conn(), legacy); err != nil {
				t.Fatalf("insert legacy row: %v", err)
			}

			corrected := *legacy
			corrected.Provider = "openai"
			corrected.ModelRaw = "gpt-5-codex"
			corrected.ModelNormalized = "gpt-5-codex"
			corrected.MessageID = "message-source-union"
			corrected.SourceFile = tc.correctedSourceFile
			if corrected.SourceFile == "" {
				corrected.RawUsageJSON = ""
			}
			corrected.InputTokens = tc.correctedTotal - 10
			corrected.OutputTokens = 10
			corrected.TotalTokens = tc.correctedTotal
			corrected.TTFTMs = nil
			if tc.correctedTotal == tc.expectedTotal {
				corrected.TTFTMs = &ttft
			}
			corrected.ImportedAtMs = 2
			corrected.UpdatedAtMs = 2
			setUsageEventFingerprintForTest(&corrected)
			if err := insertEvent(database.Conn(), &corrected); err != nil {
				t.Fatalf("insert corrected row: %v", err)
			}

			candidate := corrected
			candidate.SourceFile = legacy.SourceFile
			candidate.RawUsageJSON = legacy.RawUsageJSON
			candidate.ImportedAtMs = 3
			candidate.UpdatedAtMs = 3
			status, err := database.UpsertEvent(&candidate)
			if err != nil {
				t.Fatalf("reconcile source union: %v", err)
			}

			var count int
			var eventID, sourceFile, rawUsage string
			var total int64
			if err := database.Conn().QueryRow(`
				SELECT COUNT(*), event_id, source_file, raw_usage_json, total_tokens
				FROM usage_events
			`).Scan(&count, &eventID, &sourceFile, &rawUsage, &total); err != nil {
				t.Fatalf("select reconciled row: %v", err)
			}
			if status != "updated" || count != 1 || eventID != candidate.EventID || sourceFile != candidate.SourceFile || rawUsage != candidate.RawUsageJSON || total != tc.expectedTotal {
				t.Fatalf("unexpected reconciliation status=%s rows=%d event_id=%s source=%q raw=%q total=%d", status, count, eventID, sourceFile, rawUsage, total)
			}
		})
	}
}

func TestUpsertEventReconcilesFullyRedactedSourceIdentityDuplicates(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ttft := int64(300)
	legacy := &model.UsageEvent{
		EventID:         "legacy-redacted-event",
		DedupeKey:       "legacy-redacted-event",
		DedupeStrategy:  "session_token",
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "legacy-provider",
		ModelRaw:        "gpt-5",
		ModelNormalized: "gpt-5",
		TimestampMs:     0,
		SessionID:       "session-fully-redacted",
		LineNumber:      15,
		RawSHA256:       "fully-redacted-hash",
		InputTokens:     40,
		OutputTokens:    10,
		TotalTokens:     50,
		TTFTMs:          &ttft,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	if err := insertEvent(database.Conn(), legacy); err != nil {
		t.Fatalf("insert redacted legacy row: %v", err)
	}

	corrected := *legacy
	corrected.Provider = "openai"
	corrected.ModelRaw = "gpt-5-codex"
	corrected.ModelNormalized = "gpt-5-codex"
	corrected.MessageID = "message-fully-redacted"
	corrected.InputTokens = 15
	corrected.OutputTokens = 5
	corrected.TotalTokens = 20
	corrected.TTFTMs = nil
	corrected.ImportedAtMs = 2
	corrected.UpdatedAtMs = 2
	setUsageEventFingerprintForTest(&corrected)
	if err := insertEvent(database.Conn(), &corrected); err != nil {
		t.Fatalf("insert redacted corrected row: %v", err)
	}
	if _, err := database.Conn().Exec(`UPDATE usage_events SET source_file = NULL, raw_usage_json = NULL`); err != nil {
		t.Fatalf("simulate default redacted export: %v", err)
	}

	incoming := corrected
	incoming.SourceFile = "/synthetic/local/fully-redacted.jsonl"
	incoming.RawUsageJSON = `{"type":"event_msg","payload":{"type":"token_count"}}`
	incoming.ImportedAtMs = 3
	incoming.UpdatedAtMs = 3

	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("reconcile fully redacted rows status=%s err=%v", status, err)
	}

	var count int
	var sourceFile, rawUsage string
	if err := database.Conn().QueryRow(`
		SELECT COUNT(*), COALESCE(source_file, ''), COALESCE(raw_usage_json, '')
		FROM usage_events
	`).Scan(&count, &sourceFile, &rawUsage); err != nil {
		t.Fatalf("select fully redacted reconciliation: %v", err)
	}
	if count != 1 || sourceFile != incoming.SourceFile || rawUsage != incoming.RawUsageJSON {
		t.Fatalf("fully redacted rows did not converge rows=%d source=%q raw=%q", count, sourceFile, rawUsage)
	}

	status, err = database.UpsertEvent(&incoming)
	if err != nil || status != "skipped" {
		t.Fatalf("second fully redacted reconciliation should be idempotent, status=%s err=%v", status, err)
	}
}

func TestUpsertEventReconcilesRedactedLegacyMergedByAnotherHandleAfterLocalCanonical(t *testing.T) {
	targetPath := filepath.Join(t.TempDir(), "agent-ledger.db")
	database, err := Open(targetPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	corrected := &model.UsageEvent{
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "openai",
		ModelRaw:        "gpt-5-codex",
		ModelNormalized: "gpt-5-codex",
		TimestampMs:     1,
		SessionID:       "session-redacted-after-local",
		MessageID:       "message-redacted-after-local",
		SourceFile:      "/synthetic/local/redacted-after-local.jsonl",
		LineNumber:      19,
		RawSHA256:       "redacted-after-local-hash",
		RawUsageJSON:    `{"type":"event_msg","payload":{"type":"token_count"}}`,
		InputTokens:     15,
		OutputTokens:    5,
		TotalTokens:     20,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	setUsageEventFingerprintForTest(corrected)
	if err := insertEvent(database.Conn(), corrected); err != nil {
		t.Fatalf("insert local corrected row: %v", err)
	}

	legacy := *corrected
	legacy.EventID = "redacted-legacy-after-local"
	legacy.DedupeKey = legacy.EventID
	legacy.Provider = "legacy-provider"
	legacy.ModelRaw = "gpt-5"
	legacy.ModelNormalized = "gpt-5"
	legacy.MessageID = ""
	legacy.ImportedAtMs = 2
	legacy.UpdatedAtMs = 2
	incomingPath := filepath.Join(t.TempDir(), "redacted-incoming.db")
	incomingDatabase, err := Open(incomingPath)
	if err != nil {
		t.Fatalf("open incoming database: %v", err)
	}
	if err := insertEvent(incomingDatabase.Conn(), &legacy); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if _, err := incomingDatabase.Conn().Exec(`
		UPDATE usage_events
		SET source_file = NULL, raw_usage_json = NULL
		WHERE event_id = ?
	`, legacy.EventID); err != nil {
		t.Fatalf("simulate merged redacted legacy row: %v", err)
	}
	if err := incomingDatabase.Close(); err != nil {
		t.Fatalf("close incoming database: %v", err)
	}
	mergeDatabase, err := Open(targetPath)
	if err != nil {
		t.Fatalf("open merge database handle: %v", err)
	}
	defer mergeDatabase.Close()
	if inserted, skipped, err := mergeDatabase.MergeFrom(incomingPath); err != nil || inserted != 1 || skipped != 0 {
		t.Fatalf("merge redacted legacy inserted=%d skipped=%d err=%v", inserted, skipped, err)
	}

	incoming := *corrected
	incoming.ImportedAtMs = 3
	incoming.UpdatedAtMs = 3
	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("reconcile redacted legacy after local canonical status=%s err=%v", status, err)
	}

	var count int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil {
		t.Fatalf("count reconciled rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("redacted legacy remained after local import: rows=%d", count)
	}

	status, err = database.UpsertEvent(&incoming)
	if err != nil || status != "skipped" {
		t.Fatalf("second redacted legacy import should be idempotent, status=%s err=%v", status, err)
	}
}

func TestUpsertEventDoesNotReconcileRedactedCodexNearMatches(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sessionID   string
		timestampMs int64
		lineNumber  int
		rawSHA256   string
	}{
		{name: "different session", sessionID: "other-session", timestampMs: 1, lineNumber: 21, rawSHA256: "near-match-hash"},
		{name: "different timestamp", sessionID: "session-near-match", timestampMs: 2, lineNumber: 21, rawSHA256: "near-match-hash"},
		{name: "different line", sessionID: "session-near-match", timestampMs: 1, lineNumber: 22, rawSHA256: "near-match-hash"},
		{name: "different raw hash", sessionID: "session-near-match", timestampMs: 1, lineNumber: 21, rawSHA256: "other-near-match-hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer database.Close()

			corrected := &model.UsageEvent{
				Channel:         "codex",
				SourceAgent:     "codex",
				SourceProduct:   "codex-cli",
				Provider:        "openai",
				ModelRaw:        "gpt-5-codex",
				ModelNormalized: "gpt-5-codex",
				TimestampMs:     1,
				SessionID:       "session-near-match",
				MessageID:       "message-near-match",
				SourceFile:      "/synthetic/local/near-match.jsonl",
				LineNumber:      21,
				RawSHA256:       "near-match-hash",
				RawUsageJSON:    `{"type":"event_msg","payload":{"type":"token_count"}}`,
				InputTokens:     15,
				OutputTokens:    5,
				TotalTokens:     20,
				ImportedAtMs:    1,
				UpdatedAtMs:     1,
			}
			setUsageEventFingerprintForTest(corrected)
			if err := insertEvent(database.Conn(), corrected); err != nil {
				t.Fatalf("insert corrected row: %v", err)
			}

			nearMatch := *corrected
			nearMatch.EventID = "redacted-near-match"
			nearMatch.DedupeKey = nearMatch.EventID
			nearMatch.Provider = "legacy-provider"
			nearMatch.ModelRaw = "gpt-5"
			nearMatch.ModelNormalized = "gpt-5"
			nearMatch.MessageID = ""
			nearMatch.SessionID = tc.sessionID
			nearMatch.TimestampMs = tc.timestampMs
			nearMatch.LineNumber = tc.lineNumber
			nearMatch.RawSHA256 = tc.rawSHA256
			nearMatch.ImportedAtMs = 2
			nearMatch.UpdatedAtMs = 2
			if err := insertEvent(database.Conn(), &nearMatch); err != nil {
				t.Fatalf("insert near-match row: %v", err)
			}
			if _, err := database.Conn().Exec(`
				UPDATE usage_events
				SET source_file = NULL, raw_usage_json = NULL
				WHERE event_id = ?
			`, nearMatch.EventID); err != nil {
				t.Fatalf("redact near-match row: %v", err)
			}
			incoming := *corrected
			incoming.ImportedAtMs = 3
			incoming.UpdatedAtMs = 3
			status, err := database.UpsertEvent(&incoming)
			if err != nil || status != "skipped" {
				t.Fatalf("near-match import status=%s err=%v", status, err)
			}

			var count int
			if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil {
				t.Fatalf("count near-match rows: %v", err)
			}
			if count != 2 {
				t.Fatalf("redacted near-match was incorrectly reconciled: rows=%d", count)
			}
		})
	}
}

func TestUpsertEventExactMatchRestoresRedactedCodexSourceEnvelope(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	event := &model.UsageEvent{
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "openai",
		ModelRaw:        "gpt-5-codex",
		ModelNormalized: "gpt-5-codex",
		TimestampMs:     1,
		SessionID:       "session-exact-redacted-envelope",
		MessageID:       "message-exact-redacted-envelope",
		SourceFile:      "/synthetic/local/exact-redacted-envelope.jsonl",
		LineNumber:      20,
		RawSHA256:       "exact-redacted-envelope-hash",
		RawUsageJSON:    `{"type":"event_msg","payload":{"type":"token_count"}}`,
		InputTokens:     15,
		OutputTokens:    5,
		TotalTokens:     20,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	setUsageEventFingerprintForTest(event)
	if err := insertEvent(database.Conn(), event); err != nil {
		t.Fatalf("insert exact row: %v", err)
	}
	if _, err := database.Conn().Exec(`UPDATE usage_events SET source_file = NULL, raw_usage_json = NULL`); err != nil {
		t.Fatalf("redact exact source envelope: %v", err)
	}

	incoming := *event
	incoming.ImportedAtMs = 2
	incoming.UpdatedAtMs = 2
	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("restore exact source envelope status=%s err=%v", status, err)
	}

	var sourceFile, rawUsage string
	if err := database.Conn().QueryRow(`
		SELECT COALESCE(source_file, ''), COALESCE(raw_usage_json, '')
		FROM usage_events
	`).Scan(&sourceFile, &rawUsage); err != nil {
		t.Fatalf("select restored source envelope: %v", err)
	}
	if sourceFile != incoming.SourceFile || rawUsage != incoming.RawUsageJSON {
		t.Fatalf("exact source envelope was not restored source=%q raw=%q", sourceFile, rawUsage)
	}

	status, err = database.UpsertEvent(&incoming)
	if err != nil || status != "skipped" {
		t.Fatalf("second exact source envelope import should be idempotent, status=%s err=%v", status, err)
	}
}

func TestUpsertEventReconciliationFoldsEquivalentSiblingAccountingMetadata(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ttft := int64(300)
	corrected := &model.UsageEvent{
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "openai",
		ModelRaw:        "gpt-5-codex",
		ModelNormalized: "gpt-5-codex",
		TimestampMs:     1,
		SessionID:       "session-accounting-fold",
		MessageID:       "message-accounting-fold",
		SourceFile:      "/synthetic/accounting/session.jsonl",
		LineNumber:      12,
		RawSHA256:       "accounting-fold-hash",
		InputTokens:     40,
		OutputTokens:    10,
		TotalTokens:     50,
		TTFTMs:          &ttft,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	setUsageEventFingerprintForTest(corrected)
	if err := insertEvent(database.Conn(), corrected); err != nil {
		t.Fatalf("insert corrected usage winner: %v", err)
	}

	sourceTotal := int64(50)
	rawInput := int64(40)
	sibling := *corrected
	sibling.EventID = "legacy-accounting-sibling"
	sibling.DedupeKey = sibling.EventID
	sibling.SourceTotalTokens = &sourceTotal
	sibling.RawInputTokens = &rawInput
	sibling.TokenAccountingMethod = model.AccCodexLastTokenUsage
	sibling.AccountingProfile = "ledger"
	sibling.ImportedAtMs = 2
	sibling.UpdatedAtMs = 2
	if err := insertEvent(database.Conn(), &sibling); err != nil {
		t.Fatalf("insert accounting sibling: %v", err)
	}

	candidate := *corrected
	candidate.InputTokens = 15
	candidate.OutputTokens = 5
	candidate.TotalTokens = 20
	candidate.TTFTMs = nil
	candidate.ImportedAtMs = 3
	candidate.UpdatedAtMs = 3
	status, err := database.UpsertEvent(&candidate)
	if err != nil {
		t.Fatalf("reconcile accounting sibling: %v", err)
	}

	var count int
	var total, storedSourceTotal, storedRawInput int64
	var accounting, profile string
	if err := database.Conn().QueryRow(`
		SELECT COUNT(*), total_tokens, source_total_tokens, raw_input_tokens,
			token_accounting_method, accounting_profile
		FROM usage_events
	`).Scan(&count, &total, &storedSourceTotal, &storedRawInput, &accounting, &profile); err != nil {
		t.Fatalf("select accounting fold result: %v", err)
	}
	if status != "updated" || count != 1 || total != 50 || storedSourceTotal != sourceTotal || storedRawInput != rawInput || accounting != model.AccCodexLastTokenUsage || profile != "ledger" {
		t.Fatalf("accounting metadata was not folded status=%s rows=%d total=%d source_total=%d raw_input=%d accounting=%s profile=%s", status, count, total, storedSourceTotal, storedRawInput, accounting, profile)
	}

	status, err = database.UpsertEvent(&candidate)
	if err != nil || status != "skipped" {
		t.Fatalf("second accounting reconciliation should be idempotent, status=%s err=%v", status, err)
	}
}

func TestUpsertEventReconciliationKeepsAccountingBundleCoherent(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ttft := int64(300)
	winner := &model.UsageEvent{
		Channel:           "codex",
		SourceAgent:       "codex",
		SourceProduct:     "codex-cli",
		Provider:          "openai",
		ModelRaw:          "gpt-5-codex",
		ModelNormalized:   "gpt-5-codex",
		TimestampMs:       1,
		SessionID:         "session-accounting-coherence",
		MessageID:         "message-accounting-coherence",
		SourceFile:        "/synthetic/accounting/coherence.jsonl",
		LineNumber:        16,
		RawSHA256:         "accounting-coherence-hash",
		InputTokens:       40,
		OutputTokens:      10,
		TotalTokens:       50,
		TTFTMs:            &ttft,
		AccountingProfile: "ledger",
		ImportedAtMs:      1,
		UpdatedAtMs:       1,
	}
	setUsageEventFingerprintForTest(winner)
	if err := insertEvent(database.Conn(), winner); err != nil {
		t.Fatalf("insert accounting winner: %v", err)
	}

	sourceTotal := int64(50)
	ledgerSibling := *winner
	ledgerSibling.EventID = "ledger-accounting-sibling"
	ledgerSibling.DedupeKey = ledgerSibling.EventID
	ledgerSibling.SourceTotalTokens = &sourceTotal
	ledgerSibling.AccountingProfile = "ledger"
	ledgerSibling.ImportedAtMs = 2
	ledgerSibling.UpdatedAtMs = 2
	if err := insertEvent(database.Conn(), &ledgerSibling); err != nil {
		t.Fatalf("insert ledger accounting sibling: %v", err)
	}

	rawInput := int64(40)
	compatibleSibling := *winner
	compatibleSibling.EventID = "compatible-accounting-sibling"
	compatibleSibling.DedupeKey = compatibleSibling.EventID
	compatibleSibling.RawInputTokens = &rawInput
	compatibleSibling.TokenAccountingMethod = model.AccCodexLastTokenUsage
	compatibleSibling.AccountingProfile = "ccusage_compatible"
	compatibleSibling.ImportedAtMs = 3
	compatibleSibling.UpdatedAtMs = 3
	if err := insertEvent(database.Conn(), &compatibleSibling); err != nil {
		t.Fatalf("insert compatible accounting sibling: %v", err)
	}

	incoming := *winner
	incoming.InputTokens = 15
	incoming.OutputTokens = 5
	incoming.TotalTokens = 20
	incoming.TTFTMs = nil
	incoming.ImportedAtMs = 4
	incoming.UpdatedAtMs = 4

	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("reconcile accounting bundle status=%s err=%v", status, err)
	}

	var storedSourceTotal, storedRawInput sql.NullInt64
	var accountingMethod, accountingProfile sql.NullString
	if err := database.Conn().QueryRow(`
		SELECT source_total_tokens, raw_input_tokens,
			token_accounting_method, accounting_profile
		FROM usage_events
	`).Scan(&storedSourceTotal, &storedRawInput, &accountingMethod, &accountingProfile); err != nil {
		t.Fatalf("select coherent accounting bundle: %v", err)
	}
	if !storedSourceTotal.Valid || storedSourceTotal.Int64 != sourceTotal || storedRawInput.Valid || accountingMethod.Valid || accountingProfile.String != "ledger" {
		t.Fatalf("accounting bundle was mixed source_total=%v raw_input=%v method=%v profile=%v", storedSourceTotal, storedRawInput, accountingMethod, accountingProfile)
	}
}

func TestUpsertEventReconciliationPreservesComplementarySourceMetadata(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ttft := int64(100)
	usageWinner := &model.UsageEvent{
		EventID:         "legacy-usage-winner",
		DedupeKey:       "legacy-usage-winner",
		DedupeStrategy:  "session_token",
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "legacy-provider",
		ModelRaw:        "gpt-5",
		ModelNormalized: "gpt-5",
		TimestampMs:     1,
		SessionID:       "session-source-metadata",
		SessionPathID:   "2026/07/13/rollout-source-metadata",
		SourceFile:      "/synthetic/source-metadata.jsonl",
		LineNumber:      14,
		RawSHA256:       "source-metadata-hash",
		InputTokens:     40,
		OutputTokens:    10,
		TotalTokens:     50,
		TTFTMs:          &ttft,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	if err := insertEvent(database.Conn(), usageWinner); err != nil {
		t.Fatalf("insert usage winner: %v", err)
	}

	metadataSibling := *usageWinner
	metadataSibling.EventID = "legacy-metadata-sibling"
	metadataSibling.DedupeKey = metadataSibling.EventID
	metadataSibling.SessionPathID = ""
	metadataSibling.TurnID = "turn-from-sibling"
	metadataSibling.ProjectPath = "/synthetic/project"
	metadataSibling.InputTokens = 15
	metadataSibling.OutputTokens = 5
	metadataSibling.TotalTokens = 20
	metadataSibling.TTFTMs = nil
	metadataSibling.ImportedAtMs = 2
	metadataSibling.UpdatedAtMs = 2
	if err := insertEvent(database.Conn(), &metadataSibling); err != nil {
		t.Fatalf("insert metadata sibling: %v", err)
	}

	incoming := metadataSibling
	incoming.Provider = "openai"
	incoming.ModelRaw = "gpt-5-codex"
	incoming.ModelNormalized = "gpt-5-codex"
	incoming.TurnID = ""
	incoming.ProjectPath = ""
	incoming.ImportedAtMs = 3
	incoming.UpdatedAtMs = 3
	setUsageEventFingerprintForTest(&incoming)

	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("reconcile source metadata status=%s err=%v", status, err)
	}

	var count int
	var sessionPathID, turnID, projectPath string
	if err := database.Conn().QueryRow(`
		SELECT COUNT(*), COALESCE(session_path_id, ''), COALESCE(turn_id, ''), COALESCE(project_path, '')
		FROM usage_events
	`).Scan(&count, &sessionPathID, &turnID, &projectPath); err != nil {
		t.Fatalf("select reconciled source metadata: %v", err)
	}
	if count != 1 || sessionPathID != usageWinner.SessionPathID || turnID != metadataSibling.TurnID || projectPath != metadataSibling.ProjectPath {
		t.Fatalf("complementary source metadata was not preserved rows=%d session_path=%q turn=%q project=%q", count, sessionPathID, turnID, projectPath)
	}

	status, err = database.UpsertEvent(&incoming)
	if err != nil || status != "skipped" {
		t.Fatalf("second source metadata reconciliation should be idempotent, status=%s err=%v", status, err)
	}
}

func TestUpsertEventReconciliationKeepsSourceMetadataIndependentFromUsageWinner(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	stable := &model.UsageEvent{
		EventID:            "stable-source-metadata",
		DedupeKey:          "stable-source-metadata",
		DedupeStrategy:     "session_token",
		Channel:            "codex",
		SourceAgent:        "codex",
		SourceProduct:      "codex-cli",
		ObservabilityLevel: "full",
		Provider:           "legacy-provider",
		ModelRaw:           "gpt-5",
		ModelNormalized:    "gpt-5",
		TimestampMs:        1,
		SessionID:          "session-source-stability",
		SessionPathID:      "stable/session/path",
		TurnID:             "stable-turn",
		ProjectPath:        "project-label",
		SourceFile:         "/synthetic/source-stability.jsonl",
		LineNumber:         17,
		RawSHA256:          "source-stability-hash",
		InputTokens:        15,
		OutputTokens:       5,
		TotalTokens:        20,
		ImportedAtMs:       1,
		UpdatedAtMs:        1,
	}
	if err := insertEvent(database.Conn(), stable); err != nil {
		t.Fatalf("insert stable source metadata: %v", err)
	}

	ttft := int64(100)
	richerUsage := *stable
	richerUsage.EventID = "richer-usage-stale-metadata"
	richerUsage.DedupeKey = richerUsage.EventID
	richerUsage.SourceProduct = "stale-product"
	richerUsage.ObservabilityLevel = "inferred"
	richerUsage.SessionPathID = "stale/session/path"
	richerUsage.TurnID = "stale-turn"
	richerUsage.ProjectPath = "/synthetic/project"
	richerUsage.InputTokens = 40
	richerUsage.OutputTokens = 10
	richerUsage.TotalTokens = 50
	richerUsage.TTFTMs = &ttft
	richerUsage.ImportedAtMs = 2
	richerUsage.UpdatedAtMs = 2
	if err := insertEvent(database.Conn(), &richerUsage); err != nil {
		t.Fatalf("insert richer usage with stale metadata: %v", err)
	}

	incoming := *stable
	incoming.Provider = "openai"
	incoming.ModelRaw = "gpt-5-codex"
	incoming.ModelNormalized = "gpt-5-codex"
	incoming.SessionPathID = "incoming/session/path"
	incoming.TurnID = "incoming-turn"
	incoming.ProjectPath = ""
	incoming.ImportedAtMs = 3
	incoming.UpdatedAtMs = 3
	setUsageEventFingerprintForTest(&incoming)

	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("reconcile stable source metadata status=%s err=%v", status, err)
	}

	var sourceProduct, observability, sessionPathID, turnID, projectPath string
	var total int64
	if err := database.Conn().QueryRow(`
		SELECT source_product, observability_level, session_path_id, turn_id,
			project_path, total_tokens
		FROM usage_events
	`).Scan(&sourceProduct, &observability, &sessionPathID, &turnID, &projectPath, &total); err != nil {
		t.Fatalf("select stable source metadata: %v", err)
	}
	if sourceProduct != stable.SourceProduct || observability != stable.ObservabilityLevel || sessionPathID != stable.SessionPathID || turnID != stable.TurnID || projectPath != richerUsage.ProjectPath || total != richerUsage.TotalTokens {
		t.Fatalf("source metadata followed usage winner product=%q observability=%q session_path=%q turn=%q project=%q total=%d", sourceProduct, observability, sessionPathID, turnID, projectPath, total)
	}
}

func TestUpsertEventReconciliationRefreshesCurrentRawEnvelope(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ttft := int64(100)
	stored := &model.UsageEvent{
		Channel:         "codex",
		SourceAgent:     "codex",
		SourceProduct:   "codex-cli",
		Provider:        "openai",
		ModelRaw:        "gpt-5-codex",
		ModelNormalized: "gpt-5-codex",
		TimestampMs:     1,
		SessionID:       "session-raw-refresh",
		SourceFile:      "/synthetic/raw-refresh.jsonl",
		LineNumber:      18,
		RawSHA256:       "raw-refresh-hash",
		InputTokens:     40,
		OutputTokens:    10,
		TotalTokens:     50,
		TTFTMs:          &ttft,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	setUsageEventFingerprintForTest(stored)
	if err := insertEvent(database.Conn(), stored); err != nil {
		t.Fatalf("insert stored canonical row: %v", err)
	}

	incoming := *stored
	incoming.InputTokens = 15
	incoming.OutputTokens = 5
	incoming.TotalTokens = 20
	incoming.TTFTMs = nil
	incoming.RawUsageJSON = `{"type":"event_msg","payload":{"type":"token_count"}}`
	incoming.ImportedAtMs = 2
	incoming.UpdatedAtMs = 2
	setUsageEventFingerprintForTest(&incoming)

	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("refresh raw envelope status=%s err=%v", status, err)
	}

	var rawUsage string
	if err := database.Conn().QueryRow(`SELECT COALESCE(raw_usage_json, '') FROM usage_events`).Scan(&rawUsage); err != nil {
		t.Fatalf("select refreshed raw envelope: %v", err)
	}
	if rawUsage != incoming.RawUsageJSON {
		t.Fatalf("current raw envelope was not persisted: %q", rawUsage)
	}

	status, err = database.UpsertEvent(&incoming)
	if err != nil || status != "skipped" {
		t.Fatalf("second raw envelope reconciliation should be idempotent, status=%s err=%v", status, err)
	}
}

func TestUpsertEventReconciliationStoresRecomputableIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		messageID string
	}{
		{name: "session token identity"},
		{name: "message identity", messageID: "message-identity-current"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer database.Close()

			ttft := int64(300)
			legacy := &model.UsageEvent{
				EventID:         "legacy-identity-event",
				DedupeKey:       "legacy-identity-event",
				DedupeStrategy:  "session_token",
				Channel:         "codex",
				SourceAgent:     "codex",
				SourceProduct:   "codex-cli",
				Provider:        "legacy-provider",
				ModelRaw:        "gpt-5",
				ModelNormalized: "gpt-5",
				TimestampMs:     1,
				SessionID:       "session-identity-current",
				SourceFile:      "/synthetic/identity/session.jsonl",
				LineNumber:      13,
				RawSHA256:       "identity-consistency-hash",
				InputTokens:     40,
				OutputTokens:    10,
				TotalTokens:     50,
				TTFTMs:          &ttft,
				ImportedAtMs:    1,
				UpdatedAtMs:     1,
			}
			if err := insertEvent(database.Conn(), legacy); err != nil {
				t.Fatalf("insert legacy identity row: %v", err)
			}

			candidate := *legacy
			candidate.Provider = "openai"
			candidate.ModelRaw = "gpt-5-codex"
			candidate.ModelNormalized = "gpt-5-codex"
			candidate.MessageID = tc.messageID
			candidate.InputTokens = 15
			candidate.OutputTokens = 5
			candidate.TotalTokens = 20
			candidate.TTFTMs = nil
			candidate.ImportedAtMs = 2
			candidate.UpdatedAtMs = 2
			setUsageEventFingerprintForTest(&candidate)
			incomingEventID := candidate.EventID

			status, err := database.UpsertEvent(&candidate)
			if err != nil || status != "updated" {
				t.Fatalf("identity reconciliation status=%s err=%v", status, err)
			}

			stored := selectOnlyUsageEventForTest(t, database)
			recomputed, strategy := computeUsageEventFingerprintForTest(stored)
			if stored.EventID != recomputed || stored.DedupeKey != recomputed || stored.DedupeStrategy != strategy {
				t.Fatalf("stored identity is not recomputable event_id=%s dedupe=%s strategy=%s recomputed=%s recomputed_strategy=%s", stored.EventID, stored.DedupeKey, stored.DedupeStrategy, recomputed, strategy)
			}
			if stored.Provider != candidate.Provider || stored.ModelRaw != candidate.ModelRaw || stored.SessionID != candidate.SessionID || stored.MessageID != candidate.MessageID || stored.TotalTokens != legacy.TotalTokens {
				t.Fatalf("unexpected canonical row provider=%s model=%s session=%s message=%s total=%d", stored.Provider, stored.ModelRaw, stored.SessionID, stored.MessageID, stored.TotalTokens)
			}
			if tc.messageID == "" && stored.EventID == incomingEventID {
				t.Fatalf("session-token identity should be recomputed after retaining different usage")
			}

			status, err = database.UpsertEvent(&candidate)
			if err != nil || status != "skipped" {
				t.Fatalf("second identity reconciliation should be idempotent, status=%s err=%v", status, err)
			}
		})
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
			if tc.changeEventID {
				setUsageEventFingerprintForTest(&candidate)
			}
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

func setUsageEventFingerprintForTest(ev *model.UsageEvent) {
	eventID, strategy := computeUsageEventFingerprintForTest(ev)
	ev.EventID = eventID
	ev.DedupeKey = eventID
	ev.DedupeStrategy = strategy
}

func computeUsageEventFingerprintForTest(ev *model.UsageEvent) (string, string) {
	agent := ev.SourceAgent
	if agent == "" {
		agent = ev.Channel
	}
	eventID, strategy := fingerprint.Compute(&fingerprint.ParsedRecord{
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
	return eventID, string(strategy)
}

func selectOnlyUsageEventForTest(t *testing.T, database *Database) *model.UsageEvent {
	t.Helper()
	var count int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil {
		t.Fatalf("count usage events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one usage event, got %d", count)
	}
	var eventID string
	if err := database.Conn().QueryRow(`SELECT event_id FROM usage_events`).Scan(&eventID); err != nil {
		t.Fatalf("select only event id: %v", err)
	}
	tx, err := database.Conn().Begin()
	if err != nil {
		t.Fatalf("begin select transaction: %v", err)
	}
	defer tx.Rollback()
	event, err := selectEventForComparison(tx, eventID)
	if err != nil {
		t.Fatalf("select only event: %v", err)
	}
	return event
}

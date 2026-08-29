package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestReconcileDuplicateSupplementAndConflict(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()

	original := testEvent("event-1", "content-1", 10)
	status, err := database.UpsertEvent(original)
	if err != nil || status != ReconcileInserted {
		t.Fatalf("insert status=%q err=%v", status, err)
	}
	var updatedAt int64
	if err := database.Conn().QueryRow(`SELECT updated_at_ms FROM usage_events WHERE event_id=?`, original.EventID).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	duplicate := *original
	duplicate.UpdatedAtMs = updatedAt + 1000
	status, err = database.UpsertEvent(&duplicate)
	if err != nil || status != ReconcileSkipped {
		t.Fatalf("duplicate status=%q err=%v", status, err)
	}
	var duplicateUpdatedAt int64
	_ = database.Conn().QueryRow(`SELECT updated_at_ms FROM usage_events WHERE event_id=?`, original.EventID).Scan(&duplicateUpdatedAt)
	if duplicateUpdatedAt != updatedAt {
		t.Fatalf("exact duplicate wrote row: updated_at %d -> %d", updatedAt, duplicateUpdatedAt)
	}

	supplement := duplicate
	supplement.ContentSHA256 = "content-2"
	supplement.Provider = "openai"
	supplement.ModelRaw = "gpt-test"
	supplement.ModelNormalized = "gpt-test"
	supplement.ModelResolution = model.ModelResolutionDirectEvent
	supplement.ModelIsFallback = false
	status, err = database.UpsertEvent(&supplement)
	if err != nil || status != ReconcileUpdated {
		t.Fatalf("supplement status=%q err=%v", status, err)
	}
	stored, err := selectEvent(database.Conn(), original.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ModelNormalized != "gpt-test" || stored.Provider != "openai" || stored.ContentSHA256 != mustContentSHA256(t, stored) {
		t.Fatalf("supplement not persisted: %+v", stored)
	}

	conflict := supplement
	conflict.ContentSHA256 = "content-3"
	conflict.TotalTokens++
	status, err = database.UpsertEvent(&conflict)
	if status != ReconcileRejected || !IsRejectError(err) {
		t.Fatalf("token conflict status=%q err=%v", status, err)
	}
	storedAfter, _ := selectEvent(database.Conn(), original.EventID)
	if storedAfter.TotalTokens != stored.TotalTokens || storedAfter.ContentSHA256 != stored.ContentSHA256 {
		t.Fatalf("rejected event changed canonical row: before=%+v after=%+v", stored, storedAfter)
	}
}

func TestAnnotatedModelAliasIsCanonicalized(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()

	first := testEvent("event-parens", "hash-parens", 4)
	first.ModelRaw = "gpt-5.4(xhigh)"
	first.ModelNormalized = "gpt-5.4(xhigh)"
	first.ModelResolution = model.ModelResolutionDirectEvent
	first.ModelIsFallback = false
	if _, err := database.UpsertEvent(first); err != nil {
		t.Fatal(err)
	}
	stored, err := selectEvent(database.Conn(), first.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ModelRaw != "gpt-5.4(xhigh)" || stored.ModelNormalized != "gpt-5.4" {
		t.Fatalf("insert did not canonicalize parenthetical model: %+v", stored)
	}
	if stored.ContentSHA256 != mustContentSHA256(t, stored) {
		t.Fatalf("canonical insert stored inconsistent content hash")
	}

	second := *stored
	second.ModelNormalized = "gpt-5.4"
	status, err := database.UpsertEvent(&second)
	if err != nil || (status != ReconcileSkipped && status != ReconcileUpdated) {
		t.Fatalf("canonical alias status=%q err=%v", status, err)
	}
	stored, err = selectEvent(database.Conn(), first.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ModelNormalized != "gpt-5.4" {
		t.Fatalf("parenthetical alias overwrote canonical model: %+v", stored)
	}

	pollutedModels := map[string]string{
		"event-polluted-parenthetical": "gpt-5.4(xhigh)",
		"event-polluted-context":       "gpt-5.4[1m]",
	}
	for eventID, modelID := range pollutedModels {
		polluted := testEvent(eventID, "hash-"+eventID, 8)
		polluted.ModelRaw = modelID
		polluted.ModelNormalized = modelID
		polluted.ModelResolution = model.ModelResolutionDirectEvent
		polluted.ModelIsFallback = false
		if err := insertEvent(database.Conn(), polluted); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := database.RepairCanonicalModelIDs()
	if err != nil || updated != len(pollutedModels) {
		t.Fatalf("repair updated=%d err=%v", updated, err)
	}
	for eventID, modelID := range pollutedModels {
		repaired, err := selectEvent(database.Conn(), hashTestValue("event:"+eventID))
		if err != nil {
			t.Fatal(err)
		}
		if repaired.ModelRaw != modelID || repaired.ModelNormalized != "gpt-5.4" {
			t.Fatalf("repair did not canonicalize stored model: %+v", repaired)
		}
		if repaired.ContentSHA256 != mustContentSHA256(t, repaired) {
			t.Fatalf("repair stored inconsistent content hash")
		}
	}
}

func TestDirectModelConflictIsRejected(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	first := testEvent("event-model", "hash-a", 4)
	first.ModelRaw = "model-a"
	first.ModelNormalized = "model-a"
	first.ModelResolution = model.ModelResolutionDirectEvent
	first.ModelIsFallback = false
	if _, err := database.UpsertEvent(first); err != nil {
		t.Fatal(err)
	}
	second := *first
	second.ContentSHA256 = "hash-b"
	second.ModelRaw = "model-b"
	second.ModelNormalized = "model-b"
	status, err := database.UpsertEvent(&second)
	var rejected *RejectError
	if status != ReconcileRejected || !errors.As(err, &rejected) || rejected.Code != "direct_model_conflict" {
		t.Fatalf("direct conflict status=%q err=%v", status, err)
	}
}

func TestReconcileRecomputesCanonicalContentHash(t *testing.T) {
	sourceTotal := int64(15)
	existing := testEvent("canonical", "", 10)
	existing.SourceTotalTokens = &sourceTotal
	existing.ContentSHA256 = mustContentSHA256(t, existing)

	incoming := *existing
	incoming.SourceTotalTokens = nil
	incoming.Provider = "openai"
	incoming.ModelRaw = "gpt-test"
	incoming.ModelNormalized = "gpt-test"
	incoming.ModelResolution = model.ModelResolutionDirectEvent
	incoming.ModelIsFallback = false
	incoming.ContentSHA256 = mustContentSHA256(t, &incoming)

	canonical, status, err := reconcile(existing, &incoming)
	if err != nil || status != ReconcileUpdated {
		t.Fatalf("reconcile status=%q err=%v", status, err)
	}
	wantHash := mustContentSHA256(t, canonical)
	if canonical.ContentSHA256 != wantHash {
		t.Fatalf("canonical content hash=%q want=%q", canonical.ContentSHA256, wantHash)
	}

	complete := *canonical
	complete.ContentSHA256 = wantHash
	_, status, err = reconcile(canonical, &complete)
	if err != nil || status != ReconcileSkipped {
		t.Fatalf("complete observation status=%q err=%v", status, err)
	}
}

func TestReconcileFillsMetadataWhenContentMatches(t *testing.T) {
	existing := testEvent("metadata", "same-content", 10)
	incoming := *existing
	incoming.ProjectPath = "/private/project"
	incoming.SourceFile = "/private/source.jsonl"

	canonical, status, err := reconcile(existing, &incoming)
	if err != nil || status != ReconcileUpdated {
		t.Fatalf("reconcile status=%q err=%v", status, err)
	}
	if canonical.ProjectPath != incoming.ProjectPath || canonical.SourceFile != incoming.SourceFile {
		t.Fatalf("metadata was not restored: %+v", canonical)
	}
}

func TestMergePreflightRollbackAndIdempotency(t *testing.T) {
	destination := openTestDatabase(t)
	defer destination.Close()
	base := testEvent("shared", "shared-content", 5)
	base.ContentSHA256 = mustContentSHA256(t, base)
	if _, err := destination.UpsertEvent(base); err != nil {
		t.Fatal(err)
	}

	incomingPath := filepath.Join(t.TempDir(), "incoming.aldb")
	incoming, err := Open(incomingPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := incoming.UpsertEvent(base); err != nil {
		t.Fatal(err)
	}
	unique := testEvent("unique", "unique-content", 7)
	unique.ContentSHA256 = mustContentSHA256(t, unique)
	if _, err := incoming.UpsertEvent(unique); err != nil {
		t.Fatal(err)
	}
	_ = incoming.Close()

	result, err := destination.MergeFrom(incomingPath)
	if err != nil || result.Added != 1 || result.Skipped != 1 {
		t.Fatalf("merge result=%+v err=%v", result, err)
	}
	result, err = destination.MergeFrom(incomingPath)
	if err != nil || result.Added != 0 || result.Updated != 0 || result.Skipped != 2 {
		t.Fatalf("repeat merge result=%+v err=%v", result, err)
	}

	conflictPath := filepath.Join(t.TempDir(), "conflict.aldb")
	conflictDB, err := Open(conflictPath)
	if err != nil {
		t.Fatal(err)
	}
	conflict := *base
	conflict.TotalTokens++
	conflict.ContentSHA256 = mustContentSHA256(t, &conflict)
	if _, err := conflictDB.UpsertEvent(&conflict); err != nil {
		t.Fatal(err)
	}
	another := testEvent("must-not-insert", "new-content", 9)
	another.ContentSHA256 = mustContentSHA256(t, another)
	if _, err := conflictDB.UpsertEvent(another); err != nil {
		t.Fatal(err)
	}
	_ = conflictDB.Close()

	var before int64
	_ = destination.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&before)
	result, err = destination.MergeFrom(conflictPath)
	var mergeConflict *MergeConflictError
	if !errors.As(err, &mergeConflict) || result.Rejected != 1 {
		t.Fatalf("conflict result=%+v err=%v", result, err)
	}
	var after int64
	_ = destination.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&after)
	if after != before {
		t.Fatalf("conflict merge partially wrote destination: %d -> %d", before, after)
	}
}

func TestMergeRejectsNonV3IncomingDatabases(t *testing.T) {
	destination := openTestDatabase(t)
	defer destination.Close()

	tests := []struct {
		name  string
		setup func(string) error
	}{
		{name: "schema v2", setup: func(path string) error {
			conn, err := sql.Open("sqlite3", path)
			if err != nil {
				return err
			}
			defer conn.Close()
			_, err = conn.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO meta VALUES ('schema_version', '2')`)
			return err
		}},
		{name: "identity v1", setup: func(path string) error {
			conn, err := sql.Open("sqlite3", path)
			if err != nil {
				return err
			}
			defer conn.Close()
			_, err = conn.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO meta VALUES ('schema_version', '3'); INSERT INTO meta VALUES ('identity_version', '1')`)
			return err
		}},
		{name: "ordinary file", setup: func(path string) error {
			return os.WriteFile(path, []byte("not sqlite"), 0o600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "incoming.aldb")
			if err := test.setup(path); err != nil {
				t.Fatal(err)
			}
			if _, err := destination.MergeFrom(path); err == nil {
				t.Fatalf("expected %s rejection", test.name)
			}
		})
	}

	if _, err := destination.MergeFrom(t.TempDir()); err == nil {
		t.Fatal("expected directory rejection")
	}
}

func TestMergeRejectsTamperedContentAndMalformedIdentity(t *testing.T) {
	for _, test := range []struct {
		name     string
		column   string
		value    string
		wantCode string
	}{
		{name: "content hash", column: "content_sha256", value: "forged-content", wantCode: "invalid_content_hash"},
		{name: "event id", column: "event_id", value: "forged-event", wantCode: "invalid_identity"},
		{name: "session key", column: "session_key", value: "forged-session", wantCode: "invalid_identity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			destination := openTestDatabase(t)
			defer destination.Close()
			incomingPath := filepath.Join(t.TempDir(), "incoming.aldb")
			incoming, err := Open(incomingPath)
			if err != nil {
				t.Fatal(err)
			}
			event := testEvent("tampered", "", 10)
			event.ContentSHA256 = mustContentSHA256(t, event)
			if _, err := incoming.UpsertEvent(event); err != nil {
				t.Fatal(err)
			}
			if _, err := incoming.Conn().Exec(`UPDATE usage_events SET `+test.column+`=?`, test.value); err != nil {
				t.Fatal(err)
			}
			if err := incoming.Close(); err != nil {
				t.Fatal(err)
			}

			result, err := destination.MergeFrom(incomingPath)
			var mergeConflict *MergeConflictError
			if !errors.As(err, &mergeConflict) || result.Rejected != 1 || len(result.Conflicts) != 1 || result.Conflicts[0].Code != test.wantCode {
				t.Fatalf("merge result=%+v err=%v", result, err)
			}
		})
	}
}

func TestMergeRejectsInvalidDestinationBeforeWriting(t *testing.T) {
	destination := openTestDatabase(t)
	defer destination.Close()
	existing := testEvent("existing", "", 10)
	existing.ContentSHA256 = mustContentSHA256(t, existing)
	if _, err := destination.UpsertEvent(existing); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.Conn().Exec(`UPDATE usage_events SET content_sha256='forged-content' WHERE event_id=?`, existing.EventID); err != nil {
		t.Fatal(err)
	}

	incomingPath := filepath.Join(t.TempDir(), "incoming.aldb")
	incoming, err := Open(incomingPath)
	if err != nil {
		t.Fatal(err)
	}
	newEvent := testEvent("new", "", 5)
	newEvent.ContentSHA256 = mustContentSHA256(t, newEvent)
	if _, err := incoming.UpsertEvent(newEvent); err != nil {
		t.Fatal(err)
	}
	if err := incoming.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := destination.MergeFrom(incomingPath); err == nil || !strings.Contains(err.Error(), "destination database contains an invalid event") {
		t.Fatalf("expected invalid destination rejection, got %v", err)
	}
	var count int64
	if err := destination.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events WHERE event_id=?`, newEvent.EventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("merge wrote incoming event before destination validation completed")
	}
}

func TestValidateAccountingProfiles(t *testing.T) {
	event := testEvent("accounting", "hash", 10)
	event.TokenAccountingMethod = model.AccWorkBuddyRawUsage
	event.InputTokens = 3
	event.OutputTokens = 4
	event.ReasoningTokens = 2
	event.CacheCreationTokens = 1
	event.CacheReadTokens = 2
	event.TotalTokens = 10
	if err := ValidateEvent(event); err != nil {
		t.Fatalf("valid workbuddy accounting: %v", err)
	}
	event.TotalTokens = 12
	if err := ValidateEvent(event); !IsRejectError(err) {
		t.Fatalf("expected accounting rejection, got %v", err)
	}

	gemini := testEvent("gemini-accounting", "gemini-hash", 15)
	gemini.Channel = "gemini"
	gemini.SourceProduct = "gemini-cli"
	gemini.TokenAccountingMethod = model.AccGeminiUsage
	gemini.InputTokens = 7
	gemini.OutputTokens = 3
	gemini.ReasoningTokens = 2
	gemini.CacheReadTokens = 3
	gemini.TotalTokens = 15
	if err := ValidateEvent(gemini); err != nil {
		t.Fatalf("valid Gemini accounting: %v", err)
	}
	gemini.TotalTokens = 16
	if err := ValidateEvent(gemini); !IsRejectError(err) {
		t.Fatalf("expected Gemini accounting rejection, got %v", err)
	}

	codexPartial := testEvent("codex-partial", "codex-partial-hash", 10)
	codexPartial.TokenAccountingMethod = model.AccCodexTotalDelta
	codexPartial.InputTokens = 4
	codexPartial.ObservabilityLevel = "partial"
	if err := ValidateEvent(codexPartial); err != nil {
		t.Fatalf("partial Codex delta may preserve an authoritative total above known buckets: %v", err)
	}
	codexPartial.ObservabilityLevel = "full"
	if err := ValidateEvent(codexPartial); !IsRejectError(err) {
		t.Fatalf("full Codex delta must conserve total, got %v", err)
	}
}

func testEvent(id, content string, total int64) *model.UsageEvent {
	return &model.UsageEvent{
		EventID: hashTestValue("event:" + id), IdentityVersion: model.IdentityVersion,
		IdentityStrategy: "native_event", IdentityScope: "session",
		ContentSHA256: content, ParserVersion: "test-v1", EventGranularity: "request",
		Channel: "codex", SourceProduct: "codex-cli", Provider: "",
		ModelNormalized: "unknown", ModelResolution: model.ModelResolutionUnknown, ModelIsFallback: true,
		TimestampMs: 1_700_000_000_000, SessionKey: hashTestValue("session-key"), SessionID: "native-session",
		InputTokens: total, TotalTokens: total, ImportedAtMs: 100, UpdatedAtMs: 100,
	}
}

func mustContentSHA256(t *testing.T, event *model.UsageEvent) string {
	t.Helper()
	value, err := contentSHA256ForEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func hashTestValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

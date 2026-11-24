package cmd

import (
	"database/sql"
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func agyVarint(number, value uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, number<<3), value)
}

func agyBytes(number uint64, payload []byte) []byte {
	field := binary.AppendUvarint(nil, number<<3|2)
	return append(binary.AppendUvarint(field, uint64(len(payload))), payload...)
}

func agyUsage(messageID, responseID string, withSplit bool) []byte {
	usage := append(agyVarint(2, 10), agyVarint(3, 100)...)
	if messageID != "" {
		usage = append(usage, agyBytes(7, []byte(messageID))...)
	}
	if responseID != "" {
		usage = append(usage, agyBytes(11, []byte(responseID))...)
	}
	if withSplit {
		usage = append(usage, agyVarint(9, 40)...)
		usage = append(usage, agyVarint(10, 60)...)
	}
	return usage
}

func agySource(t *testing.T, path string, genUsage []byte) *sql.DB {
	t.Helper()
	source, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	agyExec(t, source, `
		CREATE TABLE gen_metadata (idx INTEGER PRIMARY KEY, data BLOB);
		CREATE TABLE steps (idx INTEGER PRIMARY KEY, metadata BLOB);
		CREATE TABLE trajectory_metadata_blob (data BLOB);`)
	agyExec(t, source, "INSERT INTO trajectory_metadata_blob VALUES (?)", agyBytes(2, agyVarint(1, 1700000000)))
	agyExec(t, source, "INSERT INTO gen_metadata VALUES (1, ?)",
		append(agyBytes(1, agyBytes(4, genUsage)), agyVarint(2, 5)...))
	return source
}

func agyExec(t *testing.T, source *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := source.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func agyImport(t *testing.T, database *db.Database, paths ...string) importAdapterResult {
	t.Helper()
	result := importAdapterFiles(database, adapters.NewAntigravityAdapter(), paths, time.Time{})
	if result.rejected != 0 || len(result.warnings) != 0 {
		t.Fatalf("unexpected import result %+v", result)
	}
	return result
}

func assertAGYLedger(t *testing.T, database *db.Database, count, total, reasoning, timestamp int64) {
	t.Helper()
	var rows, tokens, reasoningTokens, timestampMs int64
	err := database.Conn().QueryRow(`SELECT count(*), sum(total_tokens), sum(reasoning_tokens), max(timestamp_ms)
		FROM usage_events WHERE channel = 'antigravity'`).Scan(&rows, &tokens, &reasoningTokens, &timestampMs)
	if err != nil || rows != count || tokens != total || reasoningTokens != reasoning || timestampMs != timestamp {
		t.Fatalf("ledger count=%d total=%d reasoning=%d timestamp=%d err=%v", rows, tokens, reasoningTokens, timestampMs, err)
	}
}

func agyLedger(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestAntigravityStagedImportKeepsOneEvent(t *testing.T) {
	for _, initialID := range []string{"message", ""} {
		name := "message_only"
		if initialID == "" {
			name = "without_id"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conversation.db")
			source := agySource(t, path, agyUsage(initialID, "", false))
			database := agyLedger(t)
			if got := agyImport(t, database, path); got.added != 1 {
				t.Fatalf("first import %+v", got)
			}
			assertAGYLedger(t, database, 1, 110, 0, 1700000000000)

			step := agyBytes(1, agyVarint(1, 1700000100))
			agyExec(t, source, "INSERT INTO steps VALUES (5, ?)", append(step, agyBytes(9, agyUsage(initialID, "response", true))...))
			if got := agyImport(t, database, path); got.added != 0 || got.updated != 1 {
				t.Fatalf("enriching import %+v", got)
			}
			assertAGYLedger(t, database, 1, 110, 40, 1700000100000)

			agyExec(t, source, "DELETE FROM gen_metadata")
			agyExec(t, source, "UPDATE steps SET metadata = ? WHERE idx = 5", append(step, agyBytes(9, agyUsage("", "response", true))...))
			if got := agyImport(t, database, path); got.added != 0 || got.skipped != 1 {
				t.Fatalf("same request by response ID %+v", got)
			}
			assertAGYLedger(t, database, 1, 110, 40, 1700000100000)
		})
	}
}

func TestAntigravityCrossFileImportAndLedgerMerge(t *testing.T) {
	first := filepath.Join(t.TempDir(), "conversation.db")
	second := filepath.Join(t.TempDir(), "conversation.db")
	agySource(t, first, agyUsage("message", "", false))
	agySource(t, second, agyUsage("message", "response", true))
	database := agyLedger(t)
	if got := agyImport(t, database, first, second); got.added != 1 {
		t.Fatalf("import across source copies %+v", got)
	}
	assertAGYLedger(t, database, 1, 110, 40, 1700000000000)

	destination := agyLedger(t)
	agyImport(t, destination, first)
	incoming := agyLedger(t)
	agyImport(t, incoming, second)
	if result, err := destination.MergeFrom(incoming.Path()); err != nil || result.Added != 0 || result.Updated != 1 {
		t.Fatalf("merge result %+v err=%v", result, err)
	}
	assertAGYLedger(t, destination, 1, 110, 40, 1700000000000)
	if result, err := destination.MergeFrom(incoming.Path()); err != nil || result.Skipped != 1 {
		t.Fatalf("repeat merge result %+v err=%v", result, err)
	}
}

func TestAntigravityUnknownProviderCanBeEnriched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	source := agySource(t, path, agyUsage("message", "", false))
	database := agyLedger(t)
	agyImport(t, database, path)
	var provider string
	if err := database.Conn().QueryRow("SELECT provider FROM usage_events").Scan(&provider); err != nil || provider != "unknown" {
		t.Fatalf("provider=%q err=%v", provider, err)
	}
	stepUsage := append(agyUsage("message", "", false), agyVarint(1, 290)...)
	step := append(agyBytes(1, agyVarint(1, 1700000000)), agyBytes(9, stepUsage)...)
	agyExec(t, source, "INSERT INTO steps VALUES (5, ?)", step)
	if got := agyImport(t, database, path); got.added != 0 || got.updated != 1 {
		t.Fatalf("provider enrichment %+v", got)
	}
	if err := database.Conn().QueryRow("SELECT provider FROM usage_events").Scan(&provider); err != nil || provider != "anthropic" {
		t.Fatalf("provider=%q err=%v", provider, err)
	}
}

func TestAntigravityIncrementalPlaceholderUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	source := agySource(t, path, agyUsage("message", "", false))
	database := agyLedger(t)
	agyImport(t, database, path)

	placeholderStep := append(agyBytes(1, agyVarint(1, 1700000000)), agyBytes(9, append(agyUsage("message", "", false), agyVarint(1, 1427)...))...)
	agyExec(t, source, "INSERT INTO steps VALUES (5, ?)", placeholderStep)
	if got := agyImport(t, database, path); got.added != 0 || got.updated != 1 {
		t.Fatalf("placeholder upgrade %+v", got)
	}
	var modelRaw, modelNorm string
	if err := database.Conn().QueryRow("SELECT model_raw, model_normalized FROM usage_events").Scan(&modelRaw, &modelNorm); err != nil ||
		modelRaw != "model_placeholder_m427" || modelNorm != "model_placeholder_m427" {
		t.Fatalf("model_raw=%q model_normalized=%q err=%v", modelRaw, modelNorm, err)
	}
	if got := agyImport(t, database, path); got.skipped != 1 {
		t.Fatalf("placeholder replay %+v", got)
	}
	if result, err := agyLedger(t).MergeFrom(database.Path()); err != nil || result.Added != 1 {
		t.Fatalf("merge upgraded placeholder row %+v err=%v", result, err)
	}
}

func TestAntigravityRepairsPreviouslyImportedOutput(t *testing.T) {
	for _, test := range []struct {
		name         string
		reported     uint64
		visible      uint64
		reasoning    uint64
		mirror       bool
		oldVisible   int64
		oldReasoning int64
		oldEvidence  model.AntigravityOutputEvidence
		wantTotal    int64
	}{
		{"visible-only mirror", 100, 100, 0, true, 100, 0, model.AntigravityReportedOutputWithSplit, 110},
		{"explicit zero without native ID", 0, 60, 40, false, 0, 0, model.AntigravityReportedOutput, 110},
		{"inconsistent total without native ID", 100, 80, 40, false, 60, 40, model.AntigravityReportedOutputWithPart, 130},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conversation.db")
			usage := append(agyVarint(2, 10), agyVarint(3, test.reported)...)
			usage = append(usage, agyVarint(9, test.reasoning)...)
			usage = append(usage, agyVarint(10, test.visible)...)
			if test.mirror {
				usage = append(usage, agyBytes(7, []byte("message"))...)
			}
			source := agySource(t, path, usage)
			if test.mirror {
				mirror := append(agyUsage("message", "", false), agyVarint(9, 40)...)
				step := append(agyBytes(1, agyVarint(1, 1700000000)), agyBytes(9, mirror)...)
				agyExec(t, source, "INSERT INTO steps VALUES (5, ?)", step)
			}
			current, err := adapters.NewAntigravityAdapter().ParseFile(path)
			if err != nil || len(current) != 1 {
				t.Fatalf("parse records=%d err=%v", len(current), err)
			}
			old := *current[0]
			profile, _ := model.ParseAntigravityAccountingEvidence(old.AccountingProfile)
			old.AccountingProfile = (model.AntigravityAccountingEvidence{Time: profile.Time, Output: test.oldEvidence}).Profile()
			old.OutputTokens, old.ReasoningTokens = test.oldVisible, test.oldReasoning
			old.TotalTokens = 10 + test.oldVisible + test.oldReasoning
			importOld := func(t *testing.T) *db.Database {
				t.Helper()
				database := agyLedger(t)
				seed := old
				added, _, _, rejected, warnings := importParsedRecords(database, "antigravity", []*fingerprint.ParsedRecord{&seed})
				if added != 1 || rejected != 0 || len(warnings) > 0 {
					t.Fatalf("seed legacy row: added=%d rejected=%d warnings=%v", added, rejected, warnings)
				}
				return database
			}

			database := importOld(t)
			if result := agyImport(t, database, path); result.updated != 1 || result.added != 0 {
				t.Fatalf("repair import %+v", result)
			}
			assertAGYLedger(t, database, 1, test.wantTotal, 40, 1700000000000)
			if result := agyImport(t, database, path); result.skipped != 1 {
				t.Fatalf("repeat repair import %+v", result)
			}

			destination := importOld(t)
			if result, err := destination.MergeFrom(database.Path()); err != nil || result.Updated != 1 || result.Added != 0 {
				t.Fatalf("merge repaired row %+v err=%v", result, err)
			}
			assertAGYLedger(t, destination, 1, test.wantTotal, 40, 1700000000000)
			if result, err := database.MergeFrom(importOld(t).Path()); err != nil || result.Skipped != 1 {
				t.Fatalf("merge legacy into repaired %+v err=%v", result, err)
			}
			if !test.mirror {
				other := importOld(t)
				changedInput := append(usage, agyVarint(2, 11)...)
				agyExec(t, source, "UPDATE gen_metadata SET data = ? WHERE idx = 1", agyBytes(1, agyBytes(4, changedInput)))
				result := importAdapterFiles(other, adapters.NewAntigravityAdapter(), []string{path}, time.Time{})
				if result.rejected != 1 || len(result.warnings) != 1 ||
					!strings.Contains(result.warnings[0], "antigravity_row_conflict") {
					t.Fatalf("changed input on unkeyed row %+v", result)
				}
			}
		})
	}
}

func TestAntigravityReimportRepairsPlaceholderModelRaw(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	usage := append(agyUsage("message", "", false), agyVarint(1, 1427)...)
	agySource(t, path, usage)
	records, err := adapters.NewAntigravityAdapter().ParseFile(path)
	if err != nil || len(records) != 1 {
		t.Fatalf("parse records=%d err=%v", len(records), err)
	}
	records[0].Model = "Gemini 4 Hydrogen (Medium)"
	database := agyLedger(t)
	if added, _, _, rejected, warnings := importParsedRecords(database, "antigravity", records); added != 1 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("seed legacy row: added=%d rejected=%d warnings=%v", added, rejected, warnings)
	}
	if result := agyImport(t, database, path); result.updated != 1 {
		t.Fatalf("repair placeholder raw name %+v", result)
	}
	var raw string
	if err := database.Conn().QueryRow("SELECT model_raw FROM usage_events").Scan(&raw); err != nil || raw != "model_placeholder_m427" {
		t.Fatalf("model_raw=%q err=%v", raw, err)
	}
	if result := agyImport(t, database, path); result.skipped != 1 {
		t.Fatalf("repeat repair %+v", result)
	}
}

func TestAntigravityReimportClearsConflictingModelInference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	source := agySource(t, path, nil)
	generation := func(name string) []byte {
		chat := append(agyVarint(3, 1526), agyBytes(19, []byte(name))...)
		return agyBytes(1, chat)
	}
	agyExec(t, source, "UPDATE gen_metadata SET data = ? WHERE idx = 1", generation("Gemini 4 Hydrogen"))
	usage := append(agyUsage("message", "", false), agyVarint(1, 1526)...)
	step := append(agyBytes(1, agyVarint(1, 1700000000)), agyBytes(9, usage)...)
	agyExec(t, source, "INSERT INTO steps VALUES (5, ?)", step)

	database := agyLedger(t)
	agyImport(t, database, path)
	assertModel := func(want, resolution string) {
		t.Helper()
		var got, evidence string
		if err := database.Conn().QueryRow("SELECT model_normalized, model_resolution FROM usage_events").Scan(&got, &evidence); err != nil || got != want || evidence != resolution {
			t.Fatalf("model=%q resolution=%q err=%v, want %q/%q", got, evidence, err, want, resolution)
		}
	}
	assertModel("gemini-4-hydrogen", model.ModelResolutionTurnContext)
	agyExec(t, source, "INSERT INTO gen_metadata VALUES (2, ?)", generation("Gemini 4 Carbon"))
	if result := agyImport(t, database, path); result.updated != 1 || result.added != 0 {
		t.Fatalf("revoke ambiguous inference %+v", result)
	}
	assertModel("model_placeholder_m526", model.ModelResolutionUnknown)
	if result := agyImport(t, database, path); result.skipped != 1 {
		t.Fatalf("repeat ambiguous import %+v", result)
	}
	agyExec(t, source, "DELETE FROM gen_metadata WHERE idx = 2")
	if result := agyImport(t, database, path); result.skipped != 1 {
		t.Fatalf("incomplete snapshot erased conflict %+v", result)
	}
	info := append(agyVarint(1, 1526), agyBytes(12, []byte("Gemini 4 Hydrogen"))...)
	agyExec(t, source, "UPDATE steps SET metadata = ? WHERE idx = 5", append(step, agyBytes(24, info)...))
	if result := agyImport(t, database, path); result.updated != 1 {
		t.Fatalf("direct model evidence %+v", result)
	}
	assertModel("gemini-4-hydrogen", model.ModelResolutionDirectEvent)
}

func agyImportPreview(t *testing.T, database *db.Database, id string) {
	t.Helper()
	record := &fingerprint.ParsedRecord{
		Agent: "antigravity", SourceProduct: "antigravity", Provider: "google",
		Model: "gemini-2.5-pro", ModelNormalized: "gemini-2.5-pro", ModelResolution: model.ModelResolutionDirectEvent,
		SessionID: "conversation", NativeSessionID: "conversation", NativeEventID: id, MessageID: id,
		IdentityScope: "global", IdentityKind: "message", ParserVersion: "antigravity-v1", Granularity: "request",
		TimestampMs: 1700000000000, InputTokens: 10, OutputTokens: 100, ReasoningTokens: 40, TotalTokens: 150,
		TokenAccountingMethod: model.AccAntigravityModelUsage, AccountingProfile: model.AntigravityProfile,
	}
	if id == "" {
		record.IdentityScope, record.IdentityKind, record.IdentitySubkey = "session", "record", "record"
	}
	added, _, _, rejected, warnings := importParsedRecords(database, "antigravity", []*fingerprint.ParsedRecord{record})
	if added != 1 || rejected != 0 || len(warnings) != 0 {
		t.Fatalf("preview import added=%d rejected=%d warnings=%v", added, rejected, warnings)
	}
}

func TestAntigravityReimportPromotesProvenPreviewRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	source := agySource(t, path, agyUsage("message", "response", true))
	step := append(agyBytes(1, agyVarint(1, 1700000100)), agyBytes(9, agyUsage("message", "response", true))...)
	agyExec(t, source, "INSERT INTO steps VALUES (5, ?)", step)

	database := agyLedger(t)
	agyImportPreview(t, database, "message")
	if got := agyImport(t, database, path); got.added != 0 || got.updated != 1 {
		t.Fatalf("preview upgrade %+v", got)
	}
	assertAGYLedger(t, database, 1, 110, 40, 1700000100000)
	var eventID, parser string
	if err := database.Conn().QueryRow("SELECT event_id, parser_version FROM usage_events").Scan(&eventID, &parser); err != nil {
		t.Fatal(err)
	}
	if eventID != fingerprint.AntigravityNativeEventID("", "message") || parser != model.AntigravityParser {
		t.Fatalf("event identity or version changed unexpectedly")
	}
	if got := agyImport(t, database, path); got.skipped != 1 {
		t.Fatalf("preview upgrade replay %+v", got)
	}
}

func TestAntigravityAmbiguousPreviewAliasesRemainVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	agySource(t, path, agyUsage("message", "response", true))
	database := agyLedger(t)
	agyImportPreview(t, database, "message")
	agyImportPreview(t, database, "response")
	result := importAdapterFiles(database, adapters.NewAntigravityAdapter(), []string{path}, time.Time{})
	if result.rejected != 1 || result.added != 0 || result.updated != 0 || len(result.warnings) != 1 {
		t.Fatalf("ambiguous preview import %+v", result)
	}
	assertAGYLedger(t, database, 2, 300, 80, 1700000000000)
}

func TestAntigravityNativeNamespacesKeepDistinctRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	source := agySource(t, path, agyUsage("same-text", "", false))
	step := append(agyBytes(1, agyVarint(1, 1700000100)), agyBytes(9, agyUsage("", "same-text", true))...)
	agyExec(t, source, "INSERT INTO steps VALUES (6, ?)", step)
	database := agyLedger(t)
	got := agyImport(t, database, path)
	if got.added != 2 {
		t.Fatalf("namespaces collapsed %+v", got)
	}
	assertAGYLedger(t, database, 2, 220, 40, 1700000100000)
}

func TestAntigravityAmbiguousPreviewRetryDoesNotAddAnotherRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	source := agySource(t, path, agyUsage("primary", "", false))
	retry := agyBytes(17, agyBytes(2, agyVarint(2, 14)))
	chat := append(agyBytes(4, agyUsage("primary", "", false)), retry...)
	agyExec(t, source, "UPDATE gen_metadata SET data = ? WHERE idx = 1", agyBytes(1, chat))
	database := agyLedger(t)
	agyImportPreview(t, database, "")
	result := importAdapterFiles(database, adapters.NewAntigravityAdapter(), []string{path}, time.Time{})
	if result.added != 1 || result.rejected != 1 || len(result.warnings) != 1 ||
		!strings.Contains(result.warnings[0], "antigravity_preview_retry_ambiguous") {
		t.Fatalf("anonymous preview retry import %+v", result)
	}
	assertAGYLedger(t, database, 2, 260, 40, 1700000000000)
}

func TestAntigravityFoldsTransitiveAliasesBeforeWriting(t *testing.T) {
	message := filepath.Join(t.TempDir(), "conversation.db")
	response := filepath.Join(t.TempDir(), "conversation.db")
	bridge := filepath.Join(t.TempDir(), "conversation.db")
	agySource(t, message, agyUsage("message", "", false))
	agySource(t, response, agyUsage("", "response", false))
	agySource(t, bridge, agyUsage("message", "response", true))

	database := agyLedger(t)
	if got := agyImport(t, database, message, response, bridge); got.added != 1 {
		t.Fatalf("import across three source copies %+v", got)
	}
	assertAGYLedger(t, database, 1, 110, 40, 1700000000000)
}

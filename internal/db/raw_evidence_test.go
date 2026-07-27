package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
	"github.com/BlueSkyXN/AgentLedger/internal/usageevidence"
)

const compactableCodexRaw = `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1,"output_tokens":2}}},"private":"omit"}`

func TestCompactRawEvidenceDryRunApplyPreservesNonRawColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, event := range []*model.UsageEvent{
		rawEvidenceEvent("compact", "message_id", compactableCodexRaw),
		rawEvidenceEvent("unknown", "message_id", `{"future":true}`),
		rawEvidenceEvent("raw-hash", "raw_hash", compactableCodexRaw),
		rawEvidenceEvent("fallback", "fallback", compactableCodexRaw),
		rawEvidenceEvent("empty", "message_id", ""),
	} {
		if err := insertEvent(database.Conn(), event); err != nil {
			_ = database.Close()
			t.Fatalf("insert %s: %v", event.EventID, err)
		}
	}
	if err := database.StartImportRun("raw-evidence-run"); err != nil {
		_ = database.Close()
		t.Fatalf("start import run: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	beforeEvents := tableSnapshot(t, path, "usage_events", "raw_usage_json")
	beforeMeta := tableSnapshot(t, path, "meta")
	beforeRuns := tableSnapshot(t, path, "import_runs")
	reader, err := OpenReadOnlyV2(path)
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	dryRun, err := reader.InspectRawEvidence()
	if err != nil {
		_ = reader.Close()
		t.Fatalf("inspect: %v", err)
	}
	if dryRun.Candidates != 1 || dryRun.UnknownPreserved != 1 || dryRun.IdentityProtected != 2 || dryRun.Empty != 1 || dryRun.Updated != 0 {
		_ = reader.Close()
		t.Fatalf("unexpected dry-run stats: %+v", dryRun)
	}
	if _, err := reader.Conn().Exec(`UPDATE usage_events SET raw_usage_json = 'x'`); err == nil {
		_ = reader.Close()
		t.Fatal("dry-run connection accepted a write")
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close read-only: %v", err)
	}
	if afterDryRun := tableSnapshot(t, path, "usage_events", "raw_usage_json"); !reflect.DeepEqual(afterDryRun, beforeEvents) {
		t.Fatal("dry-run changed usage_events")
	}

	writer, err := OpenReadWriteV2(path)
	if err != nil {
		t.Fatalf("open strict writer: %v", err)
	}
	apply, err := writer.CompactRawEvidence()
	if err != nil {
		_ = writer.Close()
		t.Fatalf("apply: %v stats=%+v", err, apply)
	}
	if apply.Updated != 1 || apply.BatchesCompleted != 1 || apply.RemainingCandidates != 0 {
		_ = writer.Close()
		t.Fatalf("unexpected apply stats: %+v", apply)
	}
	second, err := writer.CompactRawEvidence()
	if err != nil {
		_ = writer.Close()
		t.Fatalf("second apply: %v stats=%+v", err, second)
	}
	if second.Candidates != 0 || second.Updated != 0 || second.RemainingCandidates != 0 {
		_ = writer.Close()
		t.Fatalf("compaction was not idempotent: %+v", second)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close strict writer: %v", err)
	}

	afterEvents := tableSnapshot(t, path, "usage_events", "raw_usage_json")
	if !reflect.DeepEqual(afterEvents, beforeEvents) {
		t.Fatal("compact changed a non-raw usage_events column")
	}
	if afterMeta := tableSnapshot(t, path, "meta"); !reflect.DeepEqual(afterMeta, beforeMeta) {
		t.Fatal("compact changed meta")
	}
	if afterRuns := tableSnapshot(t, path, "import_runs"); !reflect.DeepEqual(afterRuns, beforeRuns) {
		t.Fatal("compact changed import_runs")
	}

	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open assertions: %v", err)
	}
	defer conn.Close()
	raws := map[string]sql.NullString{}
	rows, err := conn.Query(`SELECT event_id, raw_usage_json FROM usage_events`)
	if err != nil {
		t.Fatalf("query raw: %v", err)
	}
	for rows.Next() {
		var eventID string
		var raw sql.NullString
		if err := rows.Scan(&eventID, &raw); err != nil {
			_ = rows.Close()
			t.Fatalf("scan raw: %v", err)
		}
		raws[eventID] = raw
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close raw rows: %v", err)
	}
	if !raws["compact"].Valid || !usageevidence.IsCompact("codex", raws["compact"].String) {
		t.Fatalf("recognized raw was not compacted: %+v", raws["compact"])
	}
	for _, id := range []string{"unknown", "raw-hash", "fallback"} {
		if !raws[id].Valid || raws[id].String != map[string]string{"unknown": `{"future":true}`, "raw-hash": compactableCodexRaw, "fallback": compactableCodexRaw}[id] {
			t.Fatalf("protected raw changed for %s", id)
		}
	}
	if raws["empty"].Valid {
		t.Fatalf("empty raw must be stored as NULL, got %+v", raws["empty"])
	}
}

func TestCodexReimportDoesNotRestoreCompactEvidence(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	stored := rawEvidenceEvent("codex-reimport", "message_id", compactableCodexRaw)
	stored.MessageID = "codex-reimport"
	stored.SourceFile = "/synthetic/codex.jsonl"
	stored.LineNumber = 7
	stored.RawSHA256 = "source-hash"
	stored.ModelRaw = "unknown"
	stored.ModelNormalized = "unknown"
	stored.ModelIsFallback = true
	stored.ModelResolution = model.ModelResolutionUnknown
	setUsageEventFingerprintForTest(stored)
	if err := insertEvent(database.Conn(), stored); err != nil {
		t.Fatalf("insert stored: %v", err)
	}
	if _, err := database.Conn().Exec(`UPDATE usage_events SET raw_usage_json = agentledger_compact_raw_evidence(channel, raw_usage_json)`); err != nil {
		t.Fatalf("compact stored: %v", err)
	}
	var compacted string
	if err := database.Conn().QueryRow(`SELECT raw_usage_json FROM usage_events`).Scan(&compacted); err != nil {
		t.Fatalf("select compact stored: %v", err)
	}
	if !usageevidence.IsCompact("codex", compacted) {
		t.Fatalf("stored evidence did not compact: %q", compacted)
	}
	incoming := *stored
	incoming.ModelRaw = "gpt-5-codex"
	incoming.ModelNormalized = "gpt-5-codex"
	incoming.ModelIsFallback = false
	incoming.ModelResolution = model.ModelResolutionDirectEvent
	incoming.UpdatedAtMs = 2
	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("reimport status=%s err=%v", status, err)
	}
	var raw, modelName string
	if err := database.Conn().QueryRow(`SELECT raw_usage_json, model_normalized FROM usage_events`).Scan(&raw, &modelName); err != nil {
		t.Fatalf("select reimport: %v", err)
	}
	if !usageevidence.IsCompact("codex", raw) || modelName != "gpt-5-codex" {
		t.Fatalf("reimport restored full raw or lost correction: raw=%q model=%q", raw, modelName)
	}
}

func TestBuildCanonicalReconciledEventPreservesProtectedIdentity(t *testing.T) {
	compact := usageevidence.Compact("codex", compactableCodexRaw)
	if compact.Status != usageevidence.StatusRecognizedLegacy {
		t.Fatalf("test fixture did not compact: %+v", compact)
	}
	for _, strategy := range []string{"raw_hash", "fallback"} {
		t.Run(strategy, func(t *testing.T) {
			stored := rawEvidenceEvent("protected-"+strategy, strategy, compact.JSON)
			stored.DedupeKey = stored.EventID
			incoming := *stored
			incoming.RawUsageJSON = compactableCodexRaw
			incoming.ModelRaw = "gpt-5-codex-updated"
			incoming.ModelNormalized = "gpt-5-codex-updated"

			canonical := buildCanonicalReconciledEvent(&incoming, stored, []*model.UsageEvent{stored})
			if canonical.EventID != stored.EventID || canonical.DedupeKey != stored.DedupeKey || canonical.DedupeStrategy != strategy {
				t.Fatalf("protected identity was recomputed: %+v", canonical)
			}
			if !usageevidence.IsCompact("codex", canonical.RawUsageJSON) {
				t.Fatal("canonical record restored full raw evidence")
			}
		})
	}
}

func TestMergeCompactsLegacyOmitsUnknownAndRollsBackOnSQLError(t *testing.T) {
	incomingPath := filepath.Join(t.TempDir(), "incoming.db")
	incoming, err := Open(incomingPath)
	if err != nil {
		t.Fatalf("open incoming: %v", err)
	}
	for _, event := range []*model.UsageEvent{
		rawEvidenceEvent("merge-recognized", "message_id", compactableCodexRaw),
		rawEvidenceEvent("merge-unknown", "message_id", `{"future":true}`),
		rawEvidenceEvent("merge-raw-hash", "raw_hash", compactableCodexRaw),
		rawEvidenceEvent("merge-fallback", "fallback", compactableCodexRaw),
	} {
		if err := insertEvent(incoming.Conn(), event); err != nil {
			_ = incoming.Close()
			t.Fatalf("insert incoming %s: %v", event.EventID, err)
		}
	}
	if err := incoming.Close(); err != nil {
		t.Fatalf("close incoming: %v", err)
	}

	destination, err := Open(filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatalf("open destination: %v", err)
	}
	defer destination.Close()
	result, err := destination.MergeFrom(incomingPath)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if result.Inserted != 4 || result.Skipped != 0 || result.RawEvidenceOmitted != 1 {
		t.Fatalf("unexpected merge result: %+v", result)
	}
	var recognized, unknown sql.NullString
	if err := destination.Conn().QueryRow(`SELECT raw_usage_json FROM usage_events WHERE event_id = 'merge-recognized'`).Scan(&recognized); err != nil {
		t.Fatalf("select recognized: %v", err)
	}
	if err := destination.Conn().QueryRow(`SELECT raw_usage_json FROM usage_events WHERE event_id = 'merge-unknown'`).Scan(&unknown); err != nil {
		t.Fatalf("select unknown: %v", err)
	}
	if !recognized.Valid || !usageevidence.IsCompact("codex", recognized.String) || unknown.Valid {
		t.Fatalf("merge raw evidence mismatch recognized=%+v unknown=%+v", recognized, unknown)
	}
	for eventID, wantStrategy := range map[string]string{"merge-raw-hash": "raw_hash", "merge-fallback": "fallback"} {
		var raw, strategy, dedupeKey string
		if err := destination.Conn().QueryRow(`SELECT raw_usage_json, dedupe_strategy, dedupe_key FROM usage_events WHERE event_id = ?`, eventID).Scan(&raw, &strategy, &dedupeKey); err != nil {
			t.Fatalf("select protected incoming identity: %v", err)
		}
		if !usageevidence.IsCompact("codex", raw) || strategy != wantStrategy || dedupeKey != eventID {
			t.Fatalf("merge changed protected identity event=%q strategy=%q dedupe=%q raw=%q", eventID, strategy, dedupeKey, raw)
		}
	}
	duplicate, err := destination.MergeFrom(incomingPath)
	if err != nil {
		t.Fatalf("duplicate merge: %v", err)
	}
	if duplicate.Inserted != 0 || duplicate.Skipped != 4 || duplicate.RawEvidenceOmitted != 0 {
		t.Fatalf("duplicate merge changed data: %+v", duplicate)
	}

	failingPath := filepath.Join(t.TempDir(), "failing-merge.db")
	failing, err := Open(failingPath)
	if err != nil {
		t.Fatalf("open failing incoming: %v", err)
	}
	if err := insertEvent(failing.Conn(), rawEvidenceEvent("rollback-good", "message_id", compactableCodexRaw)); err != nil {
		_ = failing.Close()
		t.Fatalf("insert valid rollback row: %v", err)
	}
	if err := insertEvent(failing.Conn(), rawEvidenceEvent("rollback-unknown", "message_id", `{"future":true}`)); err != nil {
		_ = failing.Close()
		t.Fatalf("insert unknown rollback row: %v", err)
	}
	if err := failing.Close(); err != nil {
		t.Fatalf("close failing incoming: %v", err)
	}
	if _, err := destination.Conn().Exec(`
		CREATE TRIGGER reject_rollback_merge
		BEFORE INSERT ON usage_events
		WHEN NEW.event_id LIKE 'rollback-%'
		BEGIN
			SELECT RAISE(ABORT, 'test merge failure');
		END
	`); err != nil {
		t.Fatalf("create merge failure trigger: %v", err)
	}
	if _, err := destination.MergeFrom(failingPath); err == nil {
		t.Fatal("merge accepted a forced SQL write failure")
	}
	var count int
	if err := destination.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events WHERE event_id LIKE 'rollback-%'`).Scan(&count); err != nil {
		t.Fatalf("count rollback rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("merge left partial rows after internal evidence failure: %d", count)
	}
}

func rawEvidenceEvent(id, strategy, raw string) *model.UsageEvent {
	return &model.UsageEvent{
		EventID:         id,
		DedupeKey:       id,
		DedupeStrategy:  strategy,
		Channel:         "codex",
		Provider:        "openai",
		ModelRaw:        "gpt-5-codex",
		ModelNormalized: "gpt-5-codex",
		TimestampMs:     1,
		SessionID:       "session-" + id,
		SourceFile:      "/synthetic/" + id + ".jsonl",
		LineNumber:      1,
		RawSHA256:       "hash-" + id,
		InputTokens:     1,
		OutputTokens:    2,
		TotalTokens:     3,
		RawUsageJSON:    raw,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
}

func tableSnapshot(t *testing.T, path, table string, excluded ...string) []string {
	t.Helper()
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open %s snapshot: %v", table, err)
	}
	defer conn.Close()
	rows, err := conn.Query(fmt.Sprintf("SELECT * FROM %s ORDER BY rowid", table))
	if err != nil {
		t.Fatalf("query %s snapshot: %v", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns %s snapshot: %v", table, err)
	}
	excludedSet := map[string]bool{}
	for _, column := range excluded {
		excludedSet[column] = true
	}
	var snapshot []string
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatalf("scan %s snapshot: %v", table, err)
		}
		row := ""
		for i, column := range columns {
			if !excludedSet[column] {
				row += fmt.Sprintf("%s=%T:%v;", column, values[i], values[i])
			}
		}
		snapshot = append(snapshot, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s snapshot: %v", table, err)
	}
	return snapshot
}

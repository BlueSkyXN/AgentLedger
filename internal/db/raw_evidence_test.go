package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const legacyRawUsage = `{"source":"legacy","private":"must-not-persist"}`

func TestCompactRawEvidenceDryRunApplyClearsEveryNonNullValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, event := range []*model.UsageEvent{
		rawEvidenceEvent("legacy", "message_id", legacyRawUsage),
		rawEvidenceEvent("compact", "message_id", `{"version":1}`),
		rawEvidenceEvent("raw-hash", "raw_hash", legacyRawUsage),
		rawEvidenceEvent("fallback", "fallback", legacyRawUsage),
		rawEvidenceEvent("empty", "message_id", ""),
	} {
		if err := insertEvent(database.Conn(), event); err != nil {
			_ = database.Close()
			t.Fatalf("insert %s: %v", event.EventID, err)
		}
	}
	if _, err := database.Conn().Exec(`UPDATE usage_events SET raw_usage_json = '' WHERE event_id = 'empty'`); err != nil {
		_ = database.Close()
		t.Fatalf("seed empty raw: %v", err)
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
	if dryRun.Candidates != 5 || dryRun.AlreadyNull != 0 || dryRun.RawBytesBefore <= 0 || dryRun.RawBytesAfter != 0 || dryRun.Updated != 0 || dryRun.RemainingCandidates != 5 {
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
	if apply.Candidates != 5 || apply.Updated != 5 || apply.BatchesCompleted != 1 || apply.RemainingCandidates != 0 || apply.RawBytesAfter != 0 {
		_ = writer.Close()
		t.Fatalf("unexpected apply stats: %+v", apply)
	}
	second, err := writer.CompactRawEvidence()
	if err != nil {
		_ = writer.Close()
		t.Fatalf("second apply: %v stats=%+v", err, second)
	}
	if second.Candidates != 0 || second.Updated != 0 || second.RemainingCandidates != 0 || second.AlreadyNull != 5 {
		_ = writer.Close()
		t.Fatalf("cleanup was not idempotent: %+v", second)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close strict writer: %v", err)
	}

	afterEvents := tableSnapshot(t, path, "usage_events", "raw_usage_json")
	if !reflect.DeepEqual(afterEvents, beforeEvents) {
		t.Fatal("cleanup changed a non-raw usage_events column")
	}
	if afterMeta := tableSnapshot(t, path, "meta"); !reflect.DeepEqual(afterMeta, beforeMeta) {
		t.Fatal("cleanup changed meta")
	}
	if afterRuns := tableSnapshot(t, path, "import_runs"); !reflect.DeepEqual(afterRuns, beforeRuns) {
		t.Fatal("cleanup changed import_runs")
	}
	assertNoRawUsage(t, path)
}

func TestInspectRawEvidenceCountsUTF8Bytes(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	event := rawEvidenceEvent("unicode", "message_id", "使用量")
	if err := insertEvent(database.Conn(), event); err != nil {
		t.Fatalf("insert unicode raw: %v", err)
	}
	stats, err := database.InspectRawEvidence()
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if stats.Candidates != 1 || stats.RawBytesBefore != int64(len(event.RawUsageJSON)) {
		t.Fatalf("UTF-8 byte count mismatch stats=%+v raw=%q", stats, event.RawUsageJSON)
	}
}

func TestCompactRawEvidenceCASRejectsConcurrentChange(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	first := rawEvidenceEvent("cas-first", "message_id", "first-original")
	second := rawEvidenceEvent("cas-second", "message_id", "second-original")
	for _, event := range []*model.UsageEvent{first, second} {
		if err := insertEvent(database.Conn(), event); err != nil {
			t.Fatalf("insert %s: %v", event.EventID, err)
		}
	}
	if _, err := database.Conn().Exec(`UPDATE usage_events SET raw_usage_json = 'changed' WHERE event_id = ?`, second.EventID); err != nil {
		t.Fatalf("change raw: %v", err)
	}
	err = database.applyRawEvidenceBatch([]rawEvidenceCandidate{
		{eventID: first.EventID, raw: first.RawUsageJSON},
		{eventID: second.EventID, raw: second.RawUsageJSON},
	})
	if err == nil {
		t.Fatal("CAS update accepted changed raw value")
	}
	for eventID, want := range map[string]string{first.EventID: "first-original", second.EventID: "changed"} {
		var raw string
		if err := database.Conn().QueryRow(`SELECT raw_usage_json FROM usage_events WHERE event_id = ?`, eventID).Scan(&raw); err != nil || raw != want {
			t.Fatalf("CAS failure did not roll back batch event=%s raw=%q want=%q err=%v", eventID, raw, want, err)
		}
	}
}

func TestUpsertEventClearsLegacyRawOnExactReimport(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	stored := rawEvidenceEvent("exact-reimport", "message_id", legacyRawUsage)
	if err := insertEvent(database.Conn(), stored); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	incoming := *stored
	incoming.RawUsageJSON = `{"new":"source envelope"}`
	status, err := database.UpsertEvent(&incoming)
	if err != nil || status != "updated" {
		t.Fatalf("exact reimport status=%q err=%v", status, err)
	}
	var raw sql.NullString
	if err := database.Conn().QueryRow(`SELECT raw_usage_json FROM usage_events WHERE event_id = ?`, stored.EventID).Scan(&raw); err != nil {
		t.Fatalf("read cleaned raw: %v", err)
	}
	if raw.Valid {
		t.Fatalf("exact reimport retained raw: %+v", raw)
	}
}

func TestBuildCanonicalReconciledEventPreservesProtectedIdentity(t *testing.T) {
	stored := rawEvidenceEvent("stored-raw-hash", "raw_hash", legacyRawUsage)
	incoming := *stored
	incoming.EventID = "incoming-message-id"
	incoming.DedupeKey = incoming.EventID
	incoming.DedupeStrategy = "message_id"
	incoming.RawUsageJSON = `{"incoming":"raw"}`
	canonical := buildCanonicalReconciledEvent(&incoming, stored, []*model.UsageEvent{stored})
	if canonical.EventID != stored.EventID || canonical.DedupeKey != stored.DedupeKey || canonical.DedupeStrategy != "raw_hash" {
		t.Fatalf("stored protected identity was replaced: %+v", canonical)
	}
	if canonical.RawUsageJSON != "" {
		t.Fatalf("canonical reconciliation retained raw: %q", canonical.RawUsageJSON)
	}

	protectedIncoming := *stored
	protectedIncoming.EventID = "incoming-fallback"
	protectedIncoming.DedupeKey = protectedIncoming.EventID
	protectedIncoming.DedupeStrategy = "fallback"
	canonical = buildCanonicalReconciledEvent(&protectedIncoming, nil, nil)
	if canonical.EventID != protectedIncoming.EventID || canonical.DedupeStrategy != "fallback" || canonical.RawUsageJSON != "" {
		t.Fatalf("incoming protected identity was recomputed: %+v", canonical)
	}
}

func TestMergeFromDropsEveryIncomingRawValue(t *testing.T) {
	incomingPath := filepath.Join(t.TempDir(), "incoming.db")
	incoming, err := Open(incomingPath)
	if err != nil {
		t.Fatalf("open incoming: %v", err)
	}
	events := []*model.UsageEvent{
		rawEvidenceEvent("merge-legacy", "message_id", legacyRawUsage),
		rawEvidenceEvent("merge-compact", "message_id", `{"version":1}`),
		rawEvidenceEvent("merge-raw-hash", "raw_hash", legacyRawUsage),
		rawEvidenceEvent("merge-fallback", "fallback", legacyRawUsage),
		rawEvidenceEvent("merge-invalid", "message_id", `not-json`),
		rawEvidenceEvent("merge-empty", "message_id", "placeholder"),
	}
	for i, channel := range []string{"claude", "codex", "gemini", "copilot", "workbuddy", "copilot"} {
		events[i].Channel = channel
		events[i].Provider = channel + "-provider"
	}
	for _, event := range events {
		if err := insertEvent(incoming.Conn(), event); err != nil {
			_ = incoming.Close()
			t.Fatalf("insert incoming %s: %v", event.EventID, err)
		}
	}
	// insertEvent nullifies empty raw; force a genuine empty-string legacy value.
	if _, err := incoming.Conn().Exec(`UPDATE usage_events SET raw_usage_json = '' WHERE event_id = 'merge-empty'`); err != nil {
		_ = incoming.Close()
		t.Fatalf("force empty raw: %v", err)
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
	if result.Inserted != 6 || result.Skipped != 0 || result.RawEvidenceOmitted != 6 {
		t.Fatalf("unexpected merge result: %+v", result)
	}
	var retained int
	if err := destination.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events WHERE raw_usage_json IS NOT NULL`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("merge retained raw count=%d err=%v", retained, err)
	}
	duplicate, err := destination.MergeFrom(incomingPath)
	if err != nil {
		t.Fatalf("duplicate merge: %v", err)
	}
	if duplicate.Inserted != 0 || duplicate.Skipped != 6 || duplicate.RawEvidenceOmitted != 0 {
		t.Fatalf("duplicate merge changed data: %+v", duplicate)
	}
}

func TestMergeFromRollsBackEntireTransactionOnSQLError(t *testing.T) {
	destination, err := Open(filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatalf("open destination: %v", err)
	}
	defer destination.Close()

	failingPath := filepath.Join(t.TempDir(), "failing-merge.db")
	failing, err := Open(failingPath)
	if err != nil {
		t.Fatalf("open failing incoming: %v", err)
	}
	for _, event := range []*model.UsageEvent{
		rawEvidenceEvent("rollback-good", "message_id", legacyRawUsage),
		rawEvidenceEvent("rollback-also", "message_id", `{"version":1}`),
	} {
		if err := insertEvent(failing.Conn(), event); err != nil {
			_ = failing.Close()
			t.Fatalf("insert failing row %s: %v", event.EventID, err)
		}
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
		t.Fatalf("merge left partial rows after forced SQL failure: %d", count)
	}
}

func TestCompactRawEvidenceMultiBatchInterruptAndRerun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	const total = rawEvidenceBatchSize + 3
	for i := 0; i < total; i++ {
		event := rawEvidenceEvent(fmt.Sprintf("batch-%04d", i), "message_id", fmt.Sprintf(`{"n":%d}`, i))
		if err := insertEvent(database.Conn(), event); err != nil {
			_ = database.Close()
			t.Fatalf("insert batch-%04d: %v", i, err)
		}
	}
	// Fail only the first event of the second keyset batch.
	failEventID := fmt.Sprintf("batch-%04d", rawEvidenceBatchSize)
	if _, err := database.Conn().Exec(fmt.Sprintf(`
		CREATE TRIGGER reject_second_batch
		BEFORE UPDATE ON usage_events
		WHEN NEW.event_id = '%s'
		BEGIN
			SELECT RAISE(ABORT, 'test compact batch failure');
		END
	`, failEventID)); err != nil {
		_ = database.Close()
		t.Fatalf("create compact failure trigger: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close seeder: %v", err)
	}

	writer, err := OpenReadWriteV2(path)
	if err != nil {
		t.Fatalf("open strict writer: %v", err)
	}
	first, err := writer.CompactRawEvidence()
	if err == nil {
		_ = writer.Close()
		t.Fatalf("expected multi-batch interrupt, stats=%+v", first)
	}
	if first.Updated != int64(rawEvidenceBatchSize) || first.BatchesCompleted != 1 {
		_ = writer.Close()
		t.Fatalf("first batch should commit before interrupt: %+v err=%v", first, err)
	}
	var remaining int
	if err := writer.Conn().QueryRow(`SELECT COUNT(*) FROM usage_events WHERE raw_usage_json IS NOT NULL`).Scan(&remaining); err != nil {
		_ = writer.Close()
		t.Fatalf("count remaining after interrupt: %v", err)
	}
	if remaining != 3 {
		_ = writer.Close()
		t.Fatalf("remaining candidates after interrupt=%d want=3", remaining)
	}
	if _, err := writer.Conn().Exec(`DROP TRIGGER reject_second_batch`); err != nil {
		_ = writer.Close()
		t.Fatalf("drop interrupt trigger: %v", err)
	}
	second, err := writer.CompactRawEvidence()
	if err != nil {
		_ = writer.Close()
		t.Fatalf("rerun after interrupt: %v stats=%+v", err, second)
	}
	if second.Updated != 3 || second.RemainingCandidates != 0 {
		_ = writer.Close()
		t.Fatalf("rerun should finish remaining rows: %+v", second)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	assertNoRawUsage(t, path)
}

func rawEvidenceEvent(id, strategy, raw string) *model.UsageEvent {
	return &model.UsageEvent{
		EventID: id, DedupeKey: id, DedupeStrategy: strategy,
		Channel: "codex", Provider: "openai", ModelRaw: "gpt-5-codex", ModelNormalized: "gpt-5-codex",
		TimestampMs: 1, SessionID: "session-" + id, SourceFile: "/synthetic/" + id + ".jsonl", LineNumber: 1, RawSHA256: "hash-" + id,
		InputTokens: 1, OutputTokens: 2, TotalTokens: 3, RawUsageJSON: raw, ImportedAtMs: 1, UpdatedAtMs: 1,
	}
}

func assertNoRawUsage(t *testing.T, path string) {
	t.Helper()
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open assertion connection: %v", err)
	}
	defer conn.Close()
	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM usage_events WHERE raw_usage_json IS NOT NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stored raw count=%d err=%v", count, err)
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

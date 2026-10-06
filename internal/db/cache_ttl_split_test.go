package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func cacheSplitEvent(t *testing.T, id string, oneHour *int64) *model.UsageEvent {
	t.Helper()
	event := testEvent(id, "", 0)
	event.Channel = "claude"
	event.SourceProduct = "claude-code"
	event.Provider = "anthropic"
	event.ModelRaw = "claude-opus-5"
	event.ModelNormalized = "claude-opus-5"
	event.ModelResolution = model.ModelResolutionDirectEvent
	event.ModelIsFallback = false
	event.InputTokens = 10
	event.CacheCreationTokens = 500
	event.TotalTokens = 510
	event.CacheCreation1hTokens = oneHour
	event.ContentSHA256 = mustContentSHA256(t, event)
	return event
}

func int64Value(value int64) *int64 {
	return &value
}

// writeLegacyV3Database creates a database with the exact v3 layout (no cache
// TTL split column) holding the given events.
func writeLegacyV3Database(t *testing.T, path string, events ...*model.UsageEvent) {
	t.Helper()
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if _, err := database.UpsertEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Conn().Exec(`ALTER TABLE usage_events DROP COLUMN cache_creation_1h_tokens`); err != nil {
		t.Fatalf("drop v4 column: %v", err)
	}
	if _, err := database.Conn().Exec(`UPDATE meta SET value='3' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func rawSchemaVersion(t *testing.T, path string) string {
	t.Helper()
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var version string
	if err := conn.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestOpenMigratesLegacyV3DatabaseToV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy := cacheSplitEvent(t, "legacy", nil)
	writeLegacyV3Database(t, path, legacy)

	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open(v3) should migrate: %v", err)
	}
	defer database.Close()
	if version := rawSchemaVersion(t, path); version != SchemaVersion {
		t.Fatalf("schema version after migration = %q", version)
	}
	columns, err := tableColumns(database.Conn(), "usage_events")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := columns["cache_creation_1h_tokens"]; !ok {
		t.Fatal("migration did not add cache_creation_1h_tokens")
	}
	stored, err := selectEvent(database.Conn(), legacy.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheCreation1hTokens != nil {
		t.Fatalf("migrated row must keep an unknown split, got %d", *stored.CacheCreation1hTokens)
	}
	if err := validateStoredEvent(stored); err != nil {
		t.Fatalf("migrated row content hash no longer validates: %v", err)
	}

	// Re-importing the same event with a known split fills the unknown value.
	refreshed := cacheSplitEvent(t, "legacy", int64Value(120))
	status, err := database.UpsertEvent(refreshed)
	if err != nil || status != ReconcileUpdated {
		t.Fatalf("refill status=%q err=%v", status, err)
	}
	stored, err = selectEvent(database.Conn(), legacy.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheCreation1hTokens == nil || *stored.CacheCreation1hTokens != 120 || stored.ContentSHA256 != mustContentSHA256(t, stored) {
		t.Fatalf("refilled split not persisted consistently: %+v", stored)
	}
}

func TestReadOnlyOpenAcceptsLegacyV3WithoutMigrating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy := cacheSplitEvent(t, "legacy", nil)
	writeLegacyV3Database(t, path, legacy)

	database, err := OpenReadOnlyV3(path)
	if err != nil {
		t.Fatalf("read-only open of v3: %v", err)
	}
	if database.hasCacheCreation1hColumn() {
		t.Fatal("v3 database must report no cache TTL split column")
	}
	events, err := listEvents(database.Conn(), database.hasCacheCreation1hColumn())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].CacheCreation1hTokens != nil {
		t.Fatalf("unexpected v3 events: %+v", events)
	}
	stats, err := database.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["schema_version"] != legacySchemaVersionV3 {
		t.Fatalf("stats should report the actual legacy schema, got %v", stats["schema_version"])
	}
	_ = database.Close()
	if version := rawSchemaVersion(t, path); version != legacySchemaVersionV3 {
		t.Fatalf("read-only open mutated schema version to %q", version)
	}
}

func TestReadWriteV3RejectsLegacyV3WithoutMigrating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	writeLegacyV3Database(t, path, cacheSplitEvent(t, "legacy", nil))

	_, err := OpenReadWriteV3(path)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("OpenReadWriteV3(v3) error = %v", err)
	}
	if version := rawSchemaVersion(t, path); version != legacySchemaVersionV3 {
		t.Fatalf("rejected open mutated schema version to %q", version)
	}
}

func TestReconcileCacheTTLSplitConflictsAndValidation(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()

	known := cacheSplitEvent(t, "split", int64Value(120))
	if status, err := database.UpsertEvent(known); err != nil || status != ReconcileInserted {
		t.Fatalf("insert status=%q err=%v", status, err)
	}

	// An unknown split from an older source never erases the known value.
	unknown := cacheSplitEvent(t, "split", nil)
	if status, err := database.UpsertEvent(unknown); err != nil || status == ReconcileRejected {
		t.Fatalf("unknown split status=%q err=%v", status, err)
	}
	stored, err := selectEvent(database.Conn(), known.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheCreation1hTokens == nil || *stored.CacheCreation1hTokens != 120 {
		t.Fatalf("known split was lost: %+v", stored.CacheCreation1hTokens)
	}

	conflict := cacheSplitEvent(t, "split", int64Value(100))
	status, err := database.UpsertEvent(conflict)
	var rejected *RejectError
	if status != ReconcileRejected || !errors.As(err, &rejected) || rejected.Code != "token_conflict" {
		t.Fatalf("conflicting split status=%q err=%v", status, err)
	}

	tooLarge := cacheSplitEvent(t, "too-large", int64Value(501))
	status, err = database.UpsertEvent(tooLarge)
	if status != ReconcileRejected || !errors.As(err, &rejected) || rejected.Code != "invalid_cache_ttl_split" {
		t.Fatalf("oversized split status=%q err=%v", status, err)
	}
}

func TestMergeAcceptsLegacyV3SourceAndKeepsKnownSplit(t *testing.T) {
	destination := openTestDatabase(t)
	defer destination.Close()
	shared := cacheSplitEvent(t, "shared", int64Value(200))
	if _, err := destination.UpsertEvent(shared); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(t.TempDir(), "legacy.aldb")
	onlyLegacy := cacheSplitEvent(t, "only-legacy", nil)
	writeLegacyV3Database(t, sourcePath, cacheSplitEvent(t, "shared", nil), onlyLegacy)

	result, err := destination.MergeFrom(sourcePath)
	if err != nil || result.Added != 1 || result.Skipped+result.Updated != 1 || result.Rejected != 0 {
		t.Fatalf("merge result=%+v err=%v", result, err)
	}
	stored, err := selectEvent(destination.Conn(), shared.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheCreation1hTokens == nil || *stored.CacheCreation1hTokens != 200 {
		t.Fatalf("merge from v3 erased known split: %+v", stored.CacheCreation1hTokens)
	}
	if version := rawSchemaVersion(t, sourcePath); version != legacySchemaVersionV3 {
		t.Fatalf("merge mutated v3 source schema to %q", version)
	}
}

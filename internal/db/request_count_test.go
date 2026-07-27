package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestRequestCountMetadataEnrichmentIsIdempotent(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID:         "copilot-summary",
		DedupeKey:       "copilot-summary",
		DedupeStrategy:  "message_id",
		Channel:         "copilot",
		SourceAgent:     "copilot",
		SourceProduct:   "copilot-session-state",
		Provider:        "github",
		ModelRaw:        "gpt-5.4",
		ModelNormalized: "gpt-5.4",
		TimestampMs:     1,
		InputTokens:     10,
		OutputTokens:    5,
		TotalTokens:     15,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	if status, err := database.UpsertEvent(base); err != nil || status != "inserted" {
		t.Fatalf("initial upsert status=%q err=%v", status, err)
	}

	requestCount := int64(3)
	withCount := *base
	withCount.RequestCount = &requestCount
	withCount.UpdatedAtMs = 2
	if status, err := database.UpsertEvent(&withCount); err != nil || status != "updated" {
		t.Fatalf("count enrichment status=%q err=%v", status, err)
	}
	assertStoredRequestCount(t, database, requestCount)

	withoutCount := withCount
	withoutCount.RequestCount = nil
	withoutCount.UpdatedAtMs = 3
	if status, err := database.UpsertEvent(&withoutCount); err != nil || status != "skipped" {
		t.Fatalf("unknown count should not erase known value status=%q err=%v", status, err)
	}
	assertStoredRequestCount(t, database, requestCount)

	if status, err := database.UpsertEvent(&withCount); err != nil || status != "skipped" {
		t.Fatalf("repeated count import status=%q err=%v", status, err)
	}

	correctedCount := int64(4)
	corrected := withCount
	corrected.RequestCount = &correctedCount
	corrected.UpdatedAtMs = 4
	if status, err := database.UpsertEvent(&corrected); err != nil || status != "updated" {
		t.Fatalf("count correction status=%q err=%v", status, err)
	}
	assertStoredRequestCount(t, database, correctedCount)
}

func TestRequestCountIsAdditiveV2CompatibilityColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-ledger.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := database.Conn().Exec(`ALTER TABLE usage_events DROP COLUMN request_count`); err != nil {
		t.Fatalf("drop compatibility column: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if reader, err := OpenReadOnlyV2(path); err == nil {
		_ = reader.Close()
		t.Fatal("strict read-only open accepted missing request_count")
	} else if !strings.Contains(err.Error(), "missing additive v2 column usage_events.request_count") {
		t.Fatalf("unexpected read-only validation error: %v", err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("compatibility upgrade: %v", err)
	}
	defer upgraded.Close()
	var count int
	if err := upgraded.Conn().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('usage_events') WHERE name='request_count'`).Scan(&count); err != nil {
		t.Fatalf("inspect upgraded schema: %v", err)
	}
	if count != 1 {
		t.Fatalf("request_count column was not restored: %d", count)
	}
}

func TestMergePreservesRequestCountAndAcceptsOlderV2Input(t *testing.T) {
	t.Run("new input", func(t *testing.T) {
		destination, err := Open(filepath.Join(t.TempDir(), "destination.db"))
		if err != nil {
			t.Fatalf("open destination: %v", err)
		}
		defer destination.Close()
		incomingPath := filepath.Join(t.TempDir(), "incoming.db")
		incoming, err := Open(incomingPath)
		if err != nil {
			t.Fatalf("open incoming: %v", err)
		}
		count := int64(9)
		insertMergeRequestEvent(t, incoming, "new-request-count", &count)
		if err := incoming.Close(); err != nil {
			t.Fatalf("close incoming: %v", err)
		}

		result, err := destination.MergeFrom(incomingPath)
		if err != nil || result.Inserted != 1 {
			t.Fatalf("merge result=%+v err=%v", result, err)
		}
		var stored int64
		if err := destination.Conn().QueryRow(`SELECT request_count FROM usage_events WHERE event_id='new-request-count'`).Scan(&stored); err != nil {
			t.Fatalf("read merged count: %v", err)
		}
		if stored != count {
			t.Fatalf("merged request_count=%d want=%d", stored, count)
		}
	})

	t.Run("older input without column", func(t *testing.T) {
		destination, err := Open(filepath.Join(t.TempDir(), "destination.db"))
		if err != nil {
			t.Fatalf("open destination: %v", err)
		}
		defer destination.Close()
		incomingPath := filepath.Join(t.TempDir(), "incoming.db")
		incoming, err := Open(incomingPath)
		if err != nil {
			t.Fatalf("open incoming: %v", err)
		}
		insertMergeRequestEvent(t, incoming, "old-request-count", nil)
		if _, err := incoming.Conn().Exec(`ALTER TABLE usage_events DROP COLUMN request_count`); err != nil {
			t.Fatalf("drop incoming compatibility column: %v", err)
		}
		if err := incoming.Close(); err != nil {
			t.Fatalf("close incoming: %v", err)
		}

		result, err := destination.MergeFrom(incomingPath)
		if err != nil || result.Inserted != 1 {
			t.Fatalf("merge old input result=%+v err=%v", result, err)
		}
		var known int
		if err := destination.Conn().QueryRow(`SELECT request_count IS NOT NULL FROM usage_events WHERE event_id='old-request-count'`).Scan(&known); err != nil {
			t.Fatalf("read old merged count: %v", err)
		}
		if known != 0 {
			t.Fatalf("older input should merge with unknown request_count: %d", known)
		}
	})
}

func TestRequestCountPreservedOnMoreCompleteUnknownIncoming(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	known := int64(7)
	existing := &model.UsageEvent{
		EventID: "exact-preserve-count", DedupeKey: "exact-preserve-count", DedupeStrategy: "message_id",
		Channel: "copilot", SourceAgent: "copilot", SourceProduct: "copilot-session-state",
		Provider: "github", ModelRaw: "gpt-5.4", ModelNormalized: "gpt-5.4",
		TimestampMs: 1, InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		RequestCount: &known, ImportedAtMs: 1, UpdatedAtMs: 1,
	}
	if status, err := database.UpsertEvent(existing); err != nil || status != "inserted" {
		t.Fatalf("insert status=%q err=%v", status, err)
	}

	// More complete via timing fields, but request count unknown.
	incoming := *existing
	incoming.RequestCount = nil
	incoming.TotalDurationMs = int64Ptr(1200)
	incoming.InputTokens = 20
	incoming.OutputTokens = 10
	incoming.TotalTokens = 30
	incoming.UpdatedAtMs = 2
	if status, err := database.UpsertEvent(&incoming); err != nil || status != "updated" {
		t.Fatalf("more complete upsert status=%q err=%v", status, err)
	}

	var total, count int64
	if err := database.Conn().QueryRow(`SELECT total_tokens, request_count FROM usage_events WHERE event_id='exact-preserve-count'`).Scan(&total, &count); err != nil {
		t.Fatalf("select: %v", err)
	}
	if total != 30 || count != known {
		t.Fatalf("total=%d count=%d want total=30 count=%d", total, count, known)
	}
}

func TestRequestCountMetadataEnrichmentAcrossTokenBucketChange(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID: "metadata-count-fill", DedupeKey: "metadata-count-fill", DedupeStrategy: "message_id",
		Channel: "copilot", SourceAgent: "copilot", SourceProduct: "copilot-session-state",
		Provider: "github", ModelRaw: "gpt-5.4", ModelNormalized: "gpt-5.4",
		TimestampMs: 1, InputTokens: 10, OutputTokens: 5, CacheReadTokens: 0, TotalTokens: 15,
		ImportedAtMs: 1, UpdatedAtMs: 1,
	}
	if status, err := database.UpsertEvent(base); err != nil || status != "inserted" {
		t.Fatalf("insert status=%q err=%v", status, err)
	}

	// Token buckets change but score does not increase enough to win full update
	// (same total, no timing/cost). Count should still enrich via metadata path.
	count := int64(5)
	incoming := *base
	incoming.InputTokens = 8
	incoming.OutputTokens = 7
	incoming.TotalTokens = 15
	incoming.RequestCount = &count
	incoming.UpdatedAtMs = 2
	if status, err := database.UpsertEvent(&incoming); err != nil || status != "updated" {
		t.Fatalf("metadata count fill status=%q err=%v", status, err)
	}

	var input, output, total, stored int64
	if err := database.Conn().QueryRow(`SELECT input_tokens, output_tokens, total_tokens, request_count FROM usage_events WHERE event_id='metadata-count-fill'`).Scan(&input, &output, &total, &stored); err != nil {
		t.Fatalf("select: %v", err)
	}
	if input != 10 || output != 5 || total != 15 {
		t.Fatalf("usage winner should stay existing tokens input=%d output=%d total=%d", input, output, total)
	}
	if stored != count {
		t.Fatalf("request_count=%d want=%d", stored, count)
	}
}

func TestRequestCountCanonicalReconciliationPriority(t *testing.T) {
	incomingCount := int64(11)
	exactCount := int64(22)
	olderStoredCount := int64(33)
	newerStoredCount := int64(44)

	incoming := &model.UsageEvent{
		EventID: "incoming-a", DedupeKey: "incoming-a", DedupeStrategy: "message_id",
		Channel: "copilot", Provider: "github", ModelRaw: "gpt-5.4", ModelNormalized: "gpt-5.4",
		TimestampMs: 10, InputTokens: 1, OutputTokens: 1, TotalTokens: 2,
		RequestCount: &incomingCount, ImportedAtMs: 10, UpdatedAtMs: 10,
	}
	exact := &model.UsageEvent{
		EventID: "exact-b", DedupeKey: "exact-b", DedupeStrategy: "message_id",
		Channel: "copilot", Provider: "github", ModelRaw: "gpt-5.4", ModelNormalized: "gpt-5.4",
		TimestampMs: 10, InputTokens: 1, OutputTokens: 1, TotalTokens: 2,
		RequestCount: &exactCount, ImportedAtMs: 5, UpdatedAtMs: 20,
	}
	storedOlder := &model.UsageEvent{
		EventID: "stored-c", DedupeKey: "stored-c", DedupeStrategy: "message_id",
		Channel: "copilot", Provider: "github", ModelRaw: "gpt-5.4", ModelNormalized: "gpt-5.4",
		TimestampMs: 10, InputTokens: 5, OutputTokens: 5, TotalTokens: 10,
		RequestCount: &olderStoredCount, ImportedAtMs: 1, UpdatedAtMs: 30,
	}
	storedNewer := &model.UsageEvent{
		EventID: "stored-d", DedupeKey: "stored-d", DedupeStrategy: "message_id",
		Channel: "copilot", Provider: "github", ModelRaw: "gpt-5.4", ModelNormalized: "gpt-5.4",
		TimestampMs: 10, InputTokens: 1, OutputTokens: 1, TotalTokens: 2,
		RequestCount: &newerStoredCount, ImportedAtMs: 2, UpdatedAtMs: 40,
	}

	canonical := buildCanonicalReconciledEvent(incoming, exact, []*model.UsageEvent{storedOlder, storedNewer, exact})
	if canonical.RequestCount == nil || *canonical.RequestCount != incomingCount {
		t.Fatalf("incoming non-nil should win, got %v", canonical.RequestCount)
	}
	if canonical.TotalTokens != 10 {
		t.Fatalf("token winner should remain independent, total=%d", canonical.TotalTokens)
	}

	incomingUnknown := *incoming
	incomingUnknown.RequestCount = nil
	canonical = buildCanonicalReconciledEvent(&incomingUnknown, exact, []*model.UsageEvent{storedOlder, storedNewer, exact})
	if canonical.RequestCount == nil || *canonical.RequestCount != exactCount {
		t.Fatalf("exact non-nil should beat stored, got %v", canonical.RequestCount)
	}

	canonical = buildCanonicalReconciledEvent(&incomingUnknown, nil, []*model.UsageEvent{storedOlder, storedNewer})
	if canonical.RequestCount == nil || *canonical.RequestCount != newerStoredCount {
		t.Fatalf("most recently updated stored count should win, got %v", canonical.RequestCount)
	}

	// Stable event_id tie-break when UpdatedAtMs is equal.
	tieA := *storedOlder
	tieA.EventID = "tie-b"
	tieA.UpdatedAtMs = 50
	tieA.RequestCount = int64Ptr(8)
	tieB := *storedOlder
	tieB.EventID = "tie-a"
	tieB.UpdatedAtMs = 50
	tieB.RequestCount = int64Ptr(9)
	canonical = buildCanonicalReconciledEvent(&incomingUnknown, nil, []*model.UsageEvent{&tieA, &tieB})
	if canonical.RequestCount == nil || *canonical.RequestCount != 9 {
		t.Fatalf("event_id tie-break should pick tie-a count=9, got %v", canonical.RequestCount)
	}
}

func int64Ptr(v int64) *int64 { return &v }

func insertMergeRequestEvent(t *testing.T, database *Database, eventID string, requestCount *int64) {
	t.Helper()
	event := &model.UsageEvent{
		EventID:         eventID,
		DedupeKey:       eventID,
		DedupeStrategy:  "message_id",
		Channel:         "copilot",
		Provider:        "github",
		ModelRaw:        "gpt-5.4",
		ModelNormalized: "gpt-5.4",
		TimestampMs:     1,
		InputTokens:     10,
		OutputTokens:    5,
		TotalTokens:     15,
		RequestCount:    requestCount,
		ImportedAtMs:    1,
		UpdatedAtMs:     1,
	}
	if status, err := database.UpsertEvent(event); err != nil || status != "inserted" {
		t.Fatalf("insert merge event status=%q err=%v", status, err)
	}
}

func assertStoredRequestCount(t *testing.T, database *Database, want int64) {
	t.Helper()
	var got int64
	if err := database.Conn().QueryRow(`SELECT request_count FROM usage_events WHERE event_id='copilot-summary'`).Scan(&got); err != nil {
		t.Fatalf("select request_count: %v", err)
	}
	if got != want {
		t.Fatalf("request_count=%d want=%d", got, want)
	}
}

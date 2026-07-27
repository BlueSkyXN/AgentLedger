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

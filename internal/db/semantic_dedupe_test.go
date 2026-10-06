package db

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func semanticCodexEvent(t *testing.T, id string) *model.UsageEvent {
	t.Helper()
	event := testEvent(id, "", 120)
	event.IdentityStrategy = "session_record"
	event.Provider = "openai"
	event.ModelRaw = "gpt-5"
	event.ModelNormalized = "gpt-5"
	event.ModelResolution = model.ModelResolutionDirectEvent
	event.ModelIsFallback = false
	event.InputTokens = 80
	event.OutputTokens = 20
	event.CacheReadTokens = 20
	event.SourceTotalTokens = int64Value(1200)
	event.RawInputTokens = int64Value(100)
	event.TokenAccountingMethod = model.AccCodexTotalDelta
	event.AccountingProfile = "ledger"
	event.ObservabilityLevel = "full"
	event.ContentSHA256 = mustContentSHA256(t, event)
	return event
}

func semanticSource(t *testing.T, events ...*model.UsageEvent) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.aldb")
	source, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, event := range events {
		if err := ValidateEvent(event); err != nil {
			t.Fatal(err)
		}
		if err := validateStoredEvent(event); err != nil {
			t.Fatal(err)
		}
		// Source databases can predate semantic deduplication.
		if err := insertEvent(source.Conn(), event); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestSemanticDedupePreservesDistinctEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.UsageEvent, *model.UsageEvent)
	}{
		{name: "native event", mutate: func(a, b *model.UsageEvent) { a.IdentityStrategy, b.IdentityStrategy = "native_event", "native_event" }},
		{name: "native message", mutate: func(a, b *model.UsageEvent) {
			a.IdentityStrategy, b.IdentityStrategy = "native_message", "native_message"
		}},
		{name: "native request", mutate: func(a, b *model.UsageEvent) {
			a.IdentityStrategy, b.IdentityStrategy = "native_request", "native_request"
		}},
		{name: "existing native identity", mutate: func(a, b *model.UsageEvent) { a.IdentityStrategy = "native_event" }},
		{name: "incoming native identity", mutate: func(a, b *model.UsageEvent) { b.IdentityStrategy = "native_event" }},
		{name: "session turn", mutate: func(a, b *model.UsageEvent) { a.IdentityStrategy, b.IdentityStrategy = "session_turn", "session_turn" }},
		{name: "content fallback", mutate: func(a, b *model.UsageEvent) {
			a.IdentityStrategy, b.IdentityStrategy = "content_fallback", "content_fallback"
		}},
		{name: "other source", mutate: func(a, b *model.UsageEvent) {
			for _, event := range []*model.UsageEvent{a, b} {
				event.Channel, event.SourceProduct = "zcode", "zcode-cli-db"
				event.IdentityStrategy = "native_request"
				event.TokenAccountingMethod, event.AccountingProfile = model.AccZCodeModelUsage, "zcode_model_usage_v1"
				event.SourceTotalTokens = int64Value(event.TotalTokens)
			}
		}},
		{name: "other source record", mutate: func(a, b *model.UsageEvent) {
			for _, event := range []*model.UsageEvent{a, b} {
				event.Channel, event.SourceProduct = "gemini", "gemini-cli"
				event.TokenAccountingMethod, event.AccountingProfile = model.AccGeminiUsage, "gemini_usage_v1"
			}
		}},
		{name: "source cumulative total", mutate: func(a, b *model.UsageEvent) { b.SourceTotalTokens = int64Value(1320) }},
		{name: "channel", mutate: func(a, b *model.UsageEvent) { b.Channel = "other-channel" }},
		{name: "normalized model", mutate: func(a, b *model.UsageEvent) { b.ModelNormalized = "gpt-5-other" }},
		{name: "session key", mutate: func(a, b *model.UsageEvent) { b.SessionKey = hashTestValue("other-session") }},
		{name: "timestamp", mutate: func(a, b *model.UsageEvent) { b.TimestampMs++ }},
		{name: "token buckets", mutate: func(a, b *model.UsageEvent) { b.InputTokens--; b.OutputTokens++ }},
		{name: "unknown cumulative total", mutate: func(a, b *model.UsageEvent) { a.SourceTotalTokens, b.SourceTotalTokens = nil, nil }},
		{name: "unknown raw input", mutate: func(a, b *model.UsageEvent) { a.RawInputTokens, b.RawInputTokens = nil, nil }},
		{name: "raw input", mutate: func(a, b *model.UsageEvent) { b.RawInputTokens = int64Value(101) }},
		{name: "non cumulative usage", mutate: func(a, b *model.UsageEvent) {
			a.TokenAccountingMethod, b.TokenAccountingMethod = model.AccCodexLastTokenUsage, model.AccCodexLastTokenUsage
		}},
		{name: "provider", mutate: func(a, b *model.UsageEvent) { b.Provider = "other-provider" }},
		{name: "accounting profile", mutate: func(a, b *model.UsageEvent) { b.AccountingProfile = "ccusage_compatible" }},
		{name: "observability", mutate: func(a, b *model.UsageEvent) { b.ObservabilityLevel = "partial" }},
		{name: "raw model", mutate: func(a, b *model.UsageEvent) { b.ModelRaw = "gpt-5(high)" }},
		{name: "model evidence", mutate: func(a, b *model.UsageEvent) { b.ModelResolution = model.ModelResolutionTurnContext }},
		{name: "granularity", mutate: func(a, b *model.UsageEvent) { b.EventGranularity = "session" }},
		{name: "scope", mutate: func(a, b *model.UsageEvent) { b.IdentityScope = "global" }},
		{name: "native session", mutate: func(a, b *model.UsageEvent) { b.SessionID = "different-session" }},
		{name: "session path identity", mutate: func(a, b *model.UsageEvent) { b.SessionPathID = "different-session-path" }},
		{name: "turn identity", mutate: func(a, b *model.UsageEvent) { a.TurnID, b.TurnID = "turn-a", "turn-b" }},
		{name: "message identity", mutate: func(a, b *model.UsageEvent) { a.MessageID, b.MessageID = "message-a", "message-b" }},
		{name: "request identity", mutate: func(a, b *model.UsageEvent) { a.RequestID, b.RequestID = "request-a", "request-b" }},
		{name: "unknown versus known TTL", mutate: func(a, b *model.UsageEvent) {
			a.InputTokens, b.InputTokens = 60, 60
			a.CacheCreationTokens, b.CacheCreationTokens = 20, 20
			b.CacheCreation1hTokens = int64Value(10)
		}},
		{name: "different known TTL", mutate: func(a, b *model.UsageEvent) {
			a.InputTokens, b.InputTokens = 60, 60
			a.CacheCreationTokens, b.CacheCreationTokens = 20, 20
			a.CacheCreation1hTokens, b.CacheCreation1hTokens = int64Value(5), int64Value(10)
		}},
		{name: "zero usage", mutate: func(a, b *model.UsageEvent) {
			for _, event := range []*model.UsageEvent{a, b} {
				event.InputTokens, event.OutputTokens, event.CacheReadTokens, event.TotalTokens = 0, 0, 0, 0
			}
		}},
	}
	for _, test := range tests {
		for _, mode := range []string{"import", "merge"} {
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				database := openTestDatabase(t)
				defer database.Close()
				original, incoming := semanticCodexEvent(t, "original"), semanticCodexEvent(t, "incoming")
				test.mutate(original, incoming)
				original.ContentSHA256 = mustContentSHA256(t, original)
				incoming.ContentSHA256 = mustContentSHA256(t, incoming)
				if status, err := database.UpsertEvent(original); err != nil || status != ReconcileInserted {
					t.Fatalf("original status=%s err=%v", status, err)
				}
				if mode == "import" {
					if status, err := database.UpsertEvent(incoming); err != nil || status != ReconcileInserted {
						t.Fatalf("distinct evidence status=%s err=%v", status, err)
					}
				} else {
					result, err := database.MergeFrom(semanticSource(t, incoming))
					if err != nil || result.Added != 1 || result.Skipped != 0 {
						t.Fatalf("distinct evidence merge=%+v err=%v", result, err)
					}
				}
				var count, total int64
				if err := database.Conn().QueryRow(`SELECT COUNT(*), SUM(total_tokens) FROM usage_events`).Scan(&count, &total); err != nil {
					t.Fatal(err)
				}
				if count != 2 || total != original.TotalTokens+incoming.TotalTokens {
					t.Fatalf("distinct evidence lost: rows=%d total=%d", count, total)
				}
				stored, err := selectEvent(database.Conn(), incoming.EventID)
				if err != nil || !reflect.DeepEqual(stored.CacheCreation1hTokens, incoming.CacheCreation1hTokens) {
					t.Fatalf("incoming TTL evidence lost: event=%+v err=%v", stored, err)
				}
			})
		}
	}
}

func TestSemanticDedupeImportAndMergeAreIdempotent(t *testing.T) {
	for _, mode := range []string{"import", "merge"} {
		t.Run(mode, func(t *testing.T) {
			database := openTestDatabase(t)
			defer database.Close()
			original := semanticCodexEvent(t, "original")
			original.SourceFile, original.LineNumber = "/synthetic/original.jsonl", 10
			if _, err := database.UpsertEvent(original); err != nil {
				t.Fatal(err)
			}
			before, err := selectEvent(database.Conn(), original.EventID)
			if err != nil {
				t.Fatal(err)
			}
			rewritten := semanticCodexEvent(t, "rewritten")
			rewritten.SourceFile, rewritten.LineNumber = "/synthetic/rewritten.jsonl", 20
			rewritten.RawSHA256 = hashTestValue("different-serialization")
			source := semanticSource(t, rewritten)
			for range 2 {
				if mode == "import" {
					if status, err := database.UpsertEvent(rewritten); err != nil || status != ReconcileSkipped {
						t.Fatalf("duplicate status=%s err=%v", status, err)
					}
				} else {
					result, err := database.MergeFrom(source)
					if err != nil || result.Added != 0 || result.Updated != 0 || result.Skipped != 1 {
						t.Fatalf("duplicate merge=%+v err=%v", result, err)
					}
				}
			}
			after, err := listEvents(database.Conn(), true)
			if err != nil || len(after) != 1 || !reflect.DeepEqual(before, after[0]) {
				t.Fatalf("duplicate wrote canonical event: events=%+v err=%v", after, err)
			}
		})
	}
}

func TestMergeSemanticDedupeIncludesEarlierBatchRows(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	first, second := semanticCodexEvent(t, "first"), semanticCodexEvent(t, "second")
	source := semanticSource(t, first, second)
	result, err := database.MergeFrom(source)
	if err != nil || result.Added != 1 || result.Skipped != 1 {
		t.Fatalf("batch merge=%+v err=%v", result, err)
	}
	result, err = database.MergeFrom(source)
	if err != nil || result.Added != 0 || result.Updated != 0 || result.Skipped != 2 {
		t.Fatalf("repeat batch merge=%+v err=%v", result, err)
	}
}

func TestMergeSemanticDedupeKeepsRemainingMatchingRows(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	first, second := semanticCodexEvent(t, "first"), semanticCodexEvent(t, "second")
	for _, event := range []*model.UsageEvent{first, second} {
		if err := insertEvent(database.Conn(), event); err != nil {
			t.Fatal(err)
		}
	}
	updated := *first
	updated.CacheCreation1hTokens = int64Value(0)
	updated.ContentSHA256 = mustContentSHA256(t, &updated)
	duplicate := semanticCodexEvent(t, "duplicate")
	duplicate.EventID = strings.Repeat("f", 64)
	result, err := database.MergeFrom(semanticSource(t, &updated, duplicate))
	if err != nil || result.Added != 0 || result.Updated != 1 || result.Skipped != 1 {
		t.Fatalf("matching row lost from preflight index: result=%+v err=%v", result, err)
	}
}

func TestMergeSemanticDedupeRemovesOutdatedFacts(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	original := semanticCodexEvent(t, "original")
	if _, err := database.UpsertEvent(original); err != nil {
		t.Fatal(err)
	}
	updated := *original
	updated.CacheCreation1hTokens = int64Value(0)
	updated.ContentSHA256 = mustContentSHA256(t, &updated)
	distinct := semanticCodexEvent(t, "distinct")
	distinct.EventID = strings.Repeat("f", 64)
	result, err := database.MergeFrom(semanticSource(t, &updated, distinct))
	if err != nil || result.Added != 1 || result.Updated != 1 || result.Skipped != 0 {
		t.Fatalf("outdated facts caused a skip: result=%+v err=%v", result, err)
	}
}

func TestSemanticDedupePreservesClaudeTTLSplit(t *testing.T) {
	for _, mode := range []string{"import", "merge"} {
		t.Run(mode, func(t *testing.T) {
			database := openTestDatabase(t)
			defer database.Close()
			unknown := cacheSplitEvent(t, "unknown", nil)
			known := cacheSplitEvent(t, "known", int64Value(120))
			other := cacheSplitEvent(t, "other", int64Value(300))
			for _, event := range []*model.UsageEvent{unknown, known, other} {
				event.IdentityStrategy = "native_message"
			}
			if _, err := database.UpsertEvent(unknown); err != nil {
				t.Fatal(err)
			}
			for _, event := range []*model.UsageEvent{known, other} {
				if mode == "import" {
					if status, err := database.UpsertEvent(event); err != nil || status != ReconcileInserted {
						t.Fatalf("different Claude message: status=%s err=%v", status, err)
					}
				} else {
					result, err := database.MergeFrom(semanticSource(t, event))
					if err != nil || result.Added != 1 {
						t.Fatalf("different Claude message: merge=%+v err=%v", result, err)
					}
				}
			}
			refill := *unknown
			refill.CacheCreation1hTokens = int64Value(120)
			refill.ContentSHA256 = mustContentSHA256(t, &refill)
			if mode == "import" {
				if status, err := database.UpsertEvent(&refill); err != nil || status != ReconcileUpdated {
					t.Fatalf("same-ID refill: status=%s err=%v", status, err)
				}
			} else {
				result, err := database.MergeFrom(semanticSource(t, &refill))
				if err != nil || result.Updated != 1 {
					t.Fatalf("same-ID refill: merge=%+v err=%v", result, err)
				}
			}
			var count, knownCount, oneHour int64
			if err := database.Conn().QueryRow(`SELECT COUNT(*), COUNT(cache_creation_1h_tokens), SUM(cache_creation_1h_tokens) FROM usage_events`).Scan(&count, &knownCount, &oneHour); err != nil {
				t.Fatal(err)
			}
			if count != 3 || knownCount != 3 || oneHour != 540 {
				t.Fatalf("TTL facts lost: rows=%d known=%d 1h=%d", count, knownCount, oneHour)
			}
			before, err := listEvents(database.Conn(), true)
			if err != nil {
				t.Fatal(err)
			}
			conflict := refill
			conflict.CacheCreation1hTokens = int64Value(300)
			conflict.ContentSHA256 = mustContentSHA256(t, &conflict)
			if mode == "import" {
				status, err := database.UpsertEvent(&conflict)
				var rejected *RejectError
				if status != ReconcileRejected || !errors.As(err, &rejected) || rejected.Code != "token_conflict" {
					t.Fatalf("same-ID conflict: status=%s err=%v", status, err)
				}
			} else {
				result, err := database.MergeFrom(semanticSource(t, &conflict))
				var rejected *MergeConflictError
				if !errors.As(err, &rejected) || result.Rejected != 1 || len(result.Conflicts) != 1 || result.Conflicts[0].Code != "token_conflict" {
					t.Fatalf("same-ID conflict: merge=%+v err=%v", result, err)
				}
			}
			after, err := listEvents(database.Conn(), true)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("TTL conflict wrote destination: err=%v", err)
			}
		})
	}
}

func TestMergeSemanticDedupeAcceptsLegacyV3(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	original := semanticCodexEvent(t, "original")
	if _, err := database.UpsertEvent(original); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "legacy.aldb")
	writeLegacyV3Database(t, source, semanticCodexEvent(t, "rewritten"))
	result, err := database.MergeFrom(source)
	if err != nil || result.Added != 0 || result.Skipped != 1 {
		t.Fatalf("legacy semantic duplicate: result=%+v err=%v", result, err)
	}
	if version := rawSchemaVersion(t, source); version != legacySchemaVersionV3 {
		t.Fatalf("source schema changed: %s", version)
	}
}

func TestMergeSemanticDedupeValidatesContentBeforeSkipping(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	if _, err := database.UpsertEvent(semanticCodexEvent(t, "original")); err != nil {
		t.Fatal(err)
	}
	sourcePath := semanticSource(t, semanticCodexEvent(t, "rewritten"))
	source, err := Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Conn().Exec(`UPDATE usage_events SET content_sha256 = 'invalid'`); err != nil {
		source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := database.MergeFrom(sourcePath)
	var rejected *MergeConflictError
	if !errors.As(err, &rejected) || result.Rejected != 1 || len(result.Conflicts) != 1 || result.Conflicts[0].Code != "invalid_content_hash" {
		t.Fatalf("invalid content bypassed: result=%+v err=%v", result, err)
	}
}

func TestMergeSemanticDedupeDoesNotBypassIdentityConflict(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()
	first := semanticCodexEvent(t, "first")
	conflict := semanticCodexEvent(t, "conflict")
	conflict.TimestampMs++
	conflict.ContentSHA256 = mustContentSHA256(t, conflict)
	for _, event := range []*model.UsageEvent{first, conflict} {
		if _, err := database.UpsertEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	before, err := listEvents(database.Conn(), true)
	if err != nil {
		t.Fatal(err)
	}
	incoming := *first
	incoming.EventID = conflict.EventID
	duplicate := semanticCodexEvent(t, "duplicate")
	unique := semanticCodexEvent(t, "unique")
	unique.TimestampMs += 2
	unique.ContentSHA256 = mustContentSHA256(t, unique)
	result, err := database.MergeFrom(semanticSource(t, &incoming, duplicate, unique))
	var rejected *MergeConflictError
	if !errors.As(err, &rejected) || result.Rejected != 1 || len(result.Conflicts) != 1 || result.Conflicts[0].Code != "timestamp_conflict" {
		t.Fatalf("identity conflict bypassed: result=%+v err=%v", result, err)
	}
	after, err := listEvents(database.Conn(), true)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("conflicted merge wrote destination: err=%v", err)
	}
	status, err := database.UpsertEvent(&incoming)
	var eventRejected *RejectError
	if status != ReconcileRejected || !errors.As(err, &eventRejected) || eventRejected.Code != "timestamp_conflict" {
		t.Fatalf("import identity conflict bypassed: status=%s err=%v", status, err)
	}
}

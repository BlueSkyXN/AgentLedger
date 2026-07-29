package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestCodexReplay_ParentPrefixIsNotImported(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 2, 12, "gpt-5.6-sol"),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 20, 4, 24, "gpt-5.6-sol"),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:03Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03Z", 10, 2, 12, ""),
		codexTokenCountFixture("2026-01-01T00:00:03Z", 20, 4, 24, ""),
		codexTokenCountFixture("2026-01-01T00:00:05Z", 30, 6, 36, "gpt-5.6-terra"),
	})

	adapter := NewCodexAdapter()
	if err := adapter.PrepareFileSet([]string{parent, child}); err != nil {
		t.Fatalf("prepare replay plan: %v", err)
	}
	records, err := adapter.ParseFile(child)
	if err != nil {
		t.Fatalf("parse child: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected only the child-owned usage, got %d records", len(records))
	}
	if records[0].TotalTokens != 12 || records[0].Model != "gpt-5.6-terra" {
		t.Fatalf("unexpected child record: %+v", records[0])
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayExact, 2, 24)
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayEvents, 2, 0)
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayTokens, 0, 24)
}

func TestCodexReplay_ExactPrefixAtEOFProducesNoChildRecords(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 20, 2, 22, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:03Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:03Z", 20, 2, 22, ""),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
	records := parseCodexReplayRecords(t, adapter, child)
	if len(records) != 0 {
		t.Fatalf("exact replay-only child must produce no records: %+v", records)
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayExact, 2, 22)
}

func TestCodexReplay_ForkedFromIDPrecedesNestedParentID(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	directParent := filepath.Join(dir, "direct-parent.jsonl")
	nestedParent := filepath.Join(dir, "nested-parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, directParent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "direct-parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
	})
	writeCodexReplayFixture(t, nestedParent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "nested-parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 99, 9, 108, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaWithBothParentsFixture("2026-01-01T00:00:02Z", "child", "direct-parent", "nested-parent"),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 2, 22, "gpt-5.6-terra"),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, nestedParent, child, directParent)
	records := parseCodexReplayRecords(t, adapter, child)
	if len(records) != 1 || records[0].Model != "gpt-5.6-terra" {
		t.Fatalf("forked_from_id did not select the direct parent: %+v", records)
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayExact, 1, 11)
}

func TestCodexReplay_ForkTimestampExcludesLaterParentUsage(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 2, 12, "gpt-5.6-sol"),
		codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 4, 24, "gpt-5.6-sol"),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:03Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03Z", 10, 2, 12, ""),
		codexTokenCountFixture("2026-01-01T00:00:05Z", 20, 4, 24, "gpt-5.6-terra"),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
	records := parseCodexReplayRecords(t, adapter, child)
	if len(records) != 1 || records[0].TotalTokens != 12 || records[0].Model != "gpt-5.6-terra" {
		t.Fatalf("parent usage after the fork must remain child-owned: %+v", records)
	}
}

func TestCodexReplay_ProvenEmptyParentPrefixKeepsDenseChildUsage(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:04Z", 30, 3, 33, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03.000Z", 10, 1, 11, "gpt-5.6-terra"),
		codexTokenCountFixture("2026-01-01T00:00:03.500Z", 20, 2, 22, "gpt-5.6-terra"),
	})

	for _, policy := range []string{CodexDuplicatePolicyLedger, CodexDuplicatePolicyCCUsageCompatible} {
		t.Run(policy, func(t *testing.T) {
			adapter := prepareCodexReplayAdapter(t, policy, parent, child)
			records := parseCodexReplayRecords(t, adapter, child)
			if len(records) != 2 {
				t.Fatalf("a proven empty parent prefix cannot contain replay, records=%d", len(records))
			}
			if records[0].TimestampMs != parseTimestamp("2026-01-01T00:00:03.000Z") ||
				records[1].TimestampMs != parseTimestamp("2026-01-01T00:00:03.500Z") {
				t.Fatalf("unexpected retained child usage: %+v", records)
			}
			assertCodexDiagnostic(t, adapter, codexDiagnosticReplayExact, 0, 0)
			assertCodexDiagnostic(t, adapter, codexDiagnosticReplayRewritten, 0, 0)
		})
	}
}

func TestCodexReplay_RewrittenBurstEntryAndContinuationBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		second        string
		third         string
		wantRecords   int
		wantFirstTime int64
		wantSkipped   int64
		wantTokens    int64
	}{
		{
			name:          "1000ms entry and 1001ms exit",
			second:        "2026-01-01T00:00:04.000Z",
			third:         "2026-01-01T00:00:05.001Z",
			wantRecords:   1,
			wantFirstTime: 1767225605001,
			wantSkipped:   2,
			wantTokens:    220,
		},
		{
			name:          "1001ms entry means resolved child usage is real",
			second:        "2026-01-01T00:00:04.001Z",
			third:         "2026-01-01T00:00:06.000Z",
			wantRecords:   3,
			wantFirstTime: 1767225603000,
			wantSkipped:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
				codexTokenCountFixture("2026-01-01T00:00:03.000Z", 100, 10, 110, ""),
				codexTokenCountFixture(tt.second, 200, 20, 220, ""),
				codexTokenCountFixture(tt.third, 300, 30, 330, "gpt-5.6-terra"),
			})

			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			records, err := adapter.ParseFile(child)
			if err != nil {
				t.Fatalf("parse child: %v", err)
			}
			if len(records) != tt.wantRecords || records[0].TimestampMs != tt.wantFirstTime {
				t.Fatalf("unexpected retained records: %+v", records)
			}
			assertCodexDiagnostic(t, adapter, codexDiagnosticReplayRewritten, tt.wantSkipped, tt.wantTokens)
		})
	}
}

func TestCodexReplay_RewrittenBurstUsesEveryAdjacentGap(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 1, 1, 2, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03.000Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:03.900Z", 20, 2, 22, ""),
		codexTokenCountFixture("2026-01-01T00:00:04.800Z", 30, 3, 33, ""),
		codexTokenCountFixture("2026-01-01T00:00:05.801Z", 40, 4, 44, "gpt-5.6-terra"),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
	records := parseCodexReplayRecords(t, adapter, child)
	if len(records) != 1 || records[0].TimestampMs != 1767225605801 {
		t.Fatalf("burst continuation must use adjacent gaps: %+v", records)
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayRewritten, 3, 33)
}

func TestCodexReplay_RewrittenBurstMissingOrBackwardTimestampKeepsCurrent(t *testing.T) {
	for _, tt := range []struct {
		name      string
		timestamp string
	}{
		{name: "missing", timestamp: ""},
		{name: "backward", timestamp: "2026-01-01T00:00:02.500Z"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture("2026-01-01T00:00:01Z", 1, 1, 2, ""),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
				codexTokenCountFixture("2026-01-01T00:00:03.000Z", 10, 1, 11, ""),
				codexTokenCountFixture("2026-01-01T00:00:03.500Z", 20, 2, 22, ""),
				codexTokenCountFixture(tt.timestamp, 30, 3, 33, "gpt-5.6-terra"),
			})

			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			records := parseCodexReplayRecords(t, adapter, child)
			if len(records) != 1 || records[0].Model != "gpt-5.6-terra" {
				t.Fatalf("current usage must survive burst exit: %+v", records)
			}
		})
	}
}

func TestCodexReplay_PartialPrefixMismatchDoesNotFallBackToBurst(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 20, 2, 22, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:03Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03.000Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:03.500Z", 99, 9, 108, "gpt-5.6-terra"),
		codexTokenCountFixture("2026-01-01T00:00:04.000Z", 120, 12, 132, "gpt-5.6-terra"),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
	records := parseCodexReplayRecords(t, adapter, child)
	if len(records) != 2 {
		t.Fatalf("partial mismatch must retain current and later usage, got %+v", records)
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayExact, 1, 11)
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayRewritten, 0, 0)
}

func TestCodexReplay_NonForkDenseUsageIsNeverFiltered(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	path := filepath.Join(dir, "standalone.jsonl")
	writeCodexReplayFixture(t, path, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "standalone", ""),
		codexTokenCountFixture("2026-01-01T00:00:01.000Z", 10, 1, 11, "gpt-5.6-sol"),
		codexTokenCountFixture("2026-01-01T00:00:01.100Z", 20, 2, 22, "gpt-5.6-sol"),
		codexTokenCountFixture("2026-01-01T00:00:01.200Z", 30, 3, 33, "gpt-5.6-sol"),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, path)
	records := parseCodexReplayRecords(t, adapter, path)
	if len(records) != 3 {
		t.Fatalf("non-fork dense usage must remain untouched, got %d", len(records))
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticForkFiles, 0, 0)
}

func TestCodexReplay_ResolvedChildWithoutReplayKeepsOpeningModelContext(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, "gpt-parent"),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
		codexTurnContextFixture("2026-01-01T00:00:02Z", "gpt-child"),
		codexTokenCountFixture("2026-01-01T00:00:03Z", 99, 9, 108, ""),
		codexTokenCountFixture("2026-01-01T00:00:05Z", 120, 12, 132, ""),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
	records := parseCodexReplayRecords(t, adapter, child)
	if len(records) != 2 {
		t.Fatalf("expected both real child usages, got %+v", records)
	}
	for _, record := range records {
		if record.Model != "gpt-child" || record.ModelResolution != model.ModelResolutionTurnContext {
			t.Fatalf("opening real model context was lost: %+v", record)
		}
	}
}

func TestCodexReplay_MissingParentRequiresOpeningBurst(t *testing.T) {
	for _, tt := range []struct {
		name        string
		lines       []string
		wantError   bool
		wantRecords int
		wantSkipped int64
		wantTokens  int64
	}{
		{
			name: "burst proven",
			lines: []string{
				codexTokenCountFixture("2026-01-01T00:00:03.000Z", 10, 1, 11, ""),
				codexTokenCountFixture("2026-01-01T00:00:03.500Z", 20, 2, 22, ""),
				codexTokenCountFixture("2026-01-01T00:00:05.000Z", 30, 3, 33, "gpt-5.6-terra"),
			},
			wantRecords: 1,
			wantSkipped: 2,
			wantTokens:  22,
		},
		{
			name: "single usage is insufficient",
			lines: []string{
				codexTokenCountFixture("2026-01-01T00:00:03Z", 10, 1, 11, ""),
			},
			wantError: true,
		},
		{
			name: "opening gap is too large",
			lines: []string{
				codexTokenCountFixture("2026-01-01T00:00:03.000Z", 10, 1, 11, ""),
				codexTokenCountFixture("2026-01-01T00:00:04.001Z", 20, 2, 22, ""),
			},
			wantError: true,
		},
		{
			name: "unchanged retransmit is not independent burst evidence",
			lines: []string{
				codexTokenCountFixture("2026-01-01T00:00:03.000Z", 10, 1, 11, ""),
				codexTokenCountFixture("2026-01-01T00:00:03.001Z", 10, 1, 11, ""),
				codexTokenCountFixture("2026-01-01T00:00:05.000Z", 20, 2, 22, ""),
			},
			wantError: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			child := filepath.Join(dir, "child.jsonl")
			lines := append([]string{codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "missing")}, tt.lines...)
			writeCodexReplayFixture(t, child, lines)

			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, child)
			records, err := adapter.ParseFile(child)
			if tt.wantError {
				if err == nil || !isCodexReplayQuarantineError(err) || len(records) != 0 {
					t.Fatalf("expected quarantine, records=%d err=%v", len(records), err)
				}
			} else if err != nil || len(records) != tt.wantRecords {
				t.Fatalf("unexpected parse result records=%d err=%v", len(records), err)
			}
			assertCodexDiagnostic(t, adapter, codexDiagnosticParentMissing, 1, 0)
			assertCodexDiagnostic(t, adapter, codexDiagnosticReplayRewritten, tt.wantSkipped, tt.wantTokens)
		})
	}
}

func TestCodexReplay_AmbiguousAndSelfParentsAreQuarantined(t *testing.T) {
	t.Run("ambiguous non-equivalent active parents", func(t *testing.T) {
		dir := newCodexReplayDir(t, "sessions")
		parentA := filepath.Join(dir, "parent-a.jsonl")
		parentB := filepath.Join(dir, "parent-b.jsonl")
		child := filepath.Join(dir, "child.jsonl")
		writeCodexReplayFixture(t, parentA, []string{
			codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
			codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
		})
		writeCodexReplayFixture(t, parentB, []string{
			codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
			codexTokenCountFixture("2026-01-01T00:00:01Z", 20, 2, 22, ""),
		})
		writeCodexReplayFixture(t, child, []string{
			codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
			codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
		})

		adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parentA, parentB, child)
		records, err := adapter.ParseFile(child)
		if err == nil || !isCodexReplayQuarantineError(err) || len(records) != 0 {
			t.Fatalf("expected ambiguous child quarantine, records=%d err=%v", len(records), err)
		}
		assertCodexDiagnostic(t, adapter, codexDiagnosticParentAmbiguous, 1, 0)
		assertCodexDiagnostic(t, adapter, codexDiagnosticReplayUnresolved, 1, 0)
	})

	t.Run("self reference", func(t *testing.T) {
		dir := newCodexReplayDir(t, "sessions")
		child := filepath.Join(dir, "child.jsonl")
		writeCodexReplayFixture(t, child, []string{
			codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "child"),
			codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
		})

		adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, child)
		records, err := adapter.ParseFile(child)
		if err == nil || !isCodexReplayQuarantineError(err) || len(records) != 0 {
			t.Fatalf("expected self-referential child quarantine, records=%d err=%v", len(records), err)
		}
		assertCodexDiagnostic(t, adapter, codexDiagnosticParentAmbiguous, 1, 0)
	})
}

func TestCodexReplay_EquivalentParentsResolveAndActivePrecedesArchive(t *testing.T) {
	t.Run("equivalent active candidates", func(t *testing.T) {
		dir := newCodexReplayDir(t, "sessions")
		parentA := filepath.Join(dir, "parent-a.jsonl")
		parentB := filepath.Join(dir, "parent-b.jsonl")
		child := filepath.Join(dir, "child.jsonl")
		parentLines := []string{
			codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
			codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
		}
		writeCodexReplayFixture(t, parentA, parentLines)
		writeCodexReplayFixture(t, parentB, parentLines)
		writeCodexReplayFixture(t, child, []string{
			codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
			codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
			codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 2, 22, "gpt-5.6-terra"),
		})

		adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parentB, child, parentA)
		records := parseCodexReplayRecords(t, adapter, child)
		if len(records) != 1 {
			t.Fatalf("equivalent candidates should resolve deterministically: %+v", records)
		}
		assertCodexDiagnostic(t, adapter, codexDiagnosticParentResolved, 1, 0)
	})

	t.Run("active precedes archive", func(t *testing.T) {
		root := t.TempDir()
		activeDir := filepath.Join(root, "sessions")
		archiveDir := filepath.Join(root, "archived_sessions")
		if err := os.MkdirAll(activeDir, 0o755); err != nil {
			t.Fatalf("mkdir active: %v", err)
		}
		if err := os.MkdirAll(archiveDir, 0o755); err != nil {
			t.Fatalf("mkdir archive: %v", err)
		}
		active := filepath.Join(activeDir, "parent.jsonl")
		archived := filepath.Join(archiveDir, "parent.jsonl")
		child := filepath.Join(activeDir, "child.jsonl")
		writeCodexReplayFixture(t, active, []string{
			codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
			codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
		})
		writeCodexReplayFixture(t, archived, []string{
			codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
			codexTokenCountFixture("2026-01-01T00:00:01Z", 99, 9, 108, ""),
		})
		writeCodexReplayFixture(t, child, []string{
			codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
			codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
			codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 2, 22, "gpt-5.6-terra"),
		})

		adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, archived, child, active)
		records := parseCodexReplayRecords(t, adapter, child)
		if len(records) != 1 || records[0].Model != "gpt-5.6-terra" {
			t.Fatalf("active parent should win over archive: %+v", records)
		}
	})
}

func TestCodexReplay_NestedForksUseImmediateParentStream(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	root := filepath.Join(dir, "root.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	grandchild := filepath.Join(dir, "grandchild.jsonl")
	writeCodexReplayFixture(t, root, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "root", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, "gpt-5.6-sol"),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "root"),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 2, 22, "gpt-5.6-terra"),
	})
	writeCodexReplayFixture(t, grandchild, []string{
		codexSessionMetaFixture("2026-01-01T00:00:05Z", "grandchild", "child"),
		codexTokenCountFixture("2026-01-01T00:00:05Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:05Z", 20, 2, 22, ""),
		codexTokenCountFixture("2026-01-01T00:00:07Z", 30, 3, 33, "gpt-5.6-terra"),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, grandchild, root, child)
	childRecords := parseCodexReplayRecords(t, adapter, child)
	grandchildRecords := parseCodexReplayRecords(t, adapter, grandchild)
	if len(childRecords) != 1 || childRecords[0].TotalTokens != 11 {
		t.Fatalf("unexpected child-owned usage: %+v", childRecords)
	}
	if len(grandchildRecords) != 1 || grandchildRecords[0].TotalTokens != 11 {
		t.Fatalf("unexpected grandchild-owned usage: %+v", grandchildRecords)
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayExact, 3, 33)
}

func TestCodexReplay_FileChangeAfterPreparationQuarantinesChild(t *testing.T) {
	for _, changedFile := range []string{"child", "parent"} {
		t.Run(changedFile, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
				codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
			})
			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			target := child
			if changedFile == "parent" {
				target = parent
			}
			file, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatalf("open %s for append: %v", changedFile, err)
			}
			if _, err := file.WriteString(codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 2, 22, "") + "\n"); err != nil {
				file.Close()
				t.Fatalf("append %s: %v", changedFile, err)
			}
			if err := file.Close(); err != nil {
				t.Fatalf("close %s: %v", changedFile, err)
			}

			records, err := adapter.ParseFile(child)
			if err == nil || !isCodexReplayQuarantineError(err) || len(records) != 0 {
				t.Fatalf("changed %s must quarantine child, records=%d err=%v", changedFile, len(records), err)
			}
			assertCodexDiagnostic(t, adapter, codexDiagnosticReplayFileChanged, 1, 0)
		})
	}
}

func TestCodexReplay_UpdatesLedgerBaselineAcrossReplayAndReset(t *testing.T) {
	for _, tt := range []struct {
		name       string
		parentIn   int64
		parentOut  int64
		parentTot  int64
		realIn     int64
		realOut    int64
		realTot    int64
		wantStored int64
	}{
		{name: "cumulative continues", parentIn: 20, parentOut: 4, parentTot: 24, realIn: 30, realOut: 6, realTot: 36, wantStored: 12},
		{name: "counter reset", parentIn: 90, parentOut: 10, parentTot: 100, realIn: 15, realOut: 5, realTot: 20, wantStored: 20},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture("2026-01-01T00:00:01Z", tt.parentIn, tt.parentOut, tt.parentTot, ""),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
				codexTokenCountFixture("2026-01-01T00:00:02Z", tt.parentIn, tt.parentOut, tt.parentTot, ""),
				codexTokenCountFixture("2026-01-01T00:00:04Z", tt.realIn, tt.realOut, tt.realTot, "gpt-5.6-terra"),
			})

			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			records := parseCodexReplayRecords(t, adapter, child)
			if len(records) != 1 || records[0].TotalTokens != tt.wantStored {
				t.Fatalf("replay baseline mismatch: %+v", records)
			}
		})
	}
}

func TestCodexReplay_FirstOwnedResetWithoutUsableLastUsageIsRetained(t *testing.T) {
	for _, tt := range []struct {
		name           string
		resetLine      string
		wantCompatible int
	}{
		{
			name:           "total-only reset",
			resetLine:      codexTotalOnlyTokenCountFixture("2026-01-01T00:00:04Z", 15, 5, 20, "gpt-5.6-terra"),
			wantCompatible: 0,
		},
		{
			name:           "zero last reset",
			resetLine:      codexZeroLastTokenCountFixture("2026-01-01T00:00:04Z", 15, 5, 20, "gpt-5.6-terra"),
			wantCompatible: 0,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture("2026-01-01T00:00:01Z", 90, 10, 100, ""),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
				codexTokenCountFixture("2026-01-01T00:00:02Z", 90, 10, 100, ""),
				tt.resetLine,
			})

			ledger := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			compatible := prepareCodexReplayAdapter(t, CodexDuplicatePolicyCCUsageCompatible, parent, child)
			ledgerRecords := parseCodexReplayRecords(t, ledger, child)
			compatibleRecords := parseCodexReplayRecords(t, compatible, child)
			if len(ledgerRecords) != 1 || ledgerRecords[0].TotalTokens != 20 {
				t.Fatalf("ledger reset usage was dropped: %+v", ledgerRecords)
			}
			if len(compatibleRecords) != tt.wantCompatible {
				t.Fatalf("unexpected compatible records: %+v", compatibleRecords)
			}
			if !codexReplaySkipDiagnosticsEqual(ledger.ImportDiagnostics(), compatible.ImportDiagnostics()) {
				t.Fatal("reset transition changed the policy-independent replay skip set")
			}
			assertCodexDiagnostic(t, ledger, codexDiagnosticReplayExact, 1, 100)
		})
	}
}

func TestCodexReplay_UnknownForkBoundaryFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name            string
		forkTimestamp   string
		parentUsageTime string
	}{
		{
			name:            "missing fork timestamp",
			forkTimestamp:   "",
			parentUsageTime: "2026-01-01T00:00:01Z",
		},
		{
			name:            "missing parent usage timestamp",
			forkTimestamp:   "2026-01-01T00:00:02Z",
			parentUsageTime: "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture(tt.parentUsageTime, 10, 1, 11, ""),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture(tt.forkTimestamp, "child", "parent"),
				codexTokenCountFixture("2026-01-01T00:00:03Z", 10, 1, 11, ""),
			})

			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			records, err := adapter.ParseFile(child)
			if err == nil || !isCodexReplayQuarantineError(err) || len(records) != 0 {
				t.Fatalf("unknown replay boundary must quarantine child, records=%d err=%v", len(records), err)
			}
			assertCodexDiagnostic(t, adapter, codexDiagnosticReplayUnresolved, 1, 0)
		})
	}
}

func TestCodexReplay_UnknownReplayBoundaryCanUseIndependentOpeningBurst(t *testing.T) {
	for _, tt := range []struct {
		name            string
		forkTimestamp   string
		parentUsageTime string
	}{
		{name: "missing fork timestamp", forkTimestamp: "", parentUsageTime: "2026-01-01T00:00:01Z"},
		{name: "missing parent usage timestamp", forkTimestamp: "2026-01-01T00:00:02Z", parentUsageTime: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture(tt.parentUsageTime, 10, 1, 11, ""),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture(tt.forkTimestamp, "child", "parent"),
				codexTokenCountFixture("2026-01-01T00:00:03.000Z", 100, 10, 110, ""),
				codexTokenCountFixture("2026-01-01T00:00:03.500Z", 200, 20, 220, ""),
				codexTokenCountFixture("2026-01-01T00:00:05.000Z", 300, 30, 330, "gpt-5.6-terra"),
			})

			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			records := parseCodexReplayRecords(t, adapter, child)
			if len(records) != 1 || records[0].TotalTokens != 110 || records[0].Model != "gpt-5.6-terra" {
				t.Fatalf("opening burst fallback did not preserve child-owned usage: %+v", records)
			}
			assertCodexDiagnostic(t, adapter, codexDiagnosticReplayRewritten, 2, 220)
		})
	}
}

func TestCodexReplay_UnknownBoundaryCannotBypassAmbiguousParents(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parentA := filepath.Join(dir, "parent-a.jsonl")
	parentB := filepath.Join(dir, "parent-b.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parentA, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
	})
	writeCodexReplayFixture(t, parentB, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 20, 2, 22, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03.000Z", 100, 10, 110, ""),
		codexTokenCountFixture("2026-01-01T00:00:03.500Z", 200, 20, 220, ""),
		codexTokenCountFixture("2026-01-01T00:00:05.000Z", 300, 30, 330, "gpt-5.6-terra"),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parentA, parentB, child)
	records, err := adapter.ParseFile(child)
	if err == nil || !isCodexReplayQuarantineError(err) || len(records) != 0 {
		t.Fatalf("unknown boundary must not bypass ambiguous parents, records=%d err=%v", len(records), err)
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticParentAmbiguous, 1, 0)
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayRewritten, 0, 0)
}

func TestCodexReplay_ModelAndTimingStateIsolation(t *testing.T) {
	tests := []struct {
		name             string
		declarationTime  string
		declarationModel string
		wantModel        string
		wantResolution   string
		wantFallback     bool
	}{
		{
			name:             "replay declaration does not leak",
			declarationTime:  "2026-01-01T00:00:01.500Z",
			declarationModel: "gpt-replay",
			wantModel:        "unknown",
			wantResolution:   model.ModelResolutionUnknown,
			wantFallback:     true,
		},
		{
			name:             "same-second declaration is rejected",
			declarationTime:  "2026-01-01T00:00:02Z",
			declarationModel: "gpt-same-second",
			wantModel:        "unknown",
			wantResolution:   model.ModelResolutionUnknown,
			wantFallback:     true,
		},
		{
			name:             "post-replay declaration over one second is accepted",
			declarationTime:  "2026-01-01T00:00:03.001Z",
			declarationModel: "gpt-5.6-terra",
			wantModel:        "gpt-5.6-terra",
			wantResolution:   model.ModelResolutionTurnContext,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, "gpt-parent"),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
				codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
				codexTurnContextFixture(tt.declarationTime, tt.declarationModel),
				codexTaskCompleteFixture("2026-01-01T00:00:03.500Z"),
				codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 2, 22, ""),
			})

			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			records := parseCodexReplayRecords(t, adapter, child)
			if len(records) != 1 {
				t.Fatalf("expected one real child record, got %+v", records)
			}
			record := records[0]
			if record.Model != tt.wantModel || record.ModelResolution != tt.wantResolution || record.ModelIsFallback != tt.wantFallback {
				t.Fatalf("model state leaked or was not applied: model=%q resolution=%q fallback=%v", record.Model, record.ModelResolution, record.ModelIsFallback)
			}
			if record.TotalDurationMs != 0 || record.TTFTMs != 0 || record.CompletedAtMs != 0 {
				t.Fatalf("replay-local task timing leaked into real usage: %+v", record)
			}
		})
	}
}

func TestCodexReplay_UntrustedExactReplayTimestampDropsBufferedModel(t *testing.T) {
	for _, tt := range []struct {
		name          string
		firstReplay   string
		secondReplay  string
		declarationAt string
	}{
		{
			name:          "missing final replay timestamp",
			firstReplay:   "2026-01-01T00:00:03.000Z",
			secondReplay:  "",
			declarationAt: "2026-01-01T00:00:04.500Z",
		},
		{
			name:          "backward replay timestamp",
			firstReplay:   "2026-01-01T00:00:03.000Z",
			secondReplay:  "2026-01-01T00:00:02.500Z",
			declarationAt: "2026-01-01T00:00:04.001Z",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCodexReplayDir(t, "sessions")
			parent := filepath.Join(dir, "parent.jsonl")
			child := filepath.Join(dir, "child.jsonl")
			writeCodexReplayFixture(t, parent, []string{
				codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
				codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
				codexTokenCountFixture("2026-01-01T00:00:02Z", 20, 2, 22, ""),
			})
			writeCodexReplayFixture(t, child, []string{
				codexSessionMetaFixture("2026-01-01T00:00:03Z", "child", "parent"),
				codexTokenCountFixture(tt.firstReplay, 10, 1, 11, ""),
				codexTokenCountFixture(tt.secondReplay, 20, 2, 22, ""),
				codexTurnContextFixture(tt.declarationAt, "gpt-replay-context"),
				codexTokenCountFixture("2026-01-01T00:00:06Z", 30, 3, 33, ""),
			})

			adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
			records := parseCodexReplayRecords(t, adapter, child)
			if len(records) != 1 {
				t.Fatalf("expected one child-owned usage, got %+v", records)
			}
			if records[0].Model != "unknown" || records[0].ModelResolution != model.ModelResolutionUnknown {
				t.Fatalf("untrusted replay timestamp leaked buffered model: %+v", records[0])
			}
		})
	}
}

func TestCodexReplay_DirectModelStillOverridesTrustedBufferedContext(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
		codexTurnContextFixture("2026-01-01T00:00:03.001Z", "gpt-context"),
		codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 2, 22, "gpt-direct"),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
	records := parseCodexReplayRecords(t, adapter, child)
	if len(records) != 1 || records[0].Model != "gpt-direct" || records[0].ModelResolution != model.ModelResolutionDirectEvent {
		t.Fatalf("direct event model must remain authoritative: %+v", records)
	}
}

func TestCodexReplay_ThreadSettingsFromReplayDoesNotLeak(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:02Z", "child", "parent"),
		codexThreadSettingsFixture("2026-01-01T00:00:01.500Z", "gpt-replay-settings"),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:04Z", 20, 2, 22, ""),
	})

	adapter := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
	records := parseCodexReplayRecords(t, adapter, child)
	if len(records) != 1 || records[0].Model != "unknown" || records[0].ModelResolution != model.ModelResolutionUnknown {
		t.Fatalf("replay thread_settings leaked into child usage: %+v", records)
	}
}

func TestCodexReplay_SkipSetIsIndependentOfAccountingPolicy(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 20, 2, 22, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:03Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:03Z", 20, 2, 22, ""),
		codexTokenCountFixture("2026-01-01T00:00:05Z", 30, 3, 33, "gpt-5.6-terra"),
	})

	ledger := prepareCodexReplayAdapter(t, CodexDuplicatePolicyLedger, parent, child)
	compatible := prepareCodexReplayAdapter(t, CodexDuplicatePolicyCCUsageCompatible, parent, child)
	ledgerRecords := parseCodexReplayRecords(t, ledger, child)
	compatibleRecords := parseCodexReplayRecords(t, compatible, child)
	if len(ledgerRecords) != 1 || len(compatibleRecords) != 1 {
		t.Fatalf("both policies must retain only real child usage, ledger=%d compatible=%d", len(ledgerRecords), len(compatibleRecords))
	}
	if !codexReplaySkipDiagnosticsEqual(ledger.ImportDiagnostics(), compatible.ImportDiagnostics()) {
		t.Fatal("replay diagnostics differ across accounting policies")
	}
}

func TestCodexReplay_UnchangedCumulativeDuplicateKeepsMatcherPendingAcrossPolicies(t *testing.T) {
	dir := newCodexReplayDir(t, "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeCodexReplayFixture(t, parent, []string{
		codexSessionMetaFixture("2026-01-01T00:00:00Z", "parent", ""),
		codexTokenCountFixture("2026-01-01T00:00:01Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:02Z", 20, 2, 22, ""),
	})
	writeCodexReplayFixture(t, child, []string{
		codexSessionMetaFixture("2026-01-01T00:00:03Z", "child", "parent"),
		codexTokenCountFixture("2026-01-01T00:00:03.000Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:03.100Z", 10, 1, 11, ""),
		codexTokenCountFixture("2026-01-01T00:00:03.200Z", 20, 2, 22, ""),
		codexTokenCountFixture("2026-01-01T00:00:05.000Z", 30, 3, 33, "gpt-5.6-terra"),
	})

	for _, policy := range []string{CodexDuplicatePolicyLedger, CodexDuplicatePolicyCCUsageCompatible} {
		t.Run(policy, func(t *testing.T) {
			adapter := prepareCodexReplayAdapter(t, policy, parent, child)
			records := parseCodexReplayRecords(t, adapter, child)
			if len(records) != 1 || records[0].Model != "gpt-5.6-terra" {
				t.Fatalf("unchanged cumulative duplicate disturbed replay matching: %+v", records)
			}
			wantTokens := int64(22)
			if policy == CodexDuplicatePolicyCCUsageCompatible {
				wantTokens = 33
			}
			assertCodexDiagnostic(t, adapter, codexDiagnosticReplayExact, 2, wantTokens)
		})
	}
}

func TestCodexReplay_PreparationFailureFailsClosed(t *testing.T) {
	adapter := NewCodexAdapter()
	err := adapter.PrepareFileSet([]string{filepath.Join(t.TempDir(), "missing.jsonl")})
	if err == nil {
		t.Fatal("expected replay preparation failure")
	}
	if adapter.replayPlan != nil {
		t.Fatal("failed preparation must not leave a partially usable replay plan")
	}
	assertCodexDiagnostic(t, adapter, codexDiagnosticReplayPlanFailed, 1, 0)
}

func TestCodexReplay_ComparisonUsageClampsCacheAndDerivesInclusiveTotal(t *testing.T) {
	var obj map[string]interface{}
	line := `{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":125,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":0},"total_token_usage":{"input_tokens":100,"cached_input_tokens":125,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":0}}}}`
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	usage, status := extractCodexReplayUsage(obj, &codexReplayUsageState{})
	if status != codexReplayUsageComparable {
		t.Fatal("expected comparison usage")
	}
	if usage.Input != 100 || usage.Cached != 100 || usage.Output != 20 || usage.Reasoning != 5 || usage.Total != 120 {
		t.Fatalf("unexpected comparison usage: %+v", usage)
	}
}

func TestCodexReplay_ComparisonPrefersLastAndFallsBackToCumulativeDelta(t *testing.T) {
	state := &codexReplayUsageState{}
	lines := []string{
		`{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":0,"total_tokens":100},"total_token_usage":{"input_tokens":100,"output_tokens":0,"total_tokens":100}}}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"output_tokens":0,"total_tokens":20},"total_token_usage":{"input_tokens":500,"output_tokens":0,"total_tokens":500}}}}`,
		`{"timestamp":"2026-01-01T00:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":550,"output_tokens":0,"total_tokens":550}}}}`,
	}
	wantInput := []int64{100, 20, 50}
	for index, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("decode usage %d: %v", index, err)
		}
		usage, status := extractCodexReplayUsage(obj, state)
		if status != codexReplayUsageComparable {
			t.Fatalf("usage %d status=%d, want comparable", index, status)
		}
		if usage.Input != wantInput[index] || usage.Total != wantInput[index] {
			t.Fatalf("usage %d comparison=%+v, want input/total=%d", index, usage, wantInput[index])
		}
	}
}

func newCodexReplayDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir replay fixture: %v", err)
	}
	return dir
}

func prepareCodexReplayAdapter(t *testing.T, policy string, paths ...string) *CodexAdapter {
	t.Helper()
	adapter := NewCodexAdapterWithOptions(CodexOptions{DuplicatePolicy: policy})
	if err := adapter.PrepareFileSet(paths); err != nil {
		t.Fatalf("prepare replay plan: %v", err)
	}
	return adapter
}

func parseCodexReplayRecords(t *testing.T, adapter *CodexAdapter, path string) []*fingerprint.ParsedRecord {
	t.Helper()
	records, err := adapter.ParseFile(path)
	if err != nil {
		t.Fatalf("parse replay fixture: %v", err)
	}
	return records
}

func assertCodexDiagnostic(t *testing.T, adapter *CodexAdapter, code string, wantEvents, wantTokens int64) {
	t.Helper()
	diagnostic, ok := importDiagnosticByCode(adapter.ImportDiagnostics(), code)
	if !ok {
		t.Fatalf("missing diagnostic %s", code)
	}
	wantCountUnit := code == codexDiagnosticForkFiles ||
		code == codexDiagnosticParentResolved ||
		code == codexDiagnosticParentMissing ||
		code == codexDiagnosticParentAmbiguous ||
		code == codexDiagnosticReplayUnresolved ||
		code == codexDiagnosticReplayFileChanged ||
		code == codexDiagnosticReplayPlanFailed
	actual := diagnostic.Events
	if wantCountUnit {
		actual = diagnostic.Count
	}
	wantUnit := ImportDiagnosticUnitEvents
	if wantCountUnit {
		wantUnit = ImportDiagnosticUnitCount
	} else if code == codexDiagnosticReplayTokens {
		wantUnit = ImportDiagnosticUnitTokens
	} else if code == codexDiagnosticReplayExact || code == codexDiagnosticReplayRewritten {
		wantUnit = ImportDiagnosticUnitUsage
	}
	if actual != wantEvents || diagnostic.Tokens != wantTokens ||
		(wantCountUnit && diagnostic.Events != 0) || (!wantCountUnit && diagnostic.Count != 0) || diagnostic.Unit != wantUnit {
		t.Fatalf("diagnostic %s unit=%s count=%d events=%d tokens=%d, want unit=%s value=%d tokens=%d", code, diagnostic.Unit, diagnostic.Count, diagnostic.Events, diagnostic.Tokens, wantUnit, wantEvents, wantTokens)
	}
}

func writeCodexReplayFixture(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", filepath.Base(path), err)
	}
}

func codexSessionMetaFixture(timestamp, sessionID, parentID string) string {
	if parentID == "" {
		return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"source":"cli"}}`, timestamp, sessionID)
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"source":{"subagent":{"thread_spawn":{"parent_thread_id":%q}}}}}`, timestamp, sessionID, parentID)
}

func codexSessionMetaWithBothParentsFixture(timestamp, sessionID, directParentID, nestedParentID string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"forked_from_id":%q,"source":{"subagent":{"thread_spawn":{"parent_thread_id":%q}}}}}`, timestamp, sessionID, directParentID, nestedParentID)
}

func codexTokenCountFixture(timestamp string, input, output, total int64, modelName string) string {
	modelField := ""
	if modelName != "" {
		modelField = fmt.Sprintf(`,"model":%q`, modelName)
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d},"total_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}%s}}}`, timestamp, input, output, total, input, output, total, modelField)
}

func codexTotalOnlyTokenCountFixture(timestamp string, input, output, total int64, modelName string) string {
	modelField := ""
	if modelName != "" {
		modelField = fmt.Sprintf(`,"model":%q`, modelName)
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}%s}}}`, timestamp, input, output, total, modelField)
}

func codexZeroLastTokenCountFixture(timestamp string, input, output, total int64, modelName string) string {
	modelField := ""
	if modelName != "" {
		modelField = fmt.Sprintf(`,"model":%q`, modelName)
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0},"total_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}%s}}}`, timestamp, input, output, total, modelField)
}

func codexTurnContextFixture(timestamp, modelName string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":%q}}`, timestamp, modelName)
}

func codexThreadSettingsFixture(timestamp, modelName string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":%q}}}`, timestamp, modelName)
}

func codexTaskCompleteFixture(timestamp string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete","duration_ms":500,"time_to_first_token_ms":100,"completed_at":%q}}`, timestamp, timestamp)
}

package cmd

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
	"github.com/BlueSkyXN/AgentLedger/internal/usageevidence"
)

func TestCompactRawRequiresExactlyOneAction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dryRun bool
		apply  bool
	}{
		{name: "missing"},
		{name: "both", dryRun: true, apply: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := executeCompactRaw(tc.dryRun, tc.apply, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "exactly one") {
				t.Fatalf("expected explicit action error, got %v", err)
			}
		})
	}
}

func TestCompactRawDryRunApplyAndIdempotentRerun(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	cfg := config.Default()
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	database, err := db.Open(filepath.Join(dataDir, "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	legacyRaw := `{"message":{"id":"msg-1","usage":{"input_tokens":3,"output_tokens":2},"content":[{"type":"text","text":"private"}]},"sessionId":"session-private"}`
	_, err = database.UpsertEvent(&model.UsageEvent{
		EventID:        "event-1",
		DedupeKey:      "event-1",
		DedupeStrategy: "message_id",
		Channel:        "claude",
		TimestampMs:    1,
		InputTokens:    3,
		OutputTokens:   2,
		TotalTokens:    5,
		RawUsageJSON:   legacyRaw,
		ImportedAtMs:   1,
		UpdatedAtMs:    1,
	})
	if err != nil {
		_ = database.Close()
		t.Fatalf("insert event: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	var dryRunOutput bytes.Buffer
	if err := executeCompactRaw(true, false, &dryRunOutput); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(dryRunOutput.String(), "Candidates:          1") {
		t.Fatalf("unexpected dry-run output: %s", dryRunOutput.String())
	}
	if got := readRawEvidenceForTest(t, cfg.DBPath()); got != legacyRaw {
		t.Fatalf("dry-run changed raw evidence: %q", got)
	}

	var applyOutput bytes.Buffer
	if err := executeCompactRaw(false, true, &applyOutput); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, want := range []string{"Rows updated:         1", "Remaining candidates: 0"} {
		if !strings.Contains(applyOutput.String(), want) {
			t.Fatalf("apply output missing %q: %s", want, applyOutput.String())
		}
	}
	compact := readRawEvidenceForTest(t, cfg.DBPath())
	if !usageevidence.IsCompact("claude", compact) || strings.Contains(compact, "private") || strings.Contains(compact, "session-private") {
		t.Fatalf("unexpected compact evidence: %s", compact)
	}

	var rerunOutput bytes.Buffer
	if err := executeCompactRaw(false, true, &rerunOutput); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
	for _, want := range []string{"Candidates:          0", "Rows updated:         0"} {
		if !strings.Contains(rerunOutput.String(), want) {
			t.Fatalf("rerun output missing %q: %s", want, rerunOutput.String())
		}
	}
}

func readRawEvidenceForTest(t *testing.T, path string) string {
	t.Helper()
	database, err := db.OpenReadOnlyV2(path)
	if err != nil {
		t.Fatalf("open read-only database: %v", err)
	}
	defer database.Close()
	var raw string
	if err := database.Conn().QueryRow(`SELECT raw_usage_json FROM usage_events WHERE event_id = 'event-1'`).Scan(&raw); err != nil {
		t.Fatalf("read raw evidence: %v", err)
	}
	return raw
}

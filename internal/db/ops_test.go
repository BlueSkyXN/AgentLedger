package db

import (
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestUpsertEventKeepsMoreCompleteDuplicate(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	base := &model.UsageEvent{
		EventID:        "event-1",
		DedupeKey:      "event-1",
		DedupeStrategy: "message_id",
		Channel:        "codex",
		Provider:       "openai",
		TimestampMs:    1,
		InputTokens:    10,
		OutputTokens:   5,
		TotalTokens:    15,
		ImportedAtMs:   1,
		UpdatedAtMs:    1,
	}
	status, err := database.UpsertEvent(base)
	if err != nil || status != "inserted" {
		t.Fatalf("initial upsert status=%s err=%v", status, err)
	}

	ttft := int64(300)
	tps := 25.0
	candidate := *base
	candidate.ModelRaw = "gpt-5"
	candidate.ModelNormalized = "gpt-5"
	candidate.TotalTokens = 20
	candidate.TTFTMs = &ttft
	candidate.OutputTPS = &tps
	candidate.UpdatedAtMs = 2
	status, err = database.UpsertEvent(&candidate)
	if err != nil || status != "updated" {
		t.Fatalf("complete upsert status=%s err=%v", status, err)
	}

	var modelName string
	var total int64
	var storedTTFT int64
	if err := database.Conn().QueryRow(`SELECT model_normalized, total_tokens, ttft_ms FROM usage_events WHERE event_id='event-1'`).Scan(&modelName, &total, &storedTTFT); err != nil {
		t.Fatalf("select updated event: %v", err)
	}
	if modelName != "gpt-5" || total != 20 || storedTTFT != ttft {
		t.Fatalf("unexpected updated event model=%s total=%d ttft=%d", modelName, total, storedTTFT)
	}

	status, err = database.UpsertEvent(base)
	if err != nil || status != "skipped" {
		t.Fatalf("less complete upsert status=%s err=%v", status, err)
	}
}

package report

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestGenerateSupportsV3ReportsAndEstimatedDefault(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "report.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	event := &model.UsageEvent{
		EventID: "e1", IdentityVersion: model.IdentityVersion, IdentityStrategy: "native_event", IdentityScope: "session",
		ContentSHA256: "hash", ParserVersion: "test-v1", EventGranularity: "request",
		Channel: "codex", SourceProduct: "codex-cli", Provider: "openai",
		ModelRaw: "unknown", ModelNormalized: "unknown", ModelResolution: model.ModelResolutionUnknown, ModelIsFallback: true,
		TimestampMs: 1_700_000_000_000, SessionKey: "session", SessionID: "session",
		InputTokens: 2, TotalTokens: 2, ImportedAtMs: 1, UpdatedAtMs: 1,
	}
	if _, err := database.UpsertEvent(event); err != nil {
		t.Fatal(err)
	}

	for _, reportType := range []string{"daily", "weekly", "monthly", "models", "channels", "sources", "providers", "projects", "sessions"} {
		t.Run(reportType, func(t *testing.T) {
			output, err := captureReportOutput(func() error {
				return Generate(database.Conn(), reportType, Filters{Timezone: "UTC", CostMode: "estimated"}, true)
			})
			if err != nil {
				t.Fatalf("Generate(%s): %v", reportType, err)
			}
			if !strings.Contains(output, "total_tokens") {
				t.Fatalf("Generate(%s) missing token output: %s", reportType, output)
			}
			if strings.Contains(output, "recorded_cost") || strings.Contains(output, "ttft") || strings.Contains(output, "output_tps") {
				t.Fatalf("Generate(%s) leaked removed metric: %s", reportType, output)
			}
		})
	}
}

func TestGenerateRejectsRemovedCostModesAndInvalidExplicitPricing(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "report.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := Generate(database.Conn(), "models", Filters{Timezone: "UTC", CostMode: "recorded"}, true); err == nil {
		t.Fatal("recorded cost mode should be rejected")
	}
	missing := filepath.Join(t.TempDir(), "missing.json")
	if err := Generate(database.Conn(), "models", Filters{Timezone: "UTC", CostMode: "estimated", PricingPath: missing, PricingExplicit: true}, true); err == nil {
		t.Fatal("invalid explicit pricing profile should fail")
	}
}

func captureReportOutput(run func() error) (string, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	previous := os.Stdout
	os.Stdout = writer
	runErr := run()
	_ = writer.Close()
	os.Stdout = previous
	data, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if runErr != nil {
		return string(data), runErr
	}
	return string(data), readErr
}

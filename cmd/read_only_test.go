package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func TestReadOnlyCommandsDoNotCreateMissingState(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	if _, _, err := openReadOnlyConfiguredDatabase(); err == nil {
		t.Fatal("ordinary read-only open should reject missing DB")
	}
	if _, _, err := openReadOnlyV3ConfiguredDatabase(); err == nil {
		t.Fatal("v3 read-only open should reject missing DB")
	}
	if _, err := os.Stat(config.ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("read-only command created config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "agent-ledger.db")); !os.IsNotExist(err) {
		t.Fatalf("read-only command created DB: %v", err)
	}
}

func TestStatusReportsV3FactsOnly(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	cfg := config.Default()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	output, err := captureCommandStdout(func() error { return statusCmd.RunE(statusCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Schema version:    3") {
		t.Fatalf("status missing v3 schema: %s", output)
	}
	for _, removed := range []string{"Recorded cost", "request", "TTFT", "TPS", "duration"} {
		if strings.Contains(output, removed) {
			t.Fatalf("status contains removed metric %q: %s", removed, output)
		}
	}
}

func captureCommandStdout(run func() error) (string, error) {
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

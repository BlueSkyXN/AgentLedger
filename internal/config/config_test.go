package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadOnlyUsesDefaultsWithoutCreatingFiles(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-dir")
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

	cfg, err := LoadReadOnly()
	if err != nil {
		t.Fatalf("load read-only: %v", err)
	}
	if cfg.DBPath() != filepath.Join(dataDir, "agent-ledger.db") {
		t.Fatalf("database path = %q", cfg.DBPath())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("read-only load created data directory: %v", err)
	}
}

func TestLoadReadOnlyReadsExistingConfig(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

	cfg := Default()
	cfg.Reports.Timezone = "UTC"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	loaded, err := LoadReadOnly()
	if err != nil {
		t.Fatalf("load read-only: %v", err)
	}
	if loaded.Reports.Timezone != "UTC" {
		t.Fatalf("timezone = %q, want UTC", loaded.Reports.Timezone)
	}
}

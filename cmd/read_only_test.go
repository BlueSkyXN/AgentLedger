package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func TestOpenReadOnlyConfiguredDatabaseDoesNotCreateMissingState(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-dir")
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

	if _, database, err := openReadOnlyConfiguredDatabase(); err == nil {
		_ = database.Close()
		t.Fatal("expected missing database error")
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("read-only command setup created local state: %v", err)
	}
}

func TestOpenReadOnlyConfiguredDatabaseUsesExistingState(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

	cfg := config.Default()
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	writer, err := db.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	loaded, reader, err := openReadOnlyConfiguredDatabase()
	if err != nil {
		t.Fatalf("open configured read-only database: %v", err)
	}
	defer reader.Close()
	if loaded.DBPath() != cfg.DBPath() {
		t.Fatalf("database path = %q, want %q", loaded.DBPath(), cfg.DBPath())
	}
	var queryOnly int
	if err := reader.Conn().QueryRow(`PRAGMA query_only`).Scan(&queryOnly); err != nil {
		t.Fatalf("query_only: %v", err)
	}
	if queryOnly != 1 {
		t.Fatalf("query_only = %d, want 1", queryOnly)
	}
}

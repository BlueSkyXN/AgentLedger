package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func TestMergeRequiresExistingV3Destination(t *testing.T) {
	incomingPath := filepath.Join(t.TempDir(), "incoming.aldb")
	incoming, err := db.Open(incomingPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = incoming.Close()

	missingPath := filepath.Join(t.TempDir(), "missing.db")
	if _, err := mergeDatabase(missingPath, incomingPath); err == nil {
		t.Fatal("expected missing destination error")
	}
	if _, err := os.Stat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("merge created missing destination: %v", err)
	}

	v2Path := filepath.Join(t.TempDir(), "v2.db")
	conn, err := sql.Open("sqlite3", v2Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO meta VALUES ('schema_version', '2')`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if _, err := mergeDatabase(v2Path, incomingPath); err == nil {
		t.Fatal("expected v2 destination error")
	}
	conn, err = sql.Open("sqlite3", v2Path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var version string
	if err := conn.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil || version != "2" {
		t.Fatalf("merge mutated v2 destination: version=%q err=%v", version, err)
	}
}

package cmd

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func TestOpenReadOnlyConfiguredDatabaseDoesNotCreateMissingState(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-dir")
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

	for name, opener := range map[string]func() (*config.Config, *db.Database, error){
		"sqlite": openReadOnlyConfiguredDatabase,
		"v2":     openReadOnlyV2ConfiguredDatabase,
	} {
		_, database, err := opener()
		if err == nil {
			_ = database.Close()
			t.Fatalf("expected %s missing database error", name)
		}
		if !strings.Contains(err.Error(), "agent-ledger init") || !strings.Contains(err.Error(), "agent-ledger import") {
			t.Fatalf("%s missing database error does not include initialization guidance: %v", name, err)
		}
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

	for name, opener := range map[string]func() (*config.Config, *db.Database, error){
		"sqlite": openReadOnlyConfiguredDatabase,
		"v2":     openReadOnlyV2ConfiguredDatabase,
	} {
		loaded, reader, err := opener()
		if err != nil {
			t.Fatalf("open configured %s read-only database: %v", name, err)
		}
		if loaded.DBPath() != cfg.DBPath() {
			_ = reader.Close()
			t.Fatalf("database path = %q, want %q", loaded.DBPath(), cfg.DBPath())
		}
		var queryOnly int
		if err := reader.Conn().QueryRow(`PRAGMA query_only`).Scan(&queryOnly); err != nil {
			_ = reader.Close()
			t.Fatalf("%s query_only: %v", name, err)
		}
		if err := reader.Close(); err != nil {
			t.Fatalf("close configured %s read-only database: %v", name, err)
		}
		if queryOnly != 1 {
			t.Fatalf("%s query_only = %d, want 1", name, queryOnly)
		}
	}
}

func TestDoctorDoesNotCreateMissingState(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "all agents"},
		{name: "codex", args: []string{"codex"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dataDir := filepath.Join(root, "missing-data-dir")
			homeDir := filepath.Join(root, "home")
			if err := os.MkdirAll(homeDir, 0o755); err != nil {
				t.Fatalf("create home: %v", err)
			}
			t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
			t.Setenv("HOME", homeDir)

			if err := discardStdout(t, func() error { return doctorCmd.RunE(doctorCmd, tc.args) }); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
				t.Fatalf("doctor created local state: %v", err)
			}
		})
	}
}

func TestVerifyAcceptsSchemaV1WithoutMutatingIt(t *testing.T) {
	cfg := prepareReadOnlyCommandConfig(t)
	conn, err := sql.Open("sqlite3", cfg.DBPath())
	if err != nil {
		t.Fatalf("open v1: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO meta (key, value) VALUES ('schema_version', '1')`); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close v1: %v", err)
	}

	if err := discardStdout(t, func() error { return verifyCmd.RunE(verifyCmd, nil) }); err != nil {
		t.Fatalf("verify v1: %v", err)
	}
	conn, err = sql.Open("sqlite3", cfg.DBPath())
	if err != nil {
		t.Fatalf("reopen v1: %v", err)
	}
	defer conn.Close()
	var version string
	if err := conn.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read v1 version: %v", err)
	}
	if version != "1" {
		t.Fatalf("verify changed schema version to %q", version)
	}
}

func TestVerifyAcceptsOrdinarySQLiteWithoutMutatingIt(t *testing.T) {
	cfg := prepareReadOnlyCommandConfig(t)
	conn, err := sql.Open("sqlite3", cfg.DBPath())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE sample (value TEXT); INSERT INTO sample VALUES ('kept')`); err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	want, err := os.ReadFile(cfg.DBPath())
	if err != nil {
		t.Fatalf("read sqlite: %v", err)
	}
	if err := discardStdout(t, func() error { return verifyCmd.RunE(verifyCmd, nil) }); err != nil {
		t.Fatalf("verify sqlite: %v", err)
	}
	got, err := os.ReadFile(cfg.DBPath())
	if err != nil {
		t.Fatalf("reread sqlite: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("verify modified the ordinary SQLite database")
	}
}

func TestVerifyAcceptsIncompleteV2WhileV2CommandsRejectIt(t *testing.T) {
	cfg := prepareReadOnlyCommandConfig(t)
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open v2: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close v2: %v", err)
	}

	conn, err := sql.Open("sqlite3", cfg.DBPath())
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := conn.Exec(`ALTER TABLE usage_events DROP COLUMN total_tokens`); err != nil {
		t.Fatalf("drop required column: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	if err := discardStdout(t, func() error { return verifyCmd.RunE(verifyCmd, nil) }); err != nil {
		t.Fatalf("verify incomplete v2: %v", err)
	}
	for name, run := range map[string]func() error{
		"status": func() error { return statusCmd.RunE(statusCmd, nil) },
		"report": func() error { return reportModelsCmd.RunE(reportModelsCmd, nil) },
		"serve":  func() error { return serveCmd.RunE(serveCmd, nil) },
	} {
		err := run()
		if err == nil {
			t.Fatalf("%s accepted incomplete v2 schema", name)
		}
		if !strings.Contains(err.Error(), "agent-ledger init") {
			t.Fatalf("%s error does not include upgrade guidance: %v", name, err)
		}
	}

	conn, err = sql.Open("sqlite3", cfg.DBPath())
	if err != nil {
		t.Fatalf("reopen incomplete v2: %v", err)
	}
	defer conn.Close()
	rows, err := conn.Query(`PRAGMA table_info(usage_events)`)
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	foundTotalTokens := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		foundTotalTokens = foundTotalTokens || name == "total_tokens"
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close rows: %v", err)
	}
	if foundTotalTokens {
		t.Fatal("read-only commands repaired the missing total_tokens column")
	}
}

func TestVerifyRejectsNonSQLiteWithoutReplacingIt(t *testing.T) {
	cfg := prepareReadOnlyCommandConfig(t)
	want := []byte("not a sqlite database")
	if err := os.WriteFile(cfg.DBPath(), want, 0o644); err != nil {
		t.Fatalf("write invalid database: %v", err)
	}

	if err := discardStdout(t, func() error { return verifyCmd.RunE(verifyCmd, nil) }); err == nil {
		t.Fatal("verify accepted a non-SQLite file")
	}
	got, err := os.ReadFile(cfg.DBPath())
	if err != nil {
		t.Fatalf("read invalid database: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("verify replaced invalid database: %q", got)
	}
}

func TestVerifyRejectsCorruptSQLiteWithoutReplacingIt(t *testing.T) {
	cfg := prepareReadOnlyCommandConfig(t)
	conn, err := sql.Open("sqlite3", cfg.DBPath())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := conn.Exec(`
		CREATE TABLE first_table (value INTEGER);
		CREATE TABLE second_table (value INTEGER);
		INSERT INTO first_table VALUES (1);
		INSERT INTO second_table VALUES (2);
		PRAGMA writable_schema = ON;
		UPDATE sqlite_schema
		SET rootpage = (SELECT rootpage FROM sqlite_schema WHERE name = 'first_table')
		WHERE name = 'second_table';
		PRAGMA writable_schema = OFF;
	`); err != nil {
		t.Fatalf("create corrupt sqlite: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close corrupt sqlite: %v", err)
	}

	want, err := os.ReadFile(cfg.DBPath())
	if err != nil {
		t.Fatalf("read corrupt sqlite: %v", err)
	}
	if err := discardStdout(t, func() error { return verifyCmd.RunE(verifyCmd, nil) }); err == nil {
		t.Fatal("verify accepted a corrupt SQLite database")
	}
	got, err := os.ReadFile(cfg.DBPath())
	if err != nil {
		t.Fatalf("reread corrupt sqlite: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("verify modified the corrupt SQLite database")
	}
}

func prepareReadOnlyCommandConfig(t *testing.T) *config.Config {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	cfg := config.Default()
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return cfg
}

func discardStdout(t *testing.T, fn func() error) error {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	previous := os.Stdout
	os.Stdout = writer
	runErr := fn()
	if err := writer.Close(); err != nil {
		os.Stdout = previous
		_ = reader.Close()
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = previous
	if _, err := io.Copy(io.Discard, reader); err != nil {
		_ = reader.Close()
		t.Fatalf("discard stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return runErr
}

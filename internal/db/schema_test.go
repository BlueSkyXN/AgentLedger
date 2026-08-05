package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaV3ContainsOnlyFactColumns(t *testing.T) {
	database := openTestDatabase(t)
	defer database.Close()

	version, exists, err := database.schemaVersion()
	if err != nil || !exists || version != SchemaVersion {
		t.Fatalf("schema version = %q exists=%v err=%v", version, exists, err)
	}
	identity, exists, err := database.identityVersion()
	if err != nil || !exists || identity != IdentityVersion {
		t.Fatalf("identity version = %q exists=%v err=%v", identity, exists, err)
	}

	columns, err := tableColumns(database.Conn(), "usage_events")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range v3RequiredTableSchemas[2].columns {
		if _, ok := columns[required]; !ok {
			t.Errorf("missing v3 column %s", required)
		}
	}
	for _, removed := range []string{
		"dedupe_key", "source_agent", "request_count", "request_started_at_ms",
		"first_token_at_ms", "completed_at_ms", "total_duration_ms", "ttft_ms",
		"output_duration_ms", "output_tps", "recorded_cost_usd", "raw_usage_json",
	} {
		if _, ok := columns[removed]; ok {
			t.Errorf("removed v2 column still exists: %s", removed)
		}
	}

	var tables int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 3 {
		t.Fatalf("v3 should contain exactly three application tables, got %d", tables)
	}
}

func TestV3OpenRejectsV2WithoutMutatingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
        INSERT INTO meta VALUES ('schema_version', '2');`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	_, err = Open(path)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("Open(v2) error = %v", err)
	}
	conn, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var version string
	if err := conn.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "2" {
		t.Fatalf("v2 source was mutated to %q", version)
	}
}

func TestV3OpenRejectsIdentityV1WithoutUpgradingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity-v1.db")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
        INSERT INTO meta VALUES ('schema_version', '3');
        INSERT INTO meta VALUES ('identity_version', '1');`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	_, err = Open(path)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("Open(identity v1) error = %v", err)
	}
	conn, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var identity string
	if err := conn.QueryRow(`SELECT value FROM meta WHERE key='identity_version'`).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if identity != "1" {
		t.Fatalf("identity version was silently upgraded to %q", identity)
	}
}

func TestOpenReadOnlyV3DoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	_, err := OpenReadOnlyV3(path)
	if err == nil {
		t.Fatal("expected missing database error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("read-only open created database: %v", statErr)
	}
}

func TestOpenReadOnlyV3RejectsUnexpectedLegacyColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-column.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`ALTER TABLE usage_events ADD COLUMN recorded_cost_usd REAL`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	_, err = OpenReadOnlyV3(path)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("unexpected legacy column must fail the v3 gate: %v", err)
	}
}

func TestOpenReadOnlyV3RejectsUnexpectedApplicationObjects(t *testing.T) {
	for _, test := range []struct {
		name string
		sql  string
	}{
		{name: "table", sql: `CREATE TABLE usage_observations (raw_usage_json TEXT)`},
		{name: "view", sql: `CREATE VIEW usage_private AS SELECT project_path FROM usage_events`},
		{name: "trigger", sql: `CREATE TRIGGER usage_shadow AFTER INSERT ON usage_events BEGIN UPDATE meta SET value=value WHERE key='schema_version'; END`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "extra-object.db")
			database, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Conn().Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()

			_, err = OpenReadOnlyV3(path)
			if !errors.Is(err, ErrIncompatibleSchema) {
				t.Fatalf("unexpected %s must fail the v3 gate: %v", test.name, err)
			}
		})
	}
}

func openTestDatabase(t *testing.T) *Database {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

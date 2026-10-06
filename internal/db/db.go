package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

const (
	sqliteDriverName     = "agentledger_sqlite3"
	readOnlyMaxOpenConns = 4
)

var timeLocationCache sync.Map

type Database struct {
	conn *sql.DB
	path string
	// schemaVer is the validated schema version ("4", or legacy "3" on
	// read-only paths); empty until validateReadOnlySchema succeeds.
	schemaVer string
}

func init() {
	sql.Register(sqliteDriverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			if err := conn.RegisterFunc("agentledger_project_label", projectLabel, true); err != nil {
				return err
			}
			return conn.RegisterFunc("agentledger_time_bucket", timeBucket, true)
		},
	})
}

func Open(path string) (*Database, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	dsn, err := SQLiteFileURI(path, url.Values{
		"_journal_mode": {"WAL"},
		"_synchronous":  {"NORMAL"},
		"_busy_timeout": {"5000"},
		"_foreign_keys": {"ON"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve database path: %w", err)
	}
	conn, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	conn.SetMaxOpenConns(1)

	db := &Database{conn: conn, path: path}
	if err := db.initSchema(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// OpenReadOnly opens an existing SQLite database without creating or migrating it.
func OpenReadOnly(path string) (*Database, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("database does not exist; run `agent-ledger init` to create it or `agent-ledger import` to initialize and load data: %w", err)
		}
		return nil, fmt.Errorf("failed to access database: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("failed to open database: path is a directory")
	}

	dsn, err := SQLiteFileURI(path, url.Values{
		"mode":          {"ro"},
		"_query_only":   {"on"},
		"_busy_timeout": {"5000"},
		"_foreign_keys": {"on"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve database path: %w", err)
	}

	conn, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	// The panel issues several independent read-only aggregations in parallel.
	// A small pool avoids head-of-line blocking while keeping local SQLite
	// resource use bounded. DSN pragmas and ConnectHook functions apply to each
	// connection opened by database/sql.
	conn.SetMaxOpenConns(readOnlyMaxOpenConns)
	conn.SetMaxIdleConns(readOnlyMaxOpenConns)

	db := &Database{conn: conn, path: path}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	return db, nil
}

// OpenReadOnlyV3 opens an existing identity-v2 database (schema v4, or legacy
// v3 whose cache TTL split reads as unknown) without creating or migrating it.
func OpenReadOnlyV3(path string) (*Database, error) {
	db, err := OpenReadOnly(path)
	if err != nil {
		return nil, err
	}
	if err := db.validateReadOnlySchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to validate database: %w", err)
	}
	return db, nil
}

// OpenReadWriteV3 opens an existing complete current-schema (v4) database
// without creating, initializing, or migrating it. It is for narrowly-scoped
// maintenance that must not trigger startup schema maintenance; a legacy v3
// database is rejected with a hint to migrate it through import or init.
func OpenReadWriteV3(path string) (*Database, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("database does not exist; run `agent-ledger init` to create it or `agent-ledger import` to initialize and load data: %w", err)
		}
		return nil, fmt.Errorf("failed to access database: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("failed to open database: path is a directory")
	}

	dsn, err := SQLiteFileURI(path, url.Values{
		"mode":          {"rw"},
		"_busy_timeout": {"5000"},
		"_foreign_keys": {"on"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve database path: %w", err)
	}

	conn, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	conn.SetMaxOpenConns(1)

	db := &Database{conn: conn, path: path}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := db.validateReadOnlySchema(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to validate database: %w", err)
	}
	if err := db.requireCurrentSchema(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to validate database: %w", err)
	}
	return db, nil
}

func projectLabel(projectPath any) string {
	var value string
	switch typed := projectPath.(type) {
	case nil:
		value = ""
	case string:
		value = typed
	case []byte:
		value = string(typed)
	default:
		value = fmt.Sprint(typed)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "no-project"
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return "no-project"
	}
	if strings.Contains(normalized, "/") {
		base := path.Base(normalized)
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	return normalized
}

func sqliteURIPath(absPath string, pathSeparator uint8) string {
	if pathSeparator == '\\' {
		normalized := strings.ReplaceAll(absPath, "\\", "/")
		if len(normalized) >= 2 && normalized[1] == ':' && ((normalized[0] >= 'A' && normalized[0] <= 'Z') || (normalized[0] >= 'a' && normalized[0] <= 'z')) {
			return "/" + normalized
		}
		return normalized
	}
	return absPath
}

// SQLiteFileURI constructs an absolute SQLite file URI and escapes path
// characters independently from SQLite query parameters.
func SQLiteFileURI(filePath string, query url.Values) (string, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	return sqliteFileURI(absPath, os.PathSeparator, query), nil
}

func sqliteFileURI(absPath string, pathSeparator uint8, query url.Values) string {
	uri := &url.URL{Scheme: "file", Path: sqliteURIPath(absPath, pathSeparator)}
	uri.RawQuery = query.Encode()
	return uri.String()
}

// timeBucket calculates calendar buckets using the offset that applied to the
// individual event. This is intentionally a Go SQLite function because SQLite's
// built-in date modifiers cannot apply historical IANA timezone/DST rules.
func timeBucket(timestampMs int64, timezone, bucket string) (string, error) {
	timezone = strings.TrimSpace(timezone)
	location, err := cachedTimeLocation(timezone)
	if err != nil {
		return "", fmt.Errorf("invalid reports timezone %q: %w", timezone, err)
	}
	local := time.UnixMilli(timestampMs).In(location)
	switch bucket {
	case "daily":
		return local.Format("2006-01-02"), nil
	case "weekly":
		weekday := (int(local.Weekday()) + 6) % 7
		monday := local.AddDate(0, 0, -weekday)
		return monday.Format("2006-01-02"), nil
	case "monthly":
		return local.Format("2006-01"), nil
	default:
		return "", fmt.Errorf("unsupported time bucket %q", bucket)
	}
}

func cachedTimeLocation(name string) (*time.Location, error) {
	if cached, ok := timeLocationCache.Load(name); ok {
		return cached.(*time.Location), nil
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	actual, _ := timeLocationCache.LoadOrStore(name, location)
	return actual.(*time.Location), nil
}

func (d *Database) Close() error {
	return d.conn.Close()
}

func (d *Database) Conn() *sql.DB {
	return d.conn
}

func (d *Database) Path() string {
	return d.path
}
